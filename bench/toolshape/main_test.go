package main

import (
	"strings"
	"testing"
)

func TestBuildProfilesIncludesSpawnSubagentCost(t *testing.T) {
	profiles, err := buildProfiles(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if len(profiles) != 3 {
		t.Fatalf("profiles len = %d, want 3", len(profiles))
	}
	var taskProfile profileReport
	for _, profile := range profiles {
		if profile.Name == "base+task_tools" {
			taskProfile = profile
			break
		}
	}
	if taskProfile.ToolCount == 0 {
		t.Fatal("missing base+task_tools profile")
	}
	if taskProfile.SpawnSubagentBytes <= 0 {
		t.Fatalf("spawn_subagent bytes = %d, want > 0", taskProfile.SpawnSubagentBytes)
	}
	if taskProfile.ToolsBytes <= taskProfile.SpawnSubagentBytes {
		t.Fatalf("tools bytes = %d, spawn bytes = %d", taskProfile.ToolsBytes, taskProfile.SpawnSubagentBytes)
	}
	if len(taskProfile.TopTools) == 0 {
		t.Fatal("missing top tools")
	}
}

func TestRenderMarkdownShowsProfilesAndTopTools(t *testing.T) {
	report := benchReport{
		Meta: benchMeta{Date: "2026-06-04T00:00:00Z", WhaleVersion: "test", LiveModel: "deepseek-v4-flash", LiveEffort: "high", LiveRepeats: 1},
		Profiles: []profileReport{{
			Name:               "base+task_tools",
			ToolCount:          2,
			ToolsBytes:         100,
			DeltaFromBaseBytes: 40,
			SpawnSubagentBytes: 35,
			SpawnSubagentShare: 0.35,
			ToolsHash:          "1234567890abcdef1234",
			TopTools: []toolReport{{
				Name:        "spawn_subagent",
				Bytes:       35,
				Hash:        "abcdef1234567890",
				Description: 12,
				Parameters:  20,
			}},
		}},
		LiveRuns: []liveRunReport{{
			Profile:             "base+task_tools",
			Repeat:              1,
			Pass:                true,
			ToolCalls:           1,
			PromptTokens:        1000,
			CompletionTokens:    12,
			CacheHitRatio:       0.75,
			CostUSD:             0.000123,
			ToolsBytes:          100,
			SystemBytes:         200,
			RuntimeBytes:        50,
			SpawnSubagentCalled: false,
		}},
	}
	md := renderMarkdown(report)
	for _, want := range []string{
		"tool-shape schema cost",
		"| base+task_tools | 2 | 100 | +40 | 35 | 35.0% | `1234567890abcdef` |",
		"| 1 | spawn_subagent | 35 | 12 | 20 | `abcdef1234567890` |",
		"**Live model:** `deepseek-v4-flash`, effort `high`, repeats 1",
		"## Live Smoke",
		"| base+task_tools | 1 | yes | 1 | 1000 | 12 | 75.0% | $0.000123 | 100 | 200 | 50 | no |",
		"scripts/bench/tool_shape.sh",
	} {
		if !strings.Contains(md, want) {
			t.Fatalf("report missing %q:\n%s", want, md)
		}
	}
}

func TestCacheHitRatio(t *testing.T) {
	if got := cacheHitRatio(3, 1); got != 0.75 {
		t.Fatalf("cacheHitRatio = %v, want 0.75", got)
	}
	if got := cacheHitRatio(0, 0); got != 0 {
		t.Fatalf("cacheHitRatio empty = %v, want 0", got)
	}
}
