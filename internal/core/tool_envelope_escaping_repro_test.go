package core

// Reproductions from session 019ead56-4099-741e-ae27-f27b40a3d6ec, a Windows
// user building a BepInEx mod in C#. 148 of the session's tool results carried
// literal & / < / > sequences because MarshalToolEnvelope uses
// json.Marshal, whose default HTML escaping rewrites & < > inside payload
// text. The model therefore read file content as
//
//	if (elem != null && elem.ValueWithoutLink > 0)
//
// copied it verbatim into an edit search (m-333, m-337), and got
// search_not_found twice.
//
// These tests pin the desired behavior — payload text (file content, command
// output, commands) must reach the model byte-for-byte — and FAIL until the
// model-facing path stops HTML-escaping payload content. They deliberately
// conflict with TestToolEnvelopeKeepsDefaultEscapingForPromptTags, which
// asserts the current escaping to keep <proposed_plan> out of result text;
// the TUI already strips those tags from visible content (commit ebc13d6),
// so the fix must retire or narrow that spec, not silently satisfy both.

import (
	"strings"
	"testing"
)

// Verbatim lines from the file the session's model read and then failed to
// edit (F:\ai项目\whale项目\elin\better_slime\Patch_GeneIntercept.cs).
const sessionCSharpPayload = `        if (Act.CC.HasCondition<ConAnorexia>())
        {
            Element elem = c.elements.GetElement(6608);
            if (elem != null && elem.ValueWithoutLink > 0)
        }`

func assertNoHTMLEscapes(t *testing.T, content string) {
	t.Helper()
	for _, esc := range []string{`\u0026`, `\u003c`, `\u003e`} {
		if strings.Contains(content, esc) {
			t.Errorf("model-facing content contains literal %s; payload text must reach the model verbatim:\n%s", esc, content)
		}
	}
}

func TestToolEnvelopeFileContentReachesModelVerbatim(t *testing.T) {
	content, err := MarshalToolEnvelope(NewToolSuccessEnvelope(map[string]any{
		"payload": map[string]any{"file_content": sessionCSharpPayload},
	}))
	if err != nil {
		t.Fatalf("marshal envelope: %v", err)
	}
	assertNoHTMLEscapes(t, content)
	if !strings.Contains(content, "elem != null && elem.ValueWithoutLink > 0") {
		t.Errorf("expected C# operators verbatim in model-facing content, got:\n%s", content)
	}
	if !strings.Contains(content, "HasCondition<ConAnorexia>()") {
		t.Errorf("expected generic type syntax verbatim in model-facing content, got:\n%s", content)
	}
}

func TestToolEnvelopeShellRedirectionReachesModelVerbatim(t *testing.T) {
	// Real command from the session (m-15); every cmd.exe command in that
	// session used 2>&1 and &, so every shell result was corrupted.
	command := `where ilspy 2>&1 & where ilspycmd 2>&1 & dotnet tool list -g 2>&1`
	content, err := MarshalToolEnvelope(ToolEnvelope{
		OK:      false,
		Success: false,
		Error:   "command failed",
		Code:    "exec_failed",
		Data: map[string]any{
			"payload": map[string]any{"command": command, "stdout": "INFO: Could not find files for the given pattern(s)."},
		},
	})
	if err != nil {
		t.Fatalf("marshal envelope: %v", err)
	}
	assertNoHTMLEscapes(t, content)
	if !strings.Contains(content, `2>&1`) {
		t.Errorf("expected shell redirection verbatim in model-facing content, got:\n%s", content)
	}
}
