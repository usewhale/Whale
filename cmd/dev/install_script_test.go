package main

import (
	"os/exec"
	"path/filepath"
	"runtime"
	"testing"
)

func TestInstallScriptWarnsWhenWhaleIsShadowed(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("scripts/install.sh is for Unix-like systems")
	}

	root := filepath.Clean(filepath.Join("..", ".."))
	cmd := exec.Command("sh", filepath.Join("scripts", "install_test.sh"))
	cmd.Dir = root
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("install_test.sh failed: %v\n%s", err, out)
	}
}
