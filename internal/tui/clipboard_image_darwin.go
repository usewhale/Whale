//go:build darwin

package tui

import (
	"fmt"
	"os"
	"os/exec"
)

func pasteClipboardImageToTempPNGImpl() (string, error) {
	file, err := os.CreateTemp("", "whale-clipboard-*.png")
	if err != nil {
		return "", fmt.Errorf("create temp image: %w", err)
	}
	path := file.Name()
	if err := file.Chmod(0o600); err != nil {
		_ = file.Close()
		_ = os.Remove(path)
		return "", fmt.Errorf("secure temp image: %w", err)
	}
	if err := file.Close(); err != nil {
		_ = os.Remove(path)
		return "", fmt.Errorf("close temp image: %w", err)
	}

	script := fmt.Sprintf(`
set outPath to POSIX file %q
try
	set pngData to the clipboard as %s
	set outFile to open for access outPath with write permission
	set eof of outFile to 0
	write pngData to outFile
	close access outFile
on error errMsg
	try
		close access outPath
	end try
	error errMsg
end try
`, path, "\u00abclass PNGf\u00bb")
	if out, err := exec.Command("osascript", "-e", script).CombinedOutput(); err != nil {
		_ = os.Remove(path)
		msg := string(out)
		if msg == "" {
			msg = err.Error()
		}
		return "", fmt.Errorf("read clipboard image: %s", msg)
	}
	info, err := os.Stat(path)
	if err != nil {
		_ = os.Remove(path)
		return "", fmt.Errorf("stat clipboard image: %w", err)
	}
	if info.Size() == 0 {
		_ = os.Remove(path)
		return "", fmt.Errorf("no image found on clipboard")
	}
	return path, nil
}
