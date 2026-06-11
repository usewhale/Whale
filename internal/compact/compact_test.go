package compact

import (
	"strings"
	"testing"

	"github.com/usewhale/whale/internal/core"
)

func TestEstimateTokens(t *testing.T) {
	if got := EstimateTokens("abcd"); got != 1 {
		t.Fatalf("expected 1 token, got %d", got)
	}
	if got := EstimateTokens("你好"); got != 2 {
		t.Fatalf("expected 2 tokens, got %d", got)
	}
	if got := EstimateTokens("   "); got != 0 {
		t.Fatalf("expected blank text to estimate to 0, got %d", got)
	}
}

func TestEstimateMessagesTokensIncludesToolPayloads(t *testing.T) {
	msgs := []core.Message{{
		Role: core.RoleAssistant,
		Text: strings.Repeat("a", 8),
		ToolCalls: []core.ToolCall{{
			Name:  "write",
			Input: strings.Repeat("b", 8),
		}},
	}, {
		Role: core.RoleTool,
		ToolResults: []core.ToolResult{{
			Name:      "write",
			ModelText: strings.Repeat("c", 8),
		}},
	}}
	if got := EstimateMessagesTokens(msgs); got == 0 {
		t.Fatal("expected non-zero estimate")
	}
}

func TestToolResultReplayContentCompactsLargeOutput(t *testing.T) {
	raw := strings.Repeat("a", 4000) + strings.Repeat("middle", 2000) + strings.Repeat("z", 4000)
	got := ToolResultReplayContent(raw)
	if got == raw {
		t.Fatal("expected large tool result to be compacted")
	}
	if !strings.Contains(got, "[tool result compacted for model replay]") {
		t.Fatalf("missing compaction marker: %q", got[:min(len(got), 80)])
	}
	if !strings.Contains(got, strings.Repeat("a", 100)) || !strings.Contains(got, strings.Repeat("z", 100)) {
		t.Fatal("expected compacted replay to retain head and tail")
	}
}

func TestEstimateMessagesTokensUsesMessagePartsPlainText(t *testing.T) {
	msg := core.UserMessageFromParts("s1", []core.MessagePart{
		{Type: core.MessagePartText, Text: strings.Repeat("a", 8)},
		{Type: core.MessagePartAttachment, Attachment: &core.AttachmentRef{
			Kind:        core.AttachmentKindPDF,
			DisplayName: "paper.pdf",
		}},
	}, false)

	got := EstimateMessagesTokens([]core.Message{msg})
	if got == 0 {
		t.Fatal("expected non-zero estimate")
	}
	if got > EstimateTokens(msg.Text)+1 {
		t.Fatalf("estimate = %d unexpectedly exceeds plain text mirror", got)
	}
}
