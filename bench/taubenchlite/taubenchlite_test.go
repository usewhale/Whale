package main

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/usewhale/whale/internal/core"
	"github.com/usewhale/whale/internal/llm"
)

func TestParseModes(t *testing.T) {
	tests := []struct {
		name string
		in   string
		want []string
	}{
		{name: "default", in: "", want: []string{"baseline", "whale"}},
		{name: "both", in: "both", want: []string{"baseline", "whale"}},
		{name: "baseline", in: "baseline", want: []string{"baseline"}},
		{name: "whale", in: "whale", want: []string{"whale"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := parseModes(tt.in)
			if err != nil {
				t.Fatalf("parseModes(%q) error: %v", tt.in, err)
			}
			if strings.Join(got, ",") != strings.Join(tt.want, ",") {
				t.Fatalf("parseModes(%q) = %v, want %v", tt.in, got, tt.want)
			}
		})
	}
	if _, err := parseModes("reasonix"); err == nil {
		t.Fatal("parseModes(reasonix) succeeded, want error")
	}
}

func TestCloneDBIsolatesRuns(t *testing.T) {
	base := retailSeed()
	a := cloneDB(base)
	b := cloneDB(base)
	row := a.Orders["o_1002"]
	row.Address = "changed"
	a.Orders["o_1002"] = row
	if b.Orders["o_1002"].Address == "changed" {
		t.Fatal("cloneDB leaked mutation across copies")
	}
	if base.Orders["o_1002"].Address == "changed" {
		t.Fatal("cloneDB mutated source")
	}
}

func TestRetailToolsHappyAndErrorPaths(t *testing.T) {
	db := retailSeed()
	tools := map[string]core.Tool{}
	for _, tool := range buildRetailTools(&db) {
		tools[tool.Name()] = tool
	}
	res := runTool(t, tools["lookup_order"], `{"orderId":"o_1002"}`)
	if res.IsError() || !strings.Contains(res.ModelText, `"status":"processing"`) {
		t.Fatalf("lookup_order unexpected result: %+v", res)
	}
	res = runTool(t, tools["update_address"], `{"orderId":"o_1002","address":"5 Birch Rd, NYC, NY 10001"}`)
	if res.IsError() || db.Orders["o_1002"].Address != "5 Birch Rd, NYC, NY 10001" {
		t.Fatalf("update_address did not mutate processing order: %+v db=%+v", res, db.Orders["o_1002"])
	}
	res = runTool(t, tools["update_address"], `{"orderId":"o_1001","address":"99 New St, SF, CA"}`)
	if !res.IsError() || db.Orders["o_1001"].Address != "1 Elm St, SF, CA 94110" {
		t.Fatalf("update_address changed shipped order: %+v db=%+v", res, db.Orders["o_1001"])
	}
	res = runTool(t, tools["refund_order"], `{"orderId":"o_1003","reason":"arrived broken"}`)
	if res.IsError() || db.Orders["o_1003"].Status != "refunded" || db.Refunds["o_1003"].Amount != 55.0 {
		t.Fatalf("refund_order unexpected result: %+v db=%+v", res, db)
	}
}

func TestTaskChecks(t *testing.T) {
	for _, task := range tasks {
		db := cloneDB(task.InitialDB)
		switch task.ID {
		case "t01_address_happy":
			row := db.Orders["o_1002"]
			row.Address = "5 Birch Rd, NYC, NY 10001"
			db.Orders["o_1002"] = row
		case "t03_cancel_processing", "t08_address_then_cancel":
			row := db.Orders["o_1004"]
			row.Status = "cancelled"
			db.Orders["o_1004"] = row
		case "t04_refund_delivered":
			row := db.Orders["o_1003"]
			row.Status = "refunded"
			db.Orders["o_1003"] = row
			db.Refunds["o_1003"] = refundRow{OrderID: "o_1003", Reason: "arrived broken", Amount: 55.0}
		}
		if !task.Check(runCheckContext{DB: db}) {
			t.Fatalf("%s expected check to pass", task.ID)
		}
	}
}

func TestUserSimulatorStopParsing(t *testing.T) {
	sim := newUserSimulator(stopProvider{}, userPersona{Style: "brief", Goal: "stop", Knowns: map[string]string{}}, "test")
	msg, stop, err := sim.next(context.Background(), []turn{{Role: "agent", Content: "Done"}})
	if err != nil {
		t.Fatal(err)
	}
	if !stop || msg != "" {
		t.Fatalf("sim.next = msg=%q stop=%v, want stop", msg, stop)
	}
}

func TestReadUsageTotalsCountsDistinctPrefixFingerprints(t *testing.T) {
	path := filepath.Join(t.TempDir(), "usage.jsonl")
	data := strings.Join([]string{
		`{"prompt_tokens":10,"completion_tokens":2,"prompt_cache_hit_tokens":8,"prompt_cache_miss_tokens":2,"cost_usd":0.1,"cache_savings_usd":0.01,"prefix_fingerprint":"fp-a","cache_shape":{"prefix_hash":"shape-a","runtime_hash":"rt-a","tools_hash":"tools-a","request_hash":"req-a"}}`,
		`{"prompt_tokens":20,"completion_tokens":3,"prompt_cache_hit_tokens":10,"prompt_cache_miss_tokens":10,"cost_usd":0.2,"cache_savings_usd":0.02,"prefix_fingerprint":"fp-a","cache_shape":{"prefix_hash":"shape-b","runtime_hash":"rt-b","tools_hash":"tools-a","request_hash":"req-b"}}`,
		`{"prompt_tokens":30,"completion_tokens":4,"prompt_cache_hit_tokens":15,"prompt_cache_miss_tokens":15,"cost_usd":0.3,"cache_savings_usd":0.03,"prefix_fingerprint":"fp-b","cache_shape":{"prefix_hash":"shape-b","runtime_hash":"rt-b","tools_hash":"tools-b","request_hash":"req-c"}}`,
	}, "\n") + "\n"
	if err := os.WriteFile(path, []byte(data), 0o644); err != nil {
		t.Fatal(err)
	}
	totals, err := readUsageTotals(path)
	if err != nil {
		t.Fatal(err)
	}
	if got := len(totals.PrefixFingerprints); got != 2 {
		t.Fatalf("prefix fingerprints = %d, want 2", got)
	}
	if got := len(totals.ShapePrefixHashes); got != 2 {
		t.Fatalf("shape prefix hashes = %d, want 2", got)
	}
	if got := len(totals.ShapeRuntimeHashes); got != 2 {
		t.Fatalf("runtime hashes = %d, want 2", got)
	}
	if got := len(totals.ShapeToolsHashes); got != 2 {
		t.Fatalf("tool-shape hashes = %d, want 2", got)
	}
	if got := len(totals.ShapeRequestHashes); got != 3 {
		t.Fatalf("request hashes = %d, want 3", got)
	}
	if got := totals.WarmCacheHitRatio(); got != 0.5 {
		t.Fatalf("warm cache ratio = %v, want 0.5", got)
	}
	if totals.PromptTokens != 60 || totals.CompletionTokens != 9 {
		t.Fatalf("unexpected token totals: %+v", totals)
	}
	if got := totals.CacheSavingsUSD; got != 0.06 {
		t.Fatalf("cache savings = %v, want 0.06", got)
	}
}

func TestShuffleToolsIsDeterministicAndSeeded(t *testing.T) {
	db := retailSeed()
	tools := buildRetailTools(&db)
	a := toolNames(shuffleTools(tools, 1))
	b := toolNames(shuffleTools(tools, 1))
	c := toolNames(shuffleTools(tools, 2))
	if strings.Join(a, ",") != strings.Join(b, ",") {
		t.Fatalf("shuffleTools with same seed differed: %v vs %v", a, b)
	}
	if strings.Join(a, ",") == strings.Join(c, ",") {
		t.Fatalf("shuffleTools with different seeds matched: %v", a)
	}
	if strings.Join(toolNames(tools), ",") == strings.Join(a, ",") {
		t.Fatalf("shuffleTools did not change tool order: %v", a)
	}
}

func TestRenderMarkdownComparesBaselineAndWhale(t *testing.T) {
	report := benchReport{
		Meta: benchMeta{Date: "2026-06-03T00:00:00Z", Model: "deepseek-v4-flash", UserModel: "deepseek-chat", Effort: "high", Modes: []string{"baseline", "whale"}, TaskCount: 1, RepeatsPerTask: 1, WhaleVersion: "test", LiveDeepSeek: true},
		Results: []runResult{
			{Mode: "baseline", TaskID: "t01_address_happy", Repeat: 1, Pass: true, Turns: 2, ToolCalls: 2, PrefixFingerprints: 2, PrefixFingerprintValues: []string{"fp-a", "fp-b"}, ShapePrefixHashes: 2, ShapePrefixHashValues: []string{"shape-a", "shape-b"}, ShapeRuntimeHashes: 2, ShapeRuntimeHashValues: []string{"rt-a", "rt-b"}, ShapeToolsHashes: 2, ShapeToolsHashValues: []string{"tools-a", "tools-b"}, ShapeRequestHashes: 2, ShapeRequestHashValues: []string{"req-a", "req-b"}, CacheHitTokens: 20, CacheMissTokens: 80, CacheHitRatio: 0.2, WarmCacheHitTokens: 0, WarmCacheMissTokens: 50, WarmCacheHitRatio: 0, CostUSD: 0.2, CacheSavingsUSD: 0.01, UncachedCostUSD: 0.21},
			{Mode: "whale", TaskID: "t01_address_happy", Repeat: 1, Pass: true, Turns: 2, ToolCalls: 2, PrefixFingerprints: 1, PrefixFingerprintValues: []string{"fp-whale"}, ShapePrefixHashes: 1, ShapePrefixHashValues: []string{"shape-whale"}, ShapeRuntimeHashes: 1, ShapeRuntimeHashValues: []string{"rt-whale"}, ShapeToolsHashes: 1, ShapeToolsHashValues: []string{"tools-whale"}, ShapeRequestHashes: 1, ShapeRequestHashValues: []string{"req-whale"}, CacheHitTokens: 80, CacheMissTokens: 20, CacheHitRatio: 0.8, WarmCacheHitTokens: 90, WarmCacheMissTokens: 10, WarmCacheHitRatio: 0.9, CostUSD: 0.1, CacheSavingsUSD: 0.04, UncachedCostUSD: 0.14},
		},
	}
	md := renderMarkdown(report)
	for _, want := range []string{
		"tau-bench-lite retail",
		"| metric | baseline | whale | delta |",
		"| cache hit | 20.0% | 80.0% | **+60.0pp** |",
		"| token-weighted cache hit | 20.0% | 80.0% | +60.0pp |",
		"| warm cache hit | 0.0% | 90.0% | +90.0pp |",
		"| mean cost / task | $0.200000 | $0.100000 | x0.50 |",
		"| mean cache savings / task | $0.010000 | $0.040000 | +$0.030000 |",
		"| mean uncached cost / task | $0.210000 | $0.140000 | -$0.070000 |",
		"| mean immutable prefix fingerprints | 2.0 | 1.0 | -1.0 |",
		"| global immutable prefix fingerprints | 2 | 1 | -1 |",
		"| mean cache-shape prefix hashes | 2.0 | 1.0 | -1.0 |",
		"| global cache-shape prefix hashes | 2 | 1 | -1 |",
		"| mean runtime hashes | 2.0 | 1.0 | -1.0 |",
		"| global runtime hashes | 2 | 1 | -1 |",
		"| mean tool-shape hashes | 2.0 | 1.0 | -1.0 |",
		"| global tool-shape hashes | 2 | 1 | -1 |",
		"| mean request hashes | 2.0 | 1.0 | -1.0 |",
		"| task | mode | repeat | pass | turns | tools | imm fp | shape prefix | runtime | tool shape | request | truncated | cache | cost |",
		"same Whale agent and retail tools",
	} {
		if !strings.Contains(md, want) {
			t.Fatalf("report missing %q:\n%s", want, md)
		}
	}
}

func runTool(t *testing.T, tool core.Tool, input string) core.ToolResult {
	t.Helper()
	res, err := tool.Run(context.Background(), core.ToolCall{ID: "tc", Name: tool.Name(), Input: input})
	if err != nil {
		t.Fatal(err)
	}
	return res
}

func toolNames(tools []core.Tool) []string {
	out := make([]string, 0, len(tools))
	for _, tool := range tools {
		out = append(out, tool.Name())
	}
	return out
}

type stopProvider struct{}

func (stopProvider) StreamResponse(context.Context, []core.Message, []core.Tool) <-chan llm.ProviderEvent {
	ch := make(chan llm.ProviderEvent, 1)
	ch <- llm.ProviderEvent{Type: llm.EventComplete, Response: &llm.ProviderResponse{Content: "##STOP##"}}
	close(ch)
	return ch
}
