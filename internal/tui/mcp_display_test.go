package tui

import "testing"

func TestParseMCPDisplayInfo(t *testing.T) {
	info, ok := parseMCPDisplayInfo("mcp__fs__list_allowed_directories", `mcp__fs__list_allowed_directories: {}`)
	if !ok {
		t.Fatal("expected MCP tool name to parse")
	}
	if info.Server != "fs" || info.Tool != "list_allowed_directories" || info.Kind != mcpKindList {
		t.Fatalf("unexpected MCP info: %+v", info)
	}
}

func TestParseMCPDisplayInfoRejectsNonMCPTool(t *testing.T) {
	if _, ok := parseMCPDisplayInfo("read_file", `read_file: {"file_path":"README.md"}`); ok {
		t.Fatal("non-MCP tool should not parse as MCP")
	}
}

func TestMCPArgsPreserveUTF8AndClassifySafely(t *testing.T) {
	info, ok := parseMCPDisplayInfo("mcp__fs__read_text_file", `mcp__fs__read_text_file: {"path":"/tmp/中文目录/README.md"}`)
	if !ok {
		t.Fatal("expected MCP tool name to parse")
	}
	if got := info.primaryArgDetail(); got != "/tmp/中文目录/README.md" {
		t.Fatalf("unexpected arg detail: %q", got)
	}
	if classifyMCPTool("write_file") != mcpKindWrite {
		t.Fatal("write_file should not be classified as read-only MCP")
	}
}

func TestSummarizeMCPOutputFlattensSmallJSON(t *testing.T) {
	got := summarizeMCPOutput(`{"path":"/tmp/a","ok":true}`)
	if got != "ok: true\npath: /tmp/a" {
		t.Fatalf("unexpected flattened JSON:\n%s", got)
	}
}
