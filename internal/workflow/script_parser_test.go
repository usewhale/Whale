package workflow

import (
	"strings"
	"testing"
)

func TestParseWorkflowScriptLineCommentAtEOFReturnsError(t *testing.T) {
	defer func() {
		if r := recover(); r != nil {
			t.Fatalf("parseWorkflowScript panicked for EOF line comment: %v", r)
		}
	}()

	_, err := parseWorkflowScript("// missing meta without trailing newline")
	if err == nil || !strings.Contains(err.Error(), "export const meta") {
		t.Fatalf("expected meta validation error, got %v", err)
	}
}

func TestParseWorkflowScriptBodyLineCommentAtEOF(t *testing.T) {
	parsed, err := parseWorkflowScript("export const meta = { name: 'comment-eof', description: 'comment eof' }\n// trailing comment")
	if err != nil {
		t.Fatalf("parseWorkflowScript: %v", err)
	}
	if !strings.Contains(parsed.Executable, "comment-eof") {
		t.Fatalf("unexpected executable: %s", parsed.Executable)
	}
}
