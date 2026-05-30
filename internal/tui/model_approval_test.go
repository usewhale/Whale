package tui

import (
	tea "github.com/charmbracelet/bubbletea"
	xansi "github.com/charmbracelet/x/ansi"
	"github.com/usewhale/whale/internal/app/service"
	tuirender "github.com/usewhale/whale/internal/tui/render"
	"strings"
	"testing"
	"time"
)

func TestApprovalNoticeTextUsesDecisionAndSummary(t *testing.T) {
	m := newModel(nil, "", "", "")
	m.approval.reason = "shell_run: go test ./..."
	if got := m.approvalNoticeText("allow"); got != "Approved to run go test ./... · this time" {
		t.Fatalf("unexpected allow notice: %q", got)
	}
	if got := m.approvalNoticeText("allow_session"); got != "Approved to run go test ./... · for this session" {
		t.Fatalf("unexpected session notice: %q", got)
	}
	if got := m.approvalNoticeText("deny"); got != "Denied request to run go test ./..." {
		t.Fatalf("unexpected deny notice: %q", got)
	}
	if got := m.approvalNoticeText("cancel"); got != "Canceled request to run go test ./..." {
		t.Fatalf("unexpected cancel notice: %q", got)
	}
}
func TestApprovalDecisionAppendsStructuredNotice(t *testing.T) {
	m, intents := newModelWithDispatchSpy()
	m.mode = modeApproval
	m.approval.toolCallID = "tool-1"
	m.approval.toolName = "shell_run"
	m.approval.reason = "shell_run: git status"

	next, _ := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("a")})
	m = next.(model)
	if len(*intents) != 1 || (*intents)[0].Kind != service.IntentAllowTool || (*intents)[0].ToolCallID != "tool-1" {
		t.Fatalf("unexpected approval intent: %+v", *intents)
	}
	if m.assembler == nil {
		t.Fatal("expected assembler with approval notice")
	}
	snap := m.assembler.Snapshot()
	if len(snap) == 0 {
		t.Fatal("expected approval notice message")
	}
	got := snap[len(snap)-1]
	if got.Kind != tuirender.KindNotice || got.Notice == nil {
		t.Fatalf("expected structured notice, got: %+v", got)
	}
	if got.Text != "Approved to run git status · this time" {
		t.Fatalf("unexpected notice text: %q", got.Text)
	}
	if got.Notice.Kind != "approval_allowed" || got.Notice.Command != "git status" || got.Notice.Scope != "this time" {
		t.Fatalf("unexpected notice metadata: %+v", got.Notice)
	}
}
func TestApprovalEscCancelsInsteadOfDenying(t *testing.T) {
	m, intents := newModelWithDispatchSpy()
	m.width = 80
	m.height = 24
	m.mode = modeApproval
	m.approval.toolCallID = "tool-1"
	m.approval.toolName = "shell_run"
	m.approval.reason = "shell_run: date"

	cmd := m.handleApprovalKey(tea.KeyMsg{Type: tea.KeyEsc})
	if cmd != nil {
		t.Fatal("expected esc approval handling to return no command")
	}
	if len(*intents) != 1 || (*intents)[0].Kind != service.IntentCancelToolApproval || (*intents)[0].ToolCallID != "tool-1" {
		t.Fatalf("expected esc to cancel approval, got %+v", *intents)
	}
	if m.mode != modeChat || m.status != "canceled" {
		t.Fatalf("expected approval cancel to return to chat canceled state, got mode=%v status=%q", m.mode, m.status)
	}
}
func TestApprovalEscRemovesPendingToolCallBeforeTurnDone(t *testing.T) {
	m, intents := newModelWithDispatchSpy()
	m.width = 100
	m.height = 24
	m.busy = true
	m.mode = modeApproval
	m.approval.toolCallID = "tool-1"
	m.approval.toolName = "shell_run"
	m.approval.reason = "shell_run: date"
	m.assembler.AddToolCall("tool-1", "shell_run", "Running date")
	m.markToolCallPending("tool-1")
	m.sawReasoningThisTurn = true

	_ = m.handleApprovalKey(tea.KeyMsg{Type: tea.KeyEsc})
	if len(*intents) != 1 || (*intents)[0].Kind != service.IntentCancelToolApproval {
		t.Fatalf("expected esc to cancel approval, got %+v", *intents)
	}
	if got := m.assembler.ToolCallText("tool-1"); got != "" {
		t.Fatalf("cancel should remove pending tool call before turn done, got %q", got)
	}
	if m.hasPendingToolCalls() {
		t.Fatalf("cancel should clear pending tool call state: %+v", m.pendingToolCalls)
	}
	if !m.sawTerminalToolOutcomeThisTurn {
		t.Fatal("cancel should mark the turn as terminal to suppress reasoning-only fallback")
	}

	next, _ := m.Update(svcMsg(service.Event{Kind: service.EventTurnDone}))
	m = next.(model)
	rendered := strings.Join(tuirender.ChatLines(m.transcript, 100), "\n")
	if strings.Contains(rendered, "Running date") {
		t.Fatalf("canceled approval committed pending running row:\n%s", rendered)
	}
	if strings.Contains(rendered, "Reasoning only") || strings.Contains(rendered, "did not produce a visible answer") {
		t.Fatalf("approval cancel should suppress reasoning-only fallback:\n%s", rendered)
	}
	if !strings.Contains(rendered, "Canceled request to run date") {
		t.Fatalf("expected cancel notice in transcript:\n%s", rendered)
	}
}
func TestApprovalDStillDenies(t *testing.T) {
	m, intents := newModelWithDispatchSpy()
	m.width = 80
	m.height = 24
	m.mode = modeApproval
	m.approval.toolCallID = "tool-1"
	m.approval.toolName = "shell_run"
	m.approval.reason = "shell_run: date"

	cmd := m.handleApprovalKey(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("d")})
	if cmd != nil {
		t.Fatal("expected deny approval handling to return no command")
	}
	if len(*intents) != 1 || (*intents)[0].Kind != service.IntentDenyTool || (*intents)[0].ToolCallID != "tool-1" {
		t.Fatalf("expected d to deny approval, got %+v", *intents)
	}
}
func TestCtrlCWhileBusyInterruptsBeforeApprovalMode(t *testing.T) {
	m, intents := newModelWithDispatchSpy()
	m.svc = &service.Service{}
	m.width = 80
	m.height = 24
	m.busy = true
	m.mode = modeApproval
	m.approval.toolCallID = "tool-1"

	next, _ := m.Update(tea.KeyMsg{Type: tea.KeyCtrlC})
	m = next.(model)

	if !m.stopping {
		t.Fatal("expected ctrl+c in busy approval mode to interrupt the turn")
	}
	if m.mode != modeChat {
		t.Fatalf("expected interrupt to leave approval mode, got %v", m.mode)
	}
	if len(*intents) != 2 ||
		(*intents)[0].Kind != service.IntentCancelToolApproval ||
		(*intents)[0].ToolCallID != "tool-1" ||
		(*intents)[1].Kind != service.IntentShutdown {
		t.Fatalf("expected cancel approval then shutdown intents, got %+v", *intents)
	}
}
func TestApprovalBusyViewDoesNotDuplicatePromptInBusyStatus(t *testing.T) {
	m := newModel(nil, "", "", "")
	m.width = 80
	m.height = 24
	m.startBusy()
	m.busySince = time.Now().Add(-12 * time.Second)
	m.mode = modeApproval
	m.approval.toolName = "shell_run"
	m.approval.reason = "shell_run: sleep 30"

	view := m.View()
	if strings.Contains(view, "Approval required · shell command") {
		t.Fatalf("approval view should not duplicate the prompt in a busy status line:\n%s", view)
	}
	if strings.Contains(view, "Esc/Ctrl+C to interrupt") {
		t.Fatalf("approval busy status line should not advertise esc as interrupt:\n%s", view)
	}
	if count := strings.Count(view, "Approval required"); count != 1 {
		t.Fatalf("approval view should show one approval title, got %d:\n%s", count, view)
	}
}
func TestApprovalViewHidesToolCallID(t *testing.T) {
	m := newModel(nil, "", "", "")
	m.width = 80
	m.height = 24
	m.mode = modeApproval
	m.approval.toolCallID = "tc-123"
	m.approval.toolName = "edit"
	m.approval.reason = "edit: internal/tui/model.go"
	view := m.View()
	if !strings.Contains(view, "Approval required") || !strings.Contains(view, "edit") {
		t.Fatalf("expected approval header in view:\n%s", view)
	}
	if strings.Contains(view, "id: tc-123") {
		t.Fatalf("approval view should not expose tool call id:\n%s", view)
	}
}
func TestApprovalViewSeparatesToolNameFromDetail(t *testing.T) {
	m := newModel(nil, "", "", "")
	m.width = 80
	m.height = 24
	m.mode = modeApproval
	m.approval.toolName = "shell_run"
	m.approval.reason = "shell_run: date"
	view := m.View()
	if strings.Contains(view, "shell_run: date") {
		t.Fatalf("approval view should not repeat tool name in body:\n%s", view)
	}
	if strings.Contains(view, "shell_run") {
		t.Fatalf("approval view should not expose internal shell tool name:\n%s", view)
	}
	if !strings.Contains(view, "Approval required") || !strings.Contains(view, "shell command") || !strings.Contains(xansi.Strip(view), "$ date") {
		t.Fatalf("expected separated approval tool and detail:\n%s", view)
	}
}
func TestApprovalViewShowsExternalDirectoryMetadata(t *testing.T) {
	m := newModel(nil, "", "", "")
	m.width = 100
	m.height = 30
	m.mode = modeApproval
	m.approval.toolName = "mcp__fs__list_directory"
	m.approval.reason = "mcp__fs__list_directory"
	m.approval.metadata = map[string]any{
		"permission_kind":   "external_directory",
		"permission_target": "/Users/goranka/Engineer/ai/dsk/opencode-dev",
	}

	view := xansi.Strip(m.View())
	for _, want := range []string{
		"Approval required: file access",
		"mcp__fs__list_directory",
		"Allow access to this path.",
		"Path: /Users/goranka/Engineer/ai/dsk/opencode-dev",
	} {
		if !strings.Contains(view, want) {
			t.Fatalf("expected approval view to contain %q:\n%s", want, view)
		}
	}
	if strings.Contains(view, "external_directory:*=ask") {
		t.Fatalf("approval view should not expose raw rule labels:\n%s", view)
	}
}
func TestApprovalViewHidesDuplicatePendingToolRow(t *testing.T) {
	m := newModel(nil, "", "", "")
	m.width = 100
	m.height = 24
	m.startBusy()
	m.mode = modeApproval
	m.approval.toolCallID = "tool-1"
	m.approval.toolName = "shell_run"
	m.approval.reason = "shell_run: git diff -- internal/tui/render.go | head -600"
	m.assembler.AddToolCall("tool-1", "shell_run", "Running git diff -- internal/tui/render.go | head -600")

	view := xansi.Strip(m.View())
	if strings.Contains(view, "Running git diff") {
		t.Fatalf("approval view should hide duplicate pending tool row:\n%s", view)
	}
	if count := strings.Count(view, "git diff -- internal/tui/render.go | head -600"); count != 1 {
		t.Fatalf("approval view should render the command exactly once, got %d:\n%s", count, view)
	}
	if !strings.Contains(view, "$ git diff -- internal/tui/render.go | head -600") {
		t.Fatalf("approval view should render a command body:\n%s", view)
	}
}
func TestApprovalViewHidesShellSessionScope(t *testing.T) {
	m := newModel(nil, "", "", "")
	m.width = 100
	m.height = 24
	m.mode = modeApproval
	m.approval.toolName = "shell_run"
	m.approval.reason = "shell_run: date"
	m.approval.metadata = map[string]any{"approval_session_scope": "this shell command"}

	view := xansi.Strip(m.View())
	if !strings.Contains(view, "Allow session (s)") {
		t.Fatalf("expected shell session option:\n%s", view)
	}
	if strings.Contains(view, "Allow session (s) same command") || strings.Contains(view, "Allow for session") || strings.Contains(view, "this shell command") {
		t.Fatalf("approval shell session option should hide scope detail:\n%s", view)
	}
}
func TestApprovalViewPreservesExactShellCommandText(t *testing.T) {
	m := newModel(nil, "", "", "")
	m.width = 120
	m.height = 30
	m.mode = modeApproval
	m.approval.toolName = "shell_run"
	cmd := "printf 'a  b'\n  echo \"c  d\" | head -1"
	m.approval.reason = "shell_run: " + cmd

	view := xansi.Strip(m.View())
	if !strings.Contains(view, "$ printf 'a  b'") {
		t.Fatalf("approval should preserve quoted repeated spaces:\n%s", view)
	}
	if !strings.Contains(view, "  echo \"c  d\" | head -1") {
		t.Fatalf("approval should preserve embedded newline and indentation:\n%s", view)
	}
	if strings.Contains(view, "printf 'a b'") || strings.Contains(view, "echo \"c d\"") {
		t.Fatalf("approval collapsed command whitespace:\n%s", view)
	}
}
func TestApprovalViewShowsDiffMetadata(t *testing.T) {
	m := newModel(nil, "", "", "")
	m.width = 100
	m.height = 30
	m.mode = modeApproval
	m.approval.toolName = "edit"
	m.approval.reason = "edit: a.txt"
	m.approval.metadata = testFileDiffMetadata()
	view := m.View()
	for _, want := range []string{"a.txt (+1 -1)", "-world", "+whale"} {
		if !strings.Contains(view, want) {
			t.Fatalf("expected approval diff metadata to contain %q:\n%s", want, view)
		}
	}
	for _, unwanted := range []string{"--- a/a.txt", "+++ b/a.txt", "@@ -1 +1 @@"} {
		if strings.Contains(view, unwanted) {
			t.Fatalf("approval diff should hide raw diff header %q:\n%s", unwanted, view)
		}
	}
}
func TestApprovalViewShowsFileReviewSessionScope(t *testing.T) {
	m := newModel(nil, "", "", "")
	m.width = 100
	m.height = 30
	m.mode = modeApproval
	m.approval.toolName = "apply_patch"
	m.approval.reason = "apply_patch: a.txt, b.txt"
	m.approval.metadata = testFileDiffMetadata()
	m.approval.metadata["approval_kind"] = "file_diff_review"
	m.approval.metadata["approval_session_scope"] = "these files: a.txt, b.txt"

	view := m.View()
	for _, want := range []string{
		"Approval required: file diff review",
		"Review file changes before Whale applies them.",
		"Allow session (s)",
		"a.txt (+1 -1)",
	} {
		if !strings.Contains(view, want) {
			t.Fatalf("expected approval view to contain %q:\n%s", want, view)
		}
	}
	if strings.Contains(view, "Allow for session =") || strings.Contains(view, "these files: a.txt, b.txt") {
		t.Fatalf("approval view should not expose session scope detail:\n%s", view)
	}
}
func TestApprovalViewUsesSimilarCommandsLabelForShellFamily(t *testing.T) {
	m := newModel(nil, "", "", "")
	m.width = 100
	m.height = 30
	m.mode = modeApproval
	m.approval.toolName = "shell_run"
	m.approval.reason = "shell_run: go test ./internal/policy"
	m.approval.metadata = map[string]any{
		"approval_session_scope": "this bounded shell command family",
		"shell_approval_family":  true,
	}

	view := m.View()
	if !strings.Contains(view, "Allow similar commands") {
		t.Fatalf("expected similar-commands option:\n%s", view)
	}
	if strings.Contains(view, "this bounded shell command family") || strings.Contains(view, "Allow for session =") {
		t.Fatalf("approval view should not expose shell scope detail:\n%s", view)
	}
}
func TestApprovalViewKeepsLargeDiffPreviewBounded(t *testing.T) {
	m := newModel(nil, "", "", "")
	m.width = 120
	m.height = 30
	m.mode = modeApproval
	m.approval.toolName = "apply_patch"
	m.approval.reason = "apply_patch: roadmap.md"
	m.approval.metadata = largeTranslationDiffMetadata(190, 190)
	m.approval.metadata["approval_kind"] = "file_diff_review"

	view := m.View()
	if !strings.Contains(view, "Allow once") || !strings.Contains(view, "Deny") {
		t.Fatalf("expected approval controls to remain visible:\n%s", view)
	}
	if !strings.Contains(view, "... diff truncated (") {
		t.Fatalf("expected approval diff preview to stay bounded:\n%s", view)
	}
	if strings.Contains(view, "+English 189") {
		t.Fatalf("approval preview should not render the full large diff:\n%s", view)
	}
}
func TestApprovalViewShowsMemoryWriteMetadata(t *testing.T) {
	m := newModel(nil, "", "", "")
	m.width = 100
	m.height = 30
	m.mode = modeApproval
	m.approval.toolName = "remember"
	m.approval.reason = "remember: Writes long-term Whale memory."
	m.approval.metadata = map[string]any{
		"approval_kind":          "memory_write",
		"approval_session_scope": "global memory: response-style",
		"memory_scope":           "global",
		"memory_type":            "user",
		"memory_name":            "response-style",
		"memory_description":     "prefers concise Chinese answers",
		"memory_content_preview": "Answer in concise Chinese with repo evidence.",
		"memory_write_status":    "created",
	}

	view := m.View()
	for _, want := range []string{
		"Approval required: memory write",
		"Review memory before Whale saves it.",
		"Created memory: global/user",
		"Name: response-style",
		"Description: prefers concise Chinese answers",
		"Content:",
		"Answer in concise Chinese with repo evidence.",
		"Allow session (s)",
	} {
		if !strings.Contains(view, want) {
			t.Fatalf("expected approval view to contain %q:\n%s", want, view)
		}
	}
	if strings.Contains(view, "Allow for session =") || strings.Contains(view, "global memory: response-style") {
		t.Fatalf("approval view should not expose memory session scope detail:\n%s", view)
	}
}
func TestApprovalViewShowsMemoryDeleteMetadata(t *testing.T) {
	m := newModel(nil, "", "", "")
	m.width = 100
	m.height = 30
	m.mode = modeApproval
	m.approval.toolName = "forget"
	m.approval.reason = "forget: Deletes long-term Whale memory."
	m.approval.metadata = map[string]any{
		"approval_kind":          "memory_delete",
		"approval_session_scope": "project memory: roadmap",
		"memory_scope":           "project",
		"memory_type":            "project",
		"memory_name":            "roadmap",
		"memory_description":     "plugin-first memory",
		"memory_content_preview": "Memory is the first official plugin.",
	}

	view := m.View()
	for _, want := range []string{
		"Approval required: memory delete",
		"Review memory before Whale deletes it.",
		"Delete memory: project/project",
		"Name: roadmap",
		"Description: plugin-first memory",
		"Memory is the first official plugin.",
		"Allow session (s)",
	} {
		if !strings.Contains(view, want) {
			t.Fatalf("expected approval view to contain %q:\n%s", want, view)
		}
	}
	if strings.Contains(view, "Allow for session =") || strings.Contains(view, "project memory: roadmap") {
		t.Fatalf("approval view should not expose memory session scope detail:\n%s", view)
	}
}
func TestApprovalDiffMetadataRendersMultipleFiles(t *testing.T) {
	metadata := map[string]any{
		"kind": "file_diff",
		"files": []any{
			map[string]any{
				"path":         "a.txt",
				"unified_diff": "--- a/a.txt\n+++ b/a.txt\n@@ -1 +1 @@\n-old\n+new",
				"additions":    1,
				"deletions":    1,
			},
			map[string]any{
				"path":         "b.txt",
				"unified_diff": "--- a/b.txt\n+++ b/b.txt\n@@ -0,0 +1 @@\n+created",
				"additions":    1,
				"deletions":    0,
			},
		},
	}
	got := renderApprovalDiffMetadata(metadata, 80)
	for _, want := range []string{"a.txt (+1 -1)", "-old", "+new", "b.txt (+1 -0)", "+created"} {
		if !strings.Contains(got, want) {
			t.Fatalf("expected rendered approval diff to contain %q:\n%s", want, got)
		}
	}
}
func TestApprovalDiffMetadataPreviewErrorFallback(t *testing.T) {
	metadata := map[string]any{
		"kind":          "file_diff",
		"preview_error": "could not read file",
	}
	got := renderApprovalDiffMetadata(metadata, 80)
	if !strings.Contains(got, "diff preview unavailable: could not read file") {
		t.Fatalf("expected preview error fallback, got:\n%s", got)
	}
}
func TestApprovalDiffMetadataTruncatesLongPreview(t *testing.T) {
	metadata := map[string]any{
		"kind": "file_diff",
		"files": []any{
			map[string]any{
				"path":         "a.txt",
				"unified_diff": "--- a/a.txt\n+++ b/a.txt\n@@ -1,4 +1,4 @@\n one\n-two\n+TWO\n three\n-four\n+FOUR",
				"additions":    2,
				"deletions":    2,
			},
		},
	}
	got := renderApprovalDiffMetadata(metadata, 4)
	if !strings.Contains(got, "... diff truncated (") {
		t.Fatalf("expected hidden-line truncation marker, got:\n%s", got)
	}
	if strings.Contains(got, "@@") {
		t.Fatalf("truncated approval diff should still hide hunk headers:\n%s", got)
	}
}
func TestApprovalDiffMetadataShowsFileTruncatedMarker(t *testing.T) {
	metadata := map[string]any{
		"kind": "file_diff",
		"files": []any{
			map[string]any{
				"path":         "a.txt",
				"unified_diff": "--- a/a.txt\n+++ b/a.txt\n@@ -1 +1 @@\n-old\n+new",
				"additions":    1,
				"deletions":    1,
				"truncated":    true,
			},
		},
	}
	got := renderApprovalDiffMetadata(metadata, 80)
	if !strings.Contains(got, "... diff truncated ...") {
		t.Fatalf("expected per-file truncation marker, got:\n%s", got)
	}
}
