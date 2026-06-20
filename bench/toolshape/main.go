package main

import (
	"bufio"
	"context"
	"crypto/sha256"
	"encoding/hex"
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
	"github.com/usewhale/whale/internal/tasks"
	"github.com/usewhale/whale/internal/telemetry"
	"github.com/usewhale/whale/internal/tools"
)

type cliArgs struct {
	outDir  string
	live    bool
	repeats int
	model   string
	effort  string
	timeout time.Duration
}

type benchReport struct {
	Meta     benchMeta       `json:"meta"`
	Profiles []profileReport `json:"profiles"`
	LiveRuns []liveRunReport `json:"live_runs,omitempty"`
}

type benchMeta struct {
	Date         string `json:"date"`
	WhaleVersion string `json:"whale_version"`
	Workspace    string `json:"workspace"`
	LiveModel    string `json:"live_model,omitempty"`
	LiveEffort   string `json:"live_effort,omitempty"`
	LiveRepeats  int    `json:"live_repeats,omitempty"`
}

type profileReport struct {
	Name               string       `json:"name"`
	ToolCount          int          `json:"tool_count"`
	ToolsBytes         int          `json:"tools_bytes"`
	ToolsHash          string       `json:"tools_hash"`
	SpawnSubagentBytes int          `json:"spawn_subagent_bytes,omitempty"`
	SpawnSubagentShare float64      `json:"spawn_subagent_share,omitempty"`
	DeltaFromBaseBytes int          `json:"delta_from_base_bytes,omitempty"`
	TopTools           []toolReport `json:"top_tools"`
	Tools              []toolReport `json:"tools"`
}

type toolReport struct {
	Name        string `json:"name"`
	Bytes       int    `json:"bytes"`
	Hash        string `json:"hash"`
	Description int    `json:"description_bytes,omitempty"`
	Parameters  int    `json:"parameters_bytes,omitempty"`
}

type toolProfile struct {
	name    string
	tools   []core.Tool
	summary profileReport
}

type liveRunReport struct {
	Profile             string  `json:"profile"`
	Repeat              int     `json:"repeat"`
	Pass                bool    `json:"pass"`
	ToolCalls           int     `json:"tool_calls"`
	PromptTokens        int     `json:"prompt_tokens"`
	CompletionTokens    int     `json:"completion_tokens"`
	CacheHitTokens      int     `json:"prompt_cache_hit_tokens"`
	CacheMissTokens     int     `json:"prompt_cache_miss_tokens"`
	CacheHitRatio       float64 `json:"cache_hit_ratio"`
	CostUSD             float64 `json:"cost_usd"`
	ToolsBytes          int     `json:"tools_bytes"`
	SystemBytes         int     `json:"system_bytes"`
	RuntimeBytes        int     `json:"runtime_bytes"`
	ToolsHash           string  `json:"tools_hash,omitempty"`
	RequestHash         string  `json:"request_hash,omitempty"`
	SpawnSubagentCalled bool    `json:"spawn_subagent_called"`
	FinalOutput         string  `json:"final_output,omitempty"`
	Error               string  `json:"error,omitempty"`
}

func parseArgs() cliArgs {
	var args cliArgs
	flag.StringVar(&args.outDir, "out", "", "Output directory")
	flag.BoolVar(&args.live, "live", false, "Run a tiny live DeepSeek smoke comparing base vs base+task_tools")
	flag.IntVar(&args.repeats, "repeats", 1, "Live repeats per profile")
	flag.StringVar(&args.model, "model", defaults.DefaultModel, "DeepSeek model for live smoke")
	flag.StringVar(&args.effort, "effort", defaults.DefaultReasoningEffort, "DeepSeek reasoning effort for live smoke")
	flag.DurationVar(&args.timeout, "timeout", 5*time.Minute, "Live smoke timeout")
	flag.Parse()
	if strings.TrimSpace(args.outDir) == "" {
		args.outDir = filepath.Join("tmp", "bench", "tool-shape", time.Now().UTC().Format("20060102T150405Z"))
	}
	if args.repeats < 1 {
		args.repeats = 1
	}
	if strings.TrimSpace(args.model) == "" {
		args.model = defaults.DefaultModel
	}
	if strings.TrimSpace(args.effort) == "" {
		args.effort = defaults.DefaultReasoningEffort
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
	if err := os.MkdirAll(args.outDir, 0o755); err != nil {
		return err
	}
	workspace := filepath.Join(args.outDir, "workspace")
	if err := os.MkdirAll(workspace, 0o755); err != nil {
		return err
	}
	profiles, err := buildProfiles(workspace)
	if err != nil {
		return err
	}
	var liveRuns []liveRunReport
	if args.live {
		if os.Getenv("DEEPSEEK_API_KEY") == "" {
			return errors.New("DEEPSEEK_API_KEY is required for --live")
		}
		liveRuns, err = runLiveSmoke(args, workspace)
		if err != nil {
			return err
		}
	}
	report := benchReport{
		Meta: benchMeta{
			Date:         time.Now().UTC().Format(time.RFC3339),
			WhaleVersion: build.CurrentVersion(),
			Workspace:    workspace,
		},
		Profiles: profiles,
		LiveRuns: liveRuns,
	}
	if args.live {
		report.Meta.LiveModel = args.model
		report.Meta.LiveEffort = args.effort
		report.Meta.LiveRepeats = args.repeats
	}
	if err := writeOutputs(args.outDir, report); err != nil {
		return err
	}
	fmt.Printf("wrote %s\n", args.outDir)
	return nil
}

func buildProfiles(workspace string) ([]profileReport, error) {
	profiles, err := buildToolProfiles(workspace, true)
	if err != nil {
		return nil, err
	}
	out := make([]profileReport, 0, len(profiles))
	for _, profile := range profiles {
		out = append(out, profile.summary)
	}
	return out, nil
}

func buildToolProfiles(workspace string, includeTaskOnly bool) ([]toolProfile, error) {
	toolset, err := tools.NewToolset(workspace)
	if err != nil {
		return nil, err
	}
	baseTools := toolset.Tools()
	baseRegistry, err := core.NewToolRegistryChecked(baseTools)
	if err != nil {
		return nil, err
	}
	runner := tasks.NewRunner(tasks.RunnerConfig{
		ParentTools:   baseRegistry,
		WorkspaceRoot: workspace,
	})
	taskTools := tasks.NewTools(runner)
	defs := []struct {
		name  string
		tools []core.Tool
	}{
		{name: "base", tools: baseTools},
		{name: "base+task_tools", tools: append(append([]core.Tool{}, baseTools...), taskTools...)},
	}
	if includeTaskOnly {
		defs = append(defs, struct {
			name  string
			tools []core.Tool
		}{name: "task_tools_only", tools: taskTools})
	}
	out := make([]toolProfile, 0, len(defs))
	baseBytes := 0
	for i, profile := range defs {
		report := summarizeProfile(profile.name, profile.tools)
		if i == 0 {
			baseBytes = report.ToolsBytes
		} else {
			report.DeltaFromBaseBytes = report.ToolsBytes - baseBytes
		}
		out = append(out, toolProfile{
			name:    profile.name,
			tools:   profile.tools,
			summary: report,
		})
	}
	return out, nil
}

func runLiveSmoke(args cliArgs, workspace string) ([]liveRunReport, error) {
	if err := setupLiveWorkspace(workspace); err != nil {
		return nil, err
	}
	profiles, err := buildToolProfiles(workspace, false)
	if err != nil {
		return nil, err
	}
	runs := make([]liveRunReport, 0, len(profiles)*args.repeats)
	for repeat := 1; repeat <= args.repeats; repeat++ {
		for _, profile := range profiles {
			ctx, cancel := context.WithTimeout(context.Background(), args.timeout)
			run := runLiveProfile(ctx, args, workspace, profile, repeat)
			cancel()
			runs = append(runs, run)
			status := "fail"
			if run.Pass {
				status = "pass"
			}
			fmt.Printf("[%s/r%d] %s prompt=%d cache=%s cost=$%.6f tools_bytes=%d spawn=%t\n",
				run.Profile, run.Repeat, status, run.PromptTokens, pct(run.CacheHitRatio), run.CostUSD, run.ToolsBytes, run.SpawnSubagentCalled)
		}
	}
	return runs, nil
}

func setupLiveWorkspace(workspace string) error {
	pkgDir := filepath.Join(workspace, "pkg")
	if err := os.MkdirAll(pkgDir, 0o755); err != nil {
		return err
	}
	src := `package pkg

func Greeting() string {
	return "hello tool shape"
}
`
	return os.WriteFile(filepath.Join(pkgDir, "banner.go"), []byte(src), 0o644)
}

func runLiveProfile(ctx context.Context, args cliArgs, workspace string, profile toolProfile, repeat int) liveRunReport {
	dataDir := filepath.Join(args.outDir, "live", sanitizeName(profile.name), fmt.Sprintf("r%d", repeat))
	usagePath := filepath.Join(dataDir, "usage")
	report := liveRunReport{
		Profile:     profile.name,
		Repeat:      repeat,
		ToolsBytes:  profile.summary.ToolsBytes,
		ToolsHash:   profile.summary.ToolsHash,
		FinalOutput: "",
	}
	if err := os.MkdirAll(dataDir, 0o755); err != nil {
		report.Error = err.Error()
		return report
	}
	registry, err := core.NewToolRegistryChecked(profile.tools)
	if err != nil {
		report.Error = err.Error()
		return report
	}
	provider, err := deepseek.New(
		deepseek.WithModel(args.model),
		deepseek.WithReasoningEffort(args.effort),
		deepseek.WithThinking(true),
	)
	if err != nil {
		report.Error = err.Error()
		return report
	}
	messageStore, err := store.NewJSONLStore(filepath.Join(dataDir, "sessions"))
	if err != nil {
		report.Error = err.Error()
		return report
	}
	ag := agent.NewAgentWithRegistry(
		provider,
		messageStore,
		registry,
		agent.WithSessionsDir(filepath.Join(dataDir, "sessions")),
		agent.WithUsageLogPath(usagePath),
		agent.WithAutoCompact(false, 0, defaults.DeepSeekV4ContextWindow),
		agent.WithProjectMemory(false, 0, nil, workspace),
		agent.WithToolPolicy(policy.RulePolicy{Default: policy.PermissionAllow, Rules: policy.DefaultRules(), WorkspaceRoot: workspace}),
	)
	prompt := "Use local tools to read pkg/banner.go and answer only the exact string returned by Greeting. Do not use subagents; this is a direct file read."
	events, err := ag.RunStreamWithOptions(ctx, fmt.Sprintf("toolshape-%s-r%d", sanitizeName(profile.name), repeat), prompt, false)
	if err != nil {
		report.Error = err.Error()
		return report
	}
	for ev := range events {
		switch ev.Type {
		case agent.AgentEventTypeToolResult:
			if ev.Result != nil {
				report.ToolCalls++
				if ev.Result.Name == "spawn_subagent" {
					report.SpawnSubagentCalled = true
				}
			}
		case agent.AgentEventTypeDone:
			if ev.Message != nil {
				report.FinalOutput = ev.Message.Text
			}
		case agent.AgentEventTypeError:
			if ev.Err != nil {
				report.Error = ev.Err.Error()
			}
		}
	}
	if totals, err := readLiveUsageTotals(usagePath); err == nil {
		report.PromptTokens = totals.PromptTokens
		report.CompletionTokens = totals.CompletionTokens
		report.CacheHitTokens = totals.CacheHitTokens
		report.CacheMissTokens = totals.CacheMissTokens
		report.CacheHitRatio = cacheHitRatio(totals.CacheHitTokens, totals.CacheMissTokens)
		report.CostUSD = totals.CostUSD
		if totals.ToolsBytes > 0 {
			report.ToolsBytes = totals.ToolsBytes
		}
		report.SystemBytes = totals.SystemBytes
		report.RuntimeBytes = totals.RuntimeBytes
		if totals.ToolsHash != "" {
			report.ToolsHash = totals.ToolsHash
		}
		report.RequestHash = totals.RequestHash
	} else if report.Error == "" {
		report.Error = err.Error()
	}
	report.Pass = report.Error == "" && !report.SpawnSubagentCalled && strings.Contains(report.FinalOutput, "hello tool shape")
	return report
}

type liveUsageTotals struct {
	PromptTokens     int
	CompletionTokens int
	CacheHitTokens   int
	CacheMissTokens  int
	CostUSD          float64
	ToolsBytes       int
	SystemBytes      int
	RuntimeBytes     int
	ToolsHash        string
	RequestHash      string
}

func readLiveUsageTotals(dir string) (liveUsageTotals, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return liveUsageTotals{}, err
	}
	var totals liveUsageTotals
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".jsonl") {
			continue
		}
		f, err := os.Open(filepath.Join(dir, entry.Name()))
		if err != nil {
			continue
		}
		scanner := bufio.NewScanner(f)
		for scanner.Scan() {
			line := strings.TrimSpace(scanner.Text())
			if line == "" {
				continue
			}
			var rec telemetry.UsageRecord
			if err := json.Unmarshal([]byte(line), &rec); err != nil {
				f.Close()
				return liveUsageTotals{}, err
			}
			totals.PromptTokens += rec.PromptTokens
			totals.CompletionTokens += rec.CompletionTokens
			totals.CacheHitTokens += rec.PromptCacheHit
			totals.CacheMissTokens += rec.PromptCacheMiss
			totals.CostUSD += rec.CostUSD
			if rec.CacheShape != nil {
				totals.ToolsBytes = rec.CacheShape.ToolsBytes
				totals.SystemBytes = rec.CacheShape.SystemBytes
				totals.RuntimeBytes = rec.CacheShape.RuntimeBytes
				totals.ToolsHash = rec.CacheShape.ToolsHash
				totals.RequestHash = rec.CacheShape.RequestHash
			}
		}
		f.Close()
	}
	return totals, nil
}

func summarizeProfile(name string, tools []core.Tool) profileReport {
	items := make([]toolReport, 0, len(tools))
	payloads := make([]map[string]any, 0, len(tools))
	for _, tool := range tools {
		payload := core.ProviderToolPayload(tool)
		if payload == nil {
			continue
		}
		payloads = append(payloads, payload)
		items = append(items, summarizeTool(payload))
	}
	sort.SliceStable(items, func(i, j int) bool {
		if items[i].Bytes != items[j].Bytes {
			return items[i].Bytes > items[j].Bytes
		}
		return items[i].Name < items[j].Name
	})
	totalBytes := stableJSONBytes(payloads)
	spawnBytes := 0
	for _, item := range items {
		if item.Name == "spawn_subagent" {
			spawnBytes = item.Bytes
			break
		}
	}
	top := append([]toolReport(nil), items...)
	if len(top) > 12 {
		top = top[:12]
	}
	share := 0.0
	if totalBytes > 0 {
		share = float64(spawnBytes) / float64(totalBytes)
	}
	return profileReport{
		Name:               name,
		ToolCount:          len(items),
		ToolsBytes:         totalBytes,
		ToolsHash:          stableHash(payloads),
		SpawnSubagentBytes: spawnBytes,
		SpawnSubagentShare: share,
		TopTools:           top,
		Tools:              items,
	}
}

func summarizeTool(payload map[string]any) toolReport {
	fn, _ := payload["function"].(map[string]any)
	name, _ := fn["name"].(string)
	desc, _ := fn["description"].(string)
	params, _ := fn["parameters"].(map[string]any)
	return toolReport{
		Name:        name,
		Bytes:       stableJSONBytes(payload),
		Hash:        stableHash(payload),
		Description: len([]byte(desc)),
		Parameters:  stableJSONBytes(params),
	}
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

func renderMarkdown(report benchReport) string {
	var b strings.Builder
	fmt.Fprintf(&b, "# Whale tool-shape schema cost benchmark\n\n")
	fmt.Fprintf(&b, "**Date:** %s\n", report.Meta.Date)
	fmt.Fprintf(&b, "**Whale version:** `%s`\n", report.Meta.WhaleVersion)
	if len(report.LiveRuns) > 0 {
		fmt.Fprintf(&b, "**Source:** offline provider tool schema payloads plus live DeepSeek smoke\n")
		fmt.Fprintf(&b, "**Live model:** `%s`, effort `%s`, repeats %d\n\n", report.Meta.LiveModel, report.Meta.LiveEffort, report.Meta.LiveRepeats)
	} else {
		fmt.Fprintf(&b, "**Source:** offline provider tool schema payloads, no model calls\n\n")
	}
	fmt.Fprintf(&b, "## Summary\n\n")
	fmt.Fprintf(&b, "| profile | tools | tools bytes | delta vs base | spawn_subagent bytes | spawn share | tools hash |\n")
	fmt.Fprintf(&b, "|---|---:|---:|---:|---:|---:|---|\n")
	for _, profile := range report.Profiles {
		fmt.Fprintf(&b, "| %s | %d | %d | %+d | %d | %s | `%s` |\n",
			profile.Name, profile.ToolCount, profile.ToolsBytes, profile.DeltaFromBaseBytes, profile.SpawnSubagentBytes, pct(profile.SpawnSubagentShare), shortHash(profile.ToolsHash))
	}
	for _, profile := range report.Profiles {
		fmt.Fprintf(&b, "\n## Top Tools: %s\n\n", profile.Name)
		fmt.Fprintf(&b, "| rank | tool | bytes | description | parameters | hash |\n")
		fmt.Fprintf(&b, "|---:|---|---:|---:|---:|---|\n")
		for i, tool := range profile.TopTools {
			fmt.Fprintf(&b, "| %d | %s | %d | %d | %d | `%s` |\n", i+1, tool.Name, tool.Bytes, tool.Description, tool.Parameters, shortHash(tool.Hash))
		}
	}
	if len(report.LiveRuns) > 0 {
		fmt.Fprintf(&b, "\n## Live Smoke\n\n")
		fmt.Fprintf(&b, "| profile | repeat | pass | tools | prompt | completion | cache | cost | tools bytes | system bytes | runtime bytes | spawn called |\n")
		fmt.Fprintf(&b, "|---|---:|:---:|---:|---:|---:|---:|---:|---:|---:|---:|:---:|\n")
		for _, run := range report.LiveRuns {
			pass := "no"
			if run.Pass {
				pass = "yes"
			}
			spawn := "no"
			if run.SpawnSubagentCalled {
				spawn = "yes"
			}
			fmt.Fprintf(&b, "| %s | %d | %s | %d | %d | %d | %s | $%.6f | %d | %d | %d | %s |\n",
				run.Profile, run.Repeat, pass, run.ToolCalls, run.PromptTokens, run.CompletionTokens, pct(run.CacheHitRatio), run.CostUSD, run.ToolsBytes, run.SystemBytes, run.RuntimeBytes, spawn)
		}
	}
	fmt.Fprintf(&b, "\n## Reproduce\n\n")
	fmt.Fprintf(&b, "```bash\nscripts/bench/tool_shape.sh\nscripts/bench/tool_shape.sh --live --repeats 1\n```\n")
	return b.String()
}

func stableJSONBytes(v any) int {
	b, err := json.Marshal(v)
	if err != nil {
		return 0
	}
	return len(b)
}

func stableHash(v any) string {
	b, err := json.Marshal(v)
	if err != nil {
		b = []byte("null")
	}
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:])
}

func shortHash(hash string) string {
	if len(hash) <= 16 {
		return hash
	}
	return hash[:16]
}

func pct(v float64) string {
	return fmt.Sprintf("%.1f%%", v*100)
}

func cacheHitRatio(hit, miss int) float64 {
	total := hit + miss
	if total <= 0 {
		return 0
	}
	return float64(hit) / float64(total)
}

func sanitizeName(name string) string {
	name = strings.ToLower(strings.TrimSpace(name))
	var b strings.Builder
	for _, r := range name {
		switch {
		case r >= 'a' && r <= 'z':
			b.WriteRune(r)
		case r >= '0' && r <= '9':
			b.WriteRune(r)
		default:
			b.WriteByte('-')
		}
	}
	out := strings.Trim(b.String(), "-")
	if out == "" {
		return "profile"
	}
	return out
}
