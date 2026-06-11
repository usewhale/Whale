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
	"sort"
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
)

func parseArgs() cliArgs {
	var args cliArgs
	flag.StringVar(&args.taskFilter, "task", "", "Run only one task id")
	flag.StringVar(&args.mode, "mode", "both", "Run mode: whale, baseline, or both")
	flag.IntVar(&args.repeats, "repeats", 1, "Repeats per task")
	flag.StringVar(&args.model, "model", defaults.DefaultModel, "DeepSeek model for the agent")
	flag.StringVar(&args.userModel, "user-model", "deepseek-chat", "DeepSeek model for the user simulator")
	flag.StringVar(&args.effort, "effort", defaults.DefaultReasoningEffort, "DeepSeek reasoning effort for the agent")
	flag.StringVar(&args.outDir, "out", "", "Output directory")
	flag.DurationVar(&args.timeout, "timeout", 20*time.Minute, "Whole-run timeout")
	flag.BoolVar(&args.transcripts, "transcripts", true, "Write per-run JSONL transcripts")
	flag.BoolVar(&args.verbose, "verbose", false, "Print per-turn progress")
	flag.BoolVar(&args.dry, "dry", false, "Validate task and tool wiring without DeepSeek calls")
	flag.Parse()
	if args.repeats < 1 {
		args.repeats = 1
	}
	args.model = strings.TrimSpace(args.model)
	if args.model == "" {
		args.model = defaults.DefaultModel
	}
	args.userModel = strings.TrimSpace(args.userModel)
	if args.userModel == "" {
		args.userModel = "deepseek-chat"
	}
	args.effort = strings.TrimSpace(args.effort)
	if args.effort == "" {
		args.effort = defaults.DefaultReasoningEffort
	}
	if args.outDir == "" {
		args.outDir = filepath.Join("tmp", "bench", "tau-bench-lite", time.Now().UTC().Format("20060102T150405Z"))
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
	selected, err := selectTasks(strings.TrimSpace(args.taskFilter))
	if err != nil {
		return err
	}
	modes, err := parseModes(args.mode)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(args.outDir, 0o755); err != nil {
		return err
	}
	if args.dry {
		return runDry(args, selected, modes)
	}
	if os.Getenv("DEEPSEEK_API_KEY") == "" {
		return errors.New("DEEPSEEK_API_KEY is required for tau-bench-lite")
	}

	ctx, cancel := context.WithTimeout(context.Background(), args.timeout)
	defer cancel()

	results := make([]runResult, 0, len(selected)*args.repeats*len(modes))
	for _, task := range selected {
		for rep := 1; rep <= args.repeats; rep++ {
			for _, mode := range modes {
				result := runTask(ctx, args, task, mode, rep)
				results = append(results, result)
				status := "fail"
				if result.Pass {
					status = "pass"
				}
				fmt.Printf("[%s/%s/r%d] %s turns=%d tools=%d cache=%s cost=$%.6f prefixes=%d truncated=%v\n",
					task.ID, mode, rep, status, result.Turns, result.ToolCalls, pctFloat(result.CacheHitRatio), result.CostUSD, result.PrefixFingerprints, result.Truncated)
			}
		}
	}

	report := benchReport{
		Meta:    buildMeta(args, selected, modes, true),
		Results: results,
	}
	if err := writeOutputs(args.outDir, report); err != nil {
		return err
	}
	fmt.Printf("wrote %s\n", args.outDir)
	return nil
}

func runDry(args cliArgs, selected []taskSpec, modes []string) error {
	results := make([]runResult, 0, len(selected)*len(modes)*args.repeats)
	for _, task := range selected {
		for rep := 1; rep <= args.repeats; rep++ {
			for _, mode := range modes {
				db := cloneDB(task.InitialDB)
				tools := buildRetailTools(&db)
				for _, tool := range tools {
					_, _ = tool.Run(context.Background(), core.ToolCall{ID: "dry", Name: tool.Name(), Input: stubArgs(tool)})
				}
				result := runResult{
					Mode:      mode,
					TaskID:    task.ID,
					Repeat:    rep,
					Pass:      true,
					ToolCalls: len(tools),
				}
				results = append(results, result)
				fmt.Printf("[%s/%s/r%d] dry-run ok (%d tools wired)\n", task.ID, mode, rep, len(tools))
			}
		}
	}
	report := benchReport{
		Meta:    buildMeta(args, selected, modes, false),
		Results: results,
	}
	if err := writeOutputs(args.outDir, report); err != nil {
		return err
	}
	fmt.Printf("wrote %s\n", args.outDir)
	return nil
}

func buildMeta(args cliArgs, selected []taskSpec, modes []string, live bool) benchMeta {
	return benchMeta{
		Date:           time.Now().UTC().Format(time.RFC3339),
		Model:          args.model,
		UserModel:      args.userModel,
		Effort:         args.effort,
		Modes:          append([]string(nil), modes...),
		TaskCount:      len(selected),
		RepeatsPerTask: args.repeats,
		WhaleVersion:   build.CurrentVersion(),
		LiveDeepSeek:   live,
	}
}

func runTask(parent context.Context, args cliArgs, task taskSpec, mode string, repeat int) (result runResult) {
	started := time.Now()
	runID := fmt.Sprintf("%s.%s.r%d", task.ID, mode, repeat)
	dataDir := filepath.Join(args.outDir, "data", runID)
	workspace := filepath.Join(args.outDir, "workspaces", runID)
	transcriptPath := ""
	if args.transcripts {
		transcriptPath = filepath.Join(args.outDir, "transcripts", runID+".jsonl")
	}
	result = runResult{
		Mode:           mode,
		TaskID:         task.ID,
		Repeat:         repeat,
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

	writer, err := newTranscriptWriter(transcriptPath)
	if err != nil {
		result.Error = err.Error()
		return result
	}
	defer writer.Close()
	_ = writer.Write(transcriptRecord{
		TS:    nowStamp(),
		Event: "meta",
		Metadata: map[string]any{
			"task":       task.ID,
			"mode":       mode,
			"repeat":     repeat,
			"model":      args.model,
			"user_model": args.userModel,
		},
	})

	userProvider, err := deepseek.New(
		deepseek.WithModel(args.userModel),
		deepseek.WithThinking(false),
		deepseek.WithMaxTokens(200),
	)
	if err != nil {
		result.Error = err.Error()
		return result
	}
	agentProvider, err := deepseek.New(
		deepseek.WithModel(args.model),
		deepseek.WithReasoningEffort(args.effort),
		deepseek.WithThinking(true),
	)
	if err != nil {
		result.Error = err.Error()
		return result
	}
	db := cloneDB(task.InitialDB)
	retailTools := buildRetailTools(&db)
	registry, err := core.NewToolRegistryChecked(retailTools)
	if err != nil {
		result.Error = err.Error()
		return result
	}
	msgStore, err := store.NewJSONLStore(filepath.Join(dataDir, "sessions"))
	if err != nil {
		result.Error = err.Error()
		return result
	}
	usagePath := filepath.Join(dataDir, "usage.jsonl")
	options := []agent.AgentOption{
		agent.WithExtraSystemBlocks(retailSystemPrompt),
		agent.WithToolPolicy(policy.RulePolicy{Default: policy.PermissionAllow}),
		agent.WithUsageLogPath(usagePath),
		agent.WithSessionsDir(filepath.Join(dataDir, "sessions")),
		agent.WithAutoCompact(false, 0, defaults.DeepSeekV4ContextWindow),
		agent.WithProjectMemory(false, 0, nil, workspace),
	}
	if mode == "baseline" {
		toolTurn := 0
		options = append(options,
			agent.WithDynamicSystemBlocks(volatileBenchmarkBlock()),
			agent.WithToolRefresh(func(context.Context) error {
				toolTurn++
				return registry.ReplaceTools(shuffleTools(retailTools, toolTurn))
			}),
		)
	}
	ag := agent.NewAgentWithRegistry(agentProvider, msgStore, registry, options...)
	sim := newUserSimulator(userProvider, task.User, args.userModel)

	sessionID := runID
	transcript := []turn{}
	var finalOutput string
	for turnNo := 1; turnNo <= taskMaxTurns(task); turnNo++ {
		userMsg, stop, err := sim.next(parent, transcript)
		if err != nil {
			result.Error = err.Error()
			break
		}
		if stop {
			break
		}
		result.Turns++
		transcript = append(transcript, turn{Role: "user", Content: userMsg})
		_ = writer.Write(transcriptRecord{TS: nowStamp(), Turn: turnNo, Role: "user", Content: userMsg})
		if args.verbose {
			fmt.Printf("  [%s] USER: %s\n", runID, userMsg)
		}

		events, err := ag.RunStreamWithOptions(parent, sessionID, userMsg, false)
		if err != nil {
			result.Error = err.Error()
			break
		}
		var turnText strings.Builder
		for ev := range events {
			eventRec, ok := recordAgentEvent(turnNo, ev)
			if ok {
				_ = writer.Write(eventRec)
			}
			switch ev.Type {
			case agent.AgentEventTypeAssistantDelta:
				turnText.WriteString(ev.Content)
			case agent.AgentEventTypeToolResult:
				result.ToolCalls++
				if ev.Result != nil {
					transcript = append(transcript, turn{Role: "tool", ToolName: ev.Result.Name, Content: ev.Result.ModelText})
					if args.verbose {
						fmt.Printf("  [%s] TOOL %s: %s\n", runID, ev.Result.Name, truncate(ev.Result.ModelText, 140))
					}
				}
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
		agentMsg := strings.TrimSpace(turnText.String())
		if agentMsg == "" {
			agentMsg = finalOutput
		}
		finalOutput = agentMsg
		transcript = append(transcript, turn{Role: "agent", Content: agentMsg})
		if args.verbose {
			fmt.Printf("  [%s] AGENT: %s\n", runID, truncate(agentMsg, 140))
		}
		if result.Error != "" {
			break
		}
		if turnNo == taskMaxTurns(task) {
			result.Truncated = true
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
	result.WarmCacheHitTokens = totals.WarmCacheHitTokens
	result.WarmCacheMissTokens = totals.WarmCacheMissTokens
	result.WarmCacheHitRatio = totals.WarmCacheHitRatio()
	result.CostUSD = totals.CostUSD
	result.CacheSavingsUSD = totals.CacheSavingsUSD
	result.UncachedCostUSD = totals.CostUSD + totals.CacheSavingsUSD
	result.PrefixFingerprints = len(totals.PrefixFingerprints)
	result.PrefixFingerprintValues = sortedHashValues(totals.PrefixFingerprints)
	result.ShapePrefixHashes = len(totals.ShapePrefixHashes)
	result.ShapePrefixHashValues = sortedHashValues(totals.ShapePrefixHashes)
	result.ShapeRuntimeHashes = len(totals.ShapeRuntimeHashes)
	result.ShapeRuntimeHashValues = sortedHashValues(totals.ShapeRuntimeHashes)
	result.ShapeToolsHashes = len(totals.ShapeToolsHashes)
	result.ShapeToolsHashValues = sortedHashValues(totals.ShapeToolsHashes)
	result.ShapeRequestHashes = len(totals.ShapeRequestHashes)
	result.ShapeRequestHashValues = sortedHashValues(totals.ShapeRequestHashes)

	if result.Error == "" {
		result.Pass = task.Check(runCheckContext{DB: db, FinalAgentMessage: finalOutput, Transcript: transcript})
		if !result.Pass {
			result.Error = "task check failed"
		}
	}
	return result
}

func volatileBenchmarkBlock() func() string {
	var n int
	return func() string {
		n++
		return fmt.Sprintf("Benchmark volatile prefix marker: baseline-turn-%d at %s", n, time.Now().UTC().Format(time.RFC3339Nano))
	}
}

func shuffleTools(tools []core.Tool, seed int) []core.Tool {
	out := append([]core.Tool(nil), tools...)
	if len(out) < 2 {
		return out
	}
	s := seed*9301 + 49297
	for i := len(out) - 1; i > 0; i-- {
		s = (s*9301 + 49297) % 233280
		j := int(float64(s) / 233280.0 * float64(i+1))
		out[i], out[j] = out[j], out[i]
	}
	return out
}

func sortedHashValues(values map[string]bool) []string {
	if len(values) == 0 {
		return nil
	}
	out := make([]string, 0, len(values))
	for fp := range values {
		out = append(out, fp)
	}
	sort.Strings(out)
	return out
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
		rec.Args = ev.ToolCall.Input
	case agent.AgentEventTypeToolResult:
		if ev.Result == nil {
			return transcriptRecord{}, false
		}
		success := !ev.Result.IsError()
		rec.Role = "tool"
		rec.Tool = ev.Result.Name
		rec.Success = &success
		rec.Content = ev.Result.ModelText
	case agent.AgentEventTypePrefixCacheMetrics:
		if ev.CacheMetrics == nil {
			return transcriptRecord{}, false
		}
		rec.Model = ev.CacheMetrics.Model
		rec.PrefixHash = ev.CacheMetrics.PrefixFingerprint
		rec.PromptTokens = ev.CacheMetrics.PromptTokens
		rec.CachedTokens = ev.CacheMetrics.CachedTokens
		rec.CacheHitRatio = ev.CacheMetrics.CacheHitRatio
	case agent.AgentEventTypeUsage:
		if ev.Usage == nil {
			return transcriptRecord{}, false
		}
		rec.Model = ev.Usage.Model
		rec.Usage = map[string]int{
			"prompt_tokens":              ev.Usage.Usage.PromptTokens,
			"completion_tokens":          ev.Usage.Usage.CompletionTokens,
			"prompt_cache_hit_tokens":    ev.Usage.Usage.PromptCacheHitTokens,
			"prompt_cache_miss_tokens":   ev.Usage.Usage.PromptCacheMissTokens,
			"prefix_completion_requests": ev.Usage.Usage.PrefixCompletionRequests,
			"tool_result_tokens_saved":   ev.Usage.Usage.ToolResultsCompacted,
		}
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
	totals := newUsageTotals()
	lineNo := 0
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		lineNo++
		var rec telemetry.UsageRecord
		if err := json.Unmarshal(sc.Bytes(), &rec); err != nil {
			return totals, err
		}
		totals.PromptTokens += rec.PromptTokens
		totals.CompletionTokens += rec.CompletionTokens
		totals.CacheHitTokens += rec.PromptCacheHit
		totals.CacheMissTokens += rec.PromptCacheMiss
		if lineNo > 1 {
			totals.WarmCacheHitTokens += rec.PromptCacheHit
			totals.WarmCacheMissTokens += rec.PromptCacheMiss
		}
		totals.CostUSD += rec.CostUSD
		totals.CacheSavingsUSD += rec.CacheSavingsUSD
		if fp := strings.TrimSpace(rec.PrefixFingerprint); fp != "" {
			totals.PrefixFingerprints[fp] = true
		}
		recordCacheShapeHashes(&totals, rec.CacheShape)
	}
	return totals, sc.Err()
}

func newUsageTotals() usageTotals {
	return usageTotals{
		PrefixFingerprints: map[string]bool{},
		ShapePrefixHashes:  map[string]bool{},
		ShapeRuntimeHashes: map[string]bool{},
		ShapeToolsHashes:   map[string]bool{},
		ShapeRequestHashes: map[string]bool{},
	}
}

func recordCacheShapeHashes(totals *usageTotals, shape *telemetry.CacheShape) {
	if totals == nil || shape == nil {
		return
	}
	recordHash(totals.ShapePrefixHashes, shape.PrefixHash)
	recordHash(totals.ShapeRuntimeHashes, shape.RuntimeHash)
	recordHash(totals.ShapeToolsHashes, shape.ToolsHash)
	recordHash(totals.ShapeRequestHashes, shape.RequestHash)
}

func recordHash(values map[string]bool, value string) {
	if values == nil {
		return
	}
	if value = strings.TrimSpace(value); value != "" {
		values[value] = true
	}
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

func parseModes(mode string) ([]string, error) {
	switch strings.ToLower(strings.TrimSpace(mode)) {
	case "", "both":
		return []string{"baseline", "whale"}, nil
	case "baseline":
		return []string{"baseline"}, nil
	case "whale":
		return []string{"whale"}, nil
	default:
		return nil, fmt.Errorf("unknown mode: %s", mode)
	}
}

func unknownTaskError(task string) error {
	return fmt.Errorf("unknown task: %s", task)
}
