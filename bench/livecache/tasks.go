package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

var tasks = []taskSpec{
	{
		ID:          "read_search_followup",
		Description: "Find a pricing rule, then verify test coverage in a follow-up turn.",
		Prompts: []string{
			"Inspect this repository with local tools. What discount rate does the pricing code use for gold customers? Answer with the exact percent and the file path.",
			"Now find whether there is a test covering the gold customer path. Answer with the file path and test name.",
		},
		Setup: setupReadSearch,
		Check: func(root string, transcript []transcriptRecord, finalOutput string) error {
			all := strings.ToLower(transcriptText(transcript) + "\n" + finalOutput)
			if !strings.Contains(all, "pricing_test.go") || !strings.Contains(all, "gold") {
				return fmt.Errorf("final transcript did not identify gold test coverage")
			}
			return nil
		},
	},
	{
		ID:          "edit_apply_followup",
		Description: "Edit a small Go file, then verify the applied change.",
		Prompts: []string{
			"Change the greeting in pkg/banner.go from 'hello whale' to 'hello cache'. Use local tools and keep the change minimal.",
			"Verify the greeting change by reading the file and summarize the current value.",
		},
		Setup: setupEditApply,
		Check: func(root string, transcript []transcriptRecord, finalOutput string) error {
			b, err := os.ReadFile(filepath.Join(root, "pkg", "banner.go"))
			if err != nil {
				return err
			}
			if !strings.Contains(string(b), "hello cache") {
				return fmt.Errorf("pkg/banner.go was not updated to hello cache")
			}
			return nil
		},
	},
	{
		ID:          "shell_fix_followup",
		Description: "Run a failing shell check, fix config, then run it again.",
		Prompts: []string{
			"Run ./scripts/check.sh, fix the repository so the check passes, and summarize the change.",
			"Run ./scripts/check.sh one more time and report whether it passes.",
		},
		Setup: setupShellFix,
		Check: func(root string, transcript []transcriptRecord, finalOutput string) error {
			b, err := os.ReadFile(filepath.Join(root, "config.env"))
			if err != nil {
				return err
			}
			if !strings.Contains(string(b), "FEATURE_ENABLED=true") {
				return fmt.Errorf("config.env was not fixed")
			}
			if !strings.Contains(strings.ToLower(transcriptText(transcript)), "check passed") {
				return fmt.Errorf("transcript did not include a passing check")
			}
			return nil
		},
	},
}

func setupReadSearch(root string) error {
	files := map[string]string{
		"go.mod": "module example.com/livecache\n\ngo 1.22\n",
		"pkg/pricing/pricing.go": `package pricing

func DiscountRate(customerTier string) float64 {
	switch customerTier {
	case "gold":
		return 0.15
	case "silver":
		return 0.08
	default:
		return 0
	}
}
`,
		"pkg/pricing/pricing_test.go": `package pricing

import "testing"

func TestDiscountRateGoldCustomer(t *testing.T) {
	if got := DiscountRate("gold"); got != 0.15 {
		t.Fatalf("gold discount = %v", got)
	}
}
`,
		"README.md": "# live cache fixture\n",
	}
	return writeFixtureFiles(root, files)
}

func setupEditApply(root string) error {
	files := map[string]string{
		"go.mod": "module example.com/livecache\n\ngo 1.22\n",
		"pkg/banner.go": `package pkg

func Greeting() string {
	return "hello whale"
}
`,
		"pkg/banner_test.go": `package pkg

import "testing"

func TestGreeting(t *testing.T) {
	if Greeting() == "" {
		t.Fatal("empty greeting")
	}
}
`,
	}
	return writeFixtureFiles(root, files)
}

func setupShellFix(root string) error {
	files := map[string]string{
		"config.env": "FEATURE_ENABLED=false\n",
		"scripts/check.sh": `#!/usr/bin/env bash
set -euo pipefail
if grep -q '^FEATURE_ENABLED=true$' config.env; then
  echo "check passed"
  exit 0
fi
echo "check failed: FEATURE_ENABLED must be true"
exit 1
`,
	}
	if err := writeFixtureFiles(root, files); err != nil {
		return err
	}
	return os.Chmod(filepath.Join(root, "scripts", "check.sh"), 0o755)
}

func writeFixtureFiles(root string, files map[string]string) error {
	for rel, content := range files {
		path := filepath.Join(root, rel)
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			return err
		}
		if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
			return err
		}
	}
	return nil
}

func transcriptText(records []transcriptRecord) string {
	var b strings.Builder
	for _, r := range records {
		if r.Content != "" {
			b.WriteString(r.Content)
			b.WriteByte('\n')
		}
		if r.Error != "" {
			b.WriteString(r.Error)
			b.WriteByte('\n')
		}
	}
	return b.String()
}
