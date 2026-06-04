package main

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/usewhale/whale/internal/agent"
	"github.com/usewhale/whale/internal/build"
	"github.com/usewhale/whale/internal/core"
	"github.com/usewhale/whale/internal/defaults"
	"github.com/usewhale/whale/internal/llm/deepseek"
	"github.com/usewhale/whale/internal/policy"
	"github.com/usewhale/whale/internal/store"
	"github.com/usewhale/whale/internal/telemetry"
	"github.com/usewhale/whale/internal/tools"
)

type cliArgs struct {
	taskFilter  string
	repeats     int
	model       string
	effort      string
	outDir      string
	timeout     time.Duration
	transcripts bool
	verbose     bool
	dry         bool
}

func parseArgs() cliArgs {
	var args cliArgs
	flag.StringVar(&args.taskFilter, "task", "", "Run only one task id")
	flag.IntVar(&args.repeats, "repeats", 3, "Repeats per task")
	flag.StringVar(&args.model, "model", defaults.DefaultModel, "DeepSeek model")
	flag.StringVar(&args.effort, "effort", defaults.DefaultReasoningEffort, "DeepSeek reasoning effort")
	flag.StringVar(&args.outDir, "out", "", "Output directory")
	flag.DurationVar(&args.timeout, "timeout", 10*time.Minute, "Whole-run timeout")
	flag.BoolVar(&args.transcripts, "transcripts", true, "Write per-run JSONL transcripts")
	flag.BoolVar(&args.verbose, "verbose", false, "Print per-turn progress")
	flag.BoolVar(&args.dry, "dry", false, "Validate task setup without DeepSeek calls")
	flag.Parse()
	if args.repeats < 1 {
		args.repeats = 1
	}
	args.model = strings.TrimSpace(args.model)
	if args.model == "" {
		args.model = defaults.DefaultModel
	}
	args.effort = strings.TrimSpace(args.effort)
	if args.effort == "" {
		args.effort = defaults.DefaultReasoningEffort
	}
	if args.outDir == "" {
		args.outDir = filepath.Join("tmp", "bench", "live-cache", time.Now().UTC().Format("20060102T150405Z"))
	}
	return args
}

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func run() error {
	args := parseArgs()
	selected, err := selectTasks(args.taskFilter)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(args.outDir, 0o755); err != nil {
		return err
	}
	if args.dry {
		for _, task := range selected {
			root := filepath.Join(args.outDir, "dry", task.ID)
			if err := os.RemoveAll(root); err != nil {
				return err
			}
			if err := os.MkdirAll(root, 0o755); err != nil {
				return err
			}
			if err := task.Setup(root); err != nil {
				return fmt.Errorf("%s setup: %w", task.ID, err)
			}
			fmt.Printf("[%s] dry setup ok\n", task.ID)
		}
		return nil
	}
	if os.Getenv("DEEPSEEK_API_KEY") == "" {
		return errors.New("DEEPSEEK_API_KEY is required for live cache benchmark")
	}

	ctx, cancel := context.WithTimeout(context.Background(), args.timeout)
	defer cancel()

	results := make([]runResult, 0, len(selected)*args.repeats)
	for _, task := range selected {
		for rep := 1; rep <= args.repeats; rep++ {
			result := runTask(ctx, args, task, rep)
			results = append(results, result)
			status := "fail"
			if result.Pass {
				status = "pass"
			}
			fmt.Printf("[%s/r%d] %s turns=%d tools=%d cache=%s cost=$%.6f\n",
				task.ID, rep, status, result.Turns, result.ToolCalls, pctFloat(result.CacheHitRatio), result.CostUSD)
		}
	}

	report := benchReport{
		Meta: benchMeta{
			Date:           time.Now().UTC().Format(time.RFC3339),
			Model:          args.model,
			Effort:         args.effort,
			TaskCount:      len(selected),
			RepeatsPerTask: args.repeats,
			WhaleVersion:   build.CurrentVersion(),
			LiveDeepSeek:   true,
		},
		Results: results,
	}
	if err := writeOutputs(args.outDir, report); err != nil {
		return err
	}
	fmt.Printf("wrote %s\n", args.outDir)
	return nil
}

func runTask(parent context.Context, args cliArgs, task taskSpec, repeat int) (result runResult) {
	started := time.Now()
	runID := fmt.Sprintf("%s.r%d", task.ID, repeat)
	workspace := filepath.Join(args.outDir, "workspaces", runID)
	dataDir := filepath.Join(args.outDir, "data", runID)
	transcriptPath := ""
	if args.transcripts {
		transcriptPath = filepath.Join(args.outDir, "transcripts", runID+".jsonl")
	}
	result = runResult{
		TaskID:         task.ID,
		Repeat:         repeat,
		Workspace:      workspace,
		TranscriptPath: transcriptPath,
	}
	defer func() {
		result.DurationMS = time.Since(started).Milliseconds()
	}()

	if err := os.RemoveAll(workspace); err != nil {
		result.Error = err.Error()
		return result
	}
	if err := os.MkdirAll(workspace, 0o755); err != nil {
		result.Error = err.Error()
		return result
	}
	if err := task.Setup(workspace); err != nil {
		result.Error = err.Error()
		return result
	}

	records := []transcriptRecord{{
		TS:    nowStamp(),
		Event: "meta",
		Metadata: map[string]any{
			"task":      task.ID,
			"repeat":    repeat,
			"model":     args.model,
			"workspace": workspace,
		},
	}}
	writer, err := newTranscriptWriter(transcriptPath)
	if err != nil {
		result.Error = err.Error()
		return result
	}
	defer writer.Close()
	_ = writer.Write(records[0])

	provider, err := deepseek.New(
		deepseek.WithModel(args.model),
		deepseek.WithReasoningEffort(args.effort),
		deepseek.WithThinking(true),
	)
	if err != nil {
		result.Error = err.Error()
		return result
	}
	msgStore, err := store.NewJSONLStore(filepath.Join(dataDir, "sessions"))
	if err != nil {
		result.Error = err.Error()
		return result
	}
	toolset, err := tools.NewToolset(workspace)
	if err != nil {
		result.Error = err.Error()
		return result
	}
	registry, err := core.NewToolRegistryChecked(toolset.Tools())
	if err != nil {
		result.Error = err.Error()
		return result
	}
	usagePath := filepath.Join(dataDir, "usage.jsonl")
	ag := agent.NewAgentWithRegistry(
		provider,
		msgStore,
		registry,
		agent.WithToolPolicy(policy.RulePolicy{Default: policy.PermissionAllow}),
		agent.WithUsageLogPath(usagePath),
		agent.WithSessionsDir(filepath.Join(dataDir, "sessions")),
		agent.WithAutoCompact(false, 0, defaults.DeepSeekV4ContextWindow),
		agent.WithProjectMemory(false, 0, nil, workspace),
	)

	sessionID := runID
	var finalOutput string
	for i, prompt := range task.Prompts {
		turn := i + 1
		rec := transcriptRecord{TS: nowStamp(), Turn: turn, Role: "user", Content: prompt}
		records = append(records, rec)
		_ = writer.Write(rec)
		if args.verbose {
			fmt.Printf("  [%s] user%d: %s\n", runID, turn, prompt)
		}
		events, err := ag.RunStreamWithOptions(parent, sessionID, prompt, false)
		if err != nil {
			result.Error = err.Error()
			break
		}
		var turnText strings.Builder
		for ev := range events {
			eventRec, ok := recordAgentEvent(turn, ev)
			if ok {
				records = append(records, eventRec)
				_ = writer.Write(eventRec)
			}
			switch ev.Type {
			case agent.AgentEventTypeAssistantDelta:
				turnText.WriteString(ev.Content)
			case agent.AgentEventTypeToolResult:
				result.ToolCalls++
			case agent.AgentEventTypeDone:
				if ev.Message != nil {
					finalOutput = ev.Message.Text
					if turnText.Len() == 0 {
						turnText.WriteString(ev.Message.Text)
					}
				}
			case agent.AgentEventTypeError:
				if ev.Err != nil {
					result.Error = ev.Err.Error()
				}
			}
		}
		result.Turns++
		if strings.TrimSpace(finalOutput) == "" {
			finalOutput = turnText.String()
		}
		if result.Error != "" {
			break
		}
	}
	result.FinalOutput = finalOutput

	totals, err := readUsageTotals(usagePath)
	if err != nil && !os.IsNotExist(err) {
		result.Error = strings.TrimSpace(result.Error + "; " + err.Error())
	}
	result.PromptTokens = totals.PromptTokens
	result.CompletionTokens = totals.CompletionTokens
	result.CacheHitTokens = totals.CacheHitTokens
	result.CacheMissTokens = totals.CacheMissTokens
	result.CacheHitRatio = totals.CacheHitRatio()
	result.CostUSD = totals.CostUSD

	if result.Error == "" {
		if err := task.Check(workspace, records, finalOutput); err != nil {
			result.Error = err.Error()
		} else {
			result.Pass = true
		}
	}
	return result
}

func recordAgentEvent(turn int, ev agent.AgentEvent) (transcriptRecord, bool) {
	rec := transcriptRecord{TS: nowStamp(), Turn: turn, Event: string(ev.Type)}
	switch ev.Type {
	case agent.AgentEventTypeAssistantDelta:
		rec.Role = "assistant_delta"
		rec.Content = ev.Content
	case agent.AgentEventTypeToolCall:
		if ev.ToolCall == nil {
			return transcriptRecord{}, false
		}
		rec.Role = "tool_call"
		rec.Tool = ev.ToolCall.Name
		rec.Content = ev.ToolCall.Input
	case agent.AgentEventTypeToolResult:
		if ev.Result == nil {
			return transcriptRecord{}, false
		}
		success := !ev.Result.IsError
		rec.Role = "tool"
		rec.Tool = ev.Result.Name
		rec.Success = &success
		rec.Content = ev.Result.Content
	case agent.AgentEventTypePrefixCacheMetrics:
		if ev.CacheMetrics == nil {
			return transcriptRecord{}, false
		}
		rec.Model = ev.CacheMetrics.Model
		rec.PrefixHash = ev.CacheMetrics.PrefixFingerprint
		if ev.CacheMetrics.CacheShape != nil {
			rec.CacheShape = map[string]any{
				"request_kind":           ev.CacheMetrics.CacheShape.RequestKind,
				"system_hash":            ev.CacheMetrics.CacheShape.SystemHash,
				"system_segments":        ev.CacheMetrics.CacheShape.SystemSegments,
				"system_bytes":           ev.CacheMetrics.CacheShape.SystemBytes,
				"tools_hash":             ev.CacheMetrics.CacheShape.ToolsHash,
				"tools_bytes":            ev.CacheMetrics.CacheShape.ToolsBytes,
				"fewshot_hash":           ev.CacheMetrics.CacheShape.FewShotHash,
				"assistant_prefix_hash":  ev.CacheMetrics.CacheShape.AssistantPrefixHash,
				"assistant_prefix_bytes": ev.CacheMetrics.CacheShape.AssistantPrefixBytes,
				"log_head_hash":          ev.CacheMetrics.CacheShape.LogHeadHash,
				"log_head_bytes":         ev.CacheMetrics.CacheShape.LogHeadBytes,
				"log_tail_hash":          ev.CacheMetrics.CacheShape.LogTailHash,
				"log_tail_bytes":         ev.CacheMetrics.CacheShape.LogTailBytes,
				"request_hash":           ev.CacheMetrics.CacheShape.RequestHash,
				"log_messages":           ev.CacheMetrics.CacheShape.LogMessages,
				"tail_messages":          ev.CacheMetrics.CacheShape.TailMessages,
			}
		}
		rec.PromptTokens = ev.CacheMetrics.PromptTokens
		rec.CachedTokens = ev.CacheMetrics.CachedTokens
		rec.CacheHitRatio = ev.CacheMetrics.CacheHitRatio
	case agent.AgentEventTypeDone:
		if ev.Message == nil {
			return transcriptRecord{}, false
		}
		rec.Role = "assistant_final"
		rec.Content = ev.Message.Text
	case agent.AgentEventTypeError:
		if ev.Err == nil {
			return transcriptRecord{}, false
		}
		rec.Error = ev.Err.Error()
	default:
		return transcriptRecord{}, false
	}
	return rec, true
}

func readUsageTotals(path string) (usageTotals, error) {
	f, err := os.Open(path)
	if err != nil {
		return usageTotals{}, err
	}
	defer f.Close()
	var totals usageTotals
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		var rec telemetry.UsageRecord
		if err := json.Unmarshal(sc.Bytes(), &rec); err != nil {
			return totals, err
		}
		totals.PromptTokens += rec.PromptTokens
		totals.CompletionTokens += rec.CompletionTokens
		totals.CacheHitTokens += rec.PromptCacheHit
		totals.CacheMissTokens += rec.PromptCacheMiss
		totals.CostUSD += rec.CostUSD
	}
	return totals, sc.Err()
}

type transcriptWriter struct {
	f *os.File
}

func newTranscriptWriter(path string) (*transcriptWriter, error) {
	if path == "" {
		return &transcriptWriter{}, nil
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return nil, err
	}
	f, err := os.Create(path)
	if err != nil {
		return nil, err
	}
	return &transcriptWriter{f: f}, nil
}

func (w *transcriptWriter) Write(rec transcriptRecord) error {
	if w == nil || w.f == nil {
		return nil
	}
	b, err := json.Marshal(rec)
	if err != nil {
		return err
	}
	_, err = w.f.Write(append(b, '\n'))
	return err
}

func (w *transcriptWriter) Close() error {
	if w == nil || w.f == nil {
		return nil
	}
	return w.f.Close()
}

func writeOutputs(outDir string, report benchReport) error {
	b, err := json.MarshalIndent(report, "", "  ")
	if err != nil {
		return err
	}
	if err := os.WriteFile(filepath.Join(outDir, "results.json"), append(b, '\n'), 0o644); err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(outDir, "report.md"), []byte(renderMarkdown(report)), 0o644)
}

func selectTasks(filter string) ([]taskSpec, error) {
	if strings.TrimSpace(filter) == "" {
		return tasks, nil
	}
	for _, task := range tasks {
		if task.ID == filter {
			return []taskSpec{task}, nil
		}
	}
	return nil, fmt.Errorf("unknown task: %s", filter)
}
