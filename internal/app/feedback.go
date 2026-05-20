package app

import (
	"fmt"
	"os/exec"
	"runtime"
)

const FeedbackIssuesURL = "https://github.com/usewhale/DeepSeek-Code-Whale/issues"

var openFeedbackURL = openURL

func openFeedbackIssues() string {
	if err := openFeedbackURL(FeedbackIssuesURL); err != nil {
		return fmt.Sprintf("Feedback issues:\n%s\n\nCould not open browser: %v", FeedbackIssuesURL, err)
	}
	return fmt.Sprintf("Opening feedback issues:\n%s", FeedbackIssuesURL)
}

func openURL(url string) error {
	var cmd *exec.Cmd
	switch runtime.GOOS {
	case "darwin":
		cmd = exec.Command("open", url)
	case "windows":
		cmd = exec.Command("rundll32", "url.dll,FileProtocolHandler", url)
	default:
		cmd = exec.Command("xdg-open", url)
	}
	if err := cmd.Start(); err != nil {
		return err
	}
	go func() {
		_ = cmd.Wait()
	}()
	return nil
}
