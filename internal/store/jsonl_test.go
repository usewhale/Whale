package store

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/usewhale/whale/internal/core"
)

func TestDefaultDataDirIgnoresHomeOnWindows(t *testing.T) {
	got := defaultDataDir("windows", getenv(map[string]string{
		"HOME": `C:\msys64\home\goranka`,
	}), func() (string, error) {
		return `C:\Users\goranka`, nil
	})
	want := filepath.Join(`C:\Users\goranka`, ".whale")
	if got != want {
		t.Fatalf("defaultDataDir windows = %q, want %q", got, want)
	}
}

func TestDefaultDataDirUsesWhaleHomeOverride(t *testing.T) {
	got := defaultDataDir("windows", getenv(map[string]string{
		DataDirEnv: `D:\WhaleData`,
		"HOME":     `C:\msys64\home\goranka`,
	}), func() (string, error) {
		return `C:\Users\goranka`, nil
	})
	if got != `D:\WhaleData` {
		t.Fatalf("defaultDataDir with %s = %q, want D:\\WhaleData", DataDirEnv, got)
	}
}

func TestDefaultDataDirIgnoresBlankWhaleHomeOverride(t *testing.T) {
	got := defaultDataDir("linux", getenv(map[string]string{
		DataDirEnv: "  ",
		"HOME":     "/home/dev",
	}), func() (string, error) {
		return "/ignored", nil
	})
	want := filepath.Join("/home/dev", ".whale")
	if got != want {
		t.Fatalf("defaultDataDir with blank %s = %q, want %q", DataDirEnv, got, want)
	}
}

func TestDefaultDataDirDoesNotFallbackToHomeOnWindows(t *testing.T) {
	got := defaultDataDir("windows", getenv(map[string]string{
		"HOME": `C:\msys64\home\goranka`,
	}), func() (string, error) {
		return "", errors.New("user home unavailable")
	})
	if got != ".whale" {
		t.Fatalf("defaultDataDir windows without user home = %q, want .whale", got)
	}
}

func TestDefaultDataDirUsesHomeOnNonWindows(t *testing.T) {
	got := defaultDataDir("linux", getenv(map[string]string{
		"HOME": "/home/dev",
	}), func() (string, error) {
		return "/ignored", nil
	})
	want := filepath.Join("/home/dev", ".whale")
	if got != want {
		t.Fatalf("defaultDataDir linux = %q, want %q", got, want)
	}
}

func TestDefaultDataDirFallsBackToUserHomeOnNonWindows(t *testing.T) {
	got := defaultDataDir("darwin", getenv(nil), func() (string, error) {
		return "/Users/dev", nil
	})
	want := filepath.Join("/Users/dev", ".whale")
	if got != want {
		t.Fatalf("defaultDataDir darwin = %q, want %q", got, want)
	}
}

func TestMostRecentSessionIDIgnoresToolInputEventSidecars(t *testing.T) {
	dir := t.TempDir()
	sessionPath := filepath.Join(dir, "s1.jsonl")
	sidecarPath := filepath.Join(dir, "s1.tool_input_events.jsonl")
	approvalPath := filepath.Join(dir, "s1.approval_events.jsonl")
	if err := os.WriteFile(sessionPath, []byte("{}\n"), 0o600); err != nil {
		t.Fatalf("write session: %v", err)
	}
	if err := os.WriteFile(sidecarPath, []byte("{}\n"), 0o600); err != nil {
		t.Fatalf("write sidecar: %v", err)
	}
	if err := os.WriteFile(approvalPath, []byte("{}\n"), 0o600); err != nil {
		t.Fatalf("write approval sidecar: %v", err)
	}
	now := time.Now()
	_ = os.Chtimes(sessionPath, now.Add(-time.Hour), now.Add(-time.Hour))
	_ = os.Chtimes(sidecarPath, now, now)
	_ = os.Chtimes(approvalPath, now.Add(time.Minute), now.Add(time.Minute))

	got, err := MostRecentSessionID(dir)
	if err != nil {
		t.Fatalf("most recent session: %v", err)
	}
	if got != "s1" {
		t.Fatalf("expected s1, got %q", got)
	}
}

func TestJSONLStoreNormalizesLegacyTextMessage(t *testing.T) {
	dir := t.TempDir()
	st, err := NewJSONLStore(dir)
	if err != nil {
		t.Fatal(err)
	}

	if _, err := st.Create(t.Context(), core.Message{SessionID: "s1", Role: core.RoleUser, Text: "legacy"}); err != nil {
		t.Fatal(err)
	}
	msgs, err := st.List(t.Context(), "s1")
	if err != nil {
		t.Fatal(err)
	}
	if len(msgs) != 1 || len(msgs[0].Parts) != 1 {
		t.Fatalf("messages = %#v", msgs)
	}
	if msgs[0].Parts[0].Type != core.MessagePartText || msgs[0].Parts[0].Text != "legacy" {
		t.Fatalf("unexpected parts: %#v", msgs[0].Parts)
	}
}

func TestJSONLStorePreservesAttachmentParts(t *testing.T) {
	dir := t.TempDir()
	st, err := NewJSONLStore(dir)
	if err != nil {
		t.Fatal(err)
	}

	created, err := st.Create(t.Context(), core.UserMessageFromParts("s1", []core.MessagePart{
		{Type: core.MessagePartText, Text: "look"},
		{Type: core.MessagePartAttachment, Attachment: &core.AttachmentRef{
			Kind:        core.AttachmentKindImage,
			Path:        "screen.png",
			MIME:        "image/png",
			SizeBytes:   12,
			SHA256:      "abc",
			DisplayName: "screen.png",
		}},
	}, false))
	if err != nil {
		t.Fatal(err)
	}

	msgs, err := st.List(t.Context(), "s1")
	if err != nil {
		t.Fatal(err)
	}
	if len(msgs) != 1 {
		t.Fatalf("message count = %d, want 1", len(msgs))
	}
	if msgs[0].ID != created.ID {
		t.Fatalf("id = %q, want %q", msgs[0].ID, created.ID)
	}
	if len(msgs[0].Parts) != 2 || msgs[0].Parts[1].Attachment == nil {
		t.Fatalf("unexpected parts: %#v", msgs[0].Parts)
	}
	if got := msgs[0].Parts[1].Attachment.DisplayName; got != "screen.png" {
		t.Fatalf("display name = %q, want screen.png", got)
	}
}

func getenv(values map[string]string) func(string) string {
	return func(key string) string {
		return values[key]
	}
}
