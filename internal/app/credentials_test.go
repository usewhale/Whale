package app

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

func TestCredentialsRoundTrip(t *testing.T) {
	dir := t.TempDir()
	creds := Credentials{DeepSeekAPIKey: "sk-1234567890abcdef1234"}

	if err := SaveCredentials(dir, creds); err != nil {
		t.Fatalf("SaveCredentials: %v", err)
	}

	path := credentialsPath(dir)
	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("stat credentials.json: %v", err)
	}
	if runtime.GOOS != "windows" && info.Mode().Perm() != 0o600 {
		got := info.Mode().Perm()
		t.Fatalf("credentials.json perms: want 0600, got %o", got)
	}

	loaded, err := LoadCredentials(dir)
	if err != nil {
		t.Fatalf("LoadCredentials: %v", err)
	}
	if loaded.DeepSeekAPIKey != creds.DeepSeekAPIKey {
		t.Fatalf("deepseek_api_key: want %q, got %q", creds.DeepSeekAPIKey, loaded.DeepSeekAPIKey)
	}
}

func TestLoadCredentialsMissingFile(t *testing.T) {
	dir := t.TempDir()
	loaded, err := LoadCredentials(dir)
	if err != nil {
		t.Fatalf("LoadCredentials: %v", err)
	}
	if loaded.DeepSeekAPIKey != "" {
		t.Fatalf("expected empty key, got %q", loaded.DeepSeekAPIKey)
	}
}

func TestLoadCredentialsInvalidJSON(t *testing.T) {
	dir := t.TempDir()
	path := credentialsPath(dir)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(path, []byte("{"), 0o600); err != nil {
		t.Fatalf("write: %v", err)
	}
	if _, err := LoadCredentials(dir); err == nil {
		t.Fatal("expected invalid JSON error")
	}
}

func TestLoadDeepSeekAPIKeyPrefersEnvironment(t *testing.T) {
	dir := t.TempDir()
	if err := SaveCredentials(dir, Credentials{DeepSeekAPIKey: "sk-file0000000000000000"}); err != nil {
		t.Fatalf("SaveCredentials: %v", err)
	}
	t.Setenv("DEEPSEEK_API_KEY", "sk-env00000000000000000")

	got, err := LoadDeepSeekAPIKey(dir)
	if err != nil {
		t.Fatalf("LoadDeepSeekAPIKey: %v", err)
	}
	if got != "sk-env00000000000000000" {
		t.Fatalf("key: want env value, got %q", got)
	}
}

func TestLoadDeepSeekAPIKeyFallsBackToCredentials(t *testing.T) {
	dir := t.TempDir()
	want := "sk-file0000000000000000"
	if err := SaveCredentials(dir, Credentials{DeepSeekAPIKey: want}); err != nil {
		t.Fatalf("SaveCredentials: %v", err)
	}
	t.Setenv("DEEPSEEK_API_KEY", "")

	got, err := LoadDeepSeekAPIKey(dir)
	if err != nil {
		t.Fatalf("LoadDeepSeekAPIKey: %v", err)
	}
	if got != want {
		t.Fatalf("key: want %q, got %q", want, got)
	}
}

func TestValidateDeepSeekAPIKey(t *testing.T) {
	if err := ValidateDeepSeekAPIKey("sk-1234567890abcdef1234"); err != nil {
		t.Fatalf("expected valid key: %v", err)
	}
	for _, tc := range []string{"", "abc", "sk-short"} {
		if err := ValidateDeepSeekAPIKey(tc); err == nil {
			t.Fatalf("expected invalid key for %q", tc)
		}
	}
}
