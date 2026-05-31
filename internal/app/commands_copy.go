package app

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/usewhale/whale/internal/clipboard"
	"github.com/usewhale/whale/internal/core"
)

const copyMaxLookback = 20

var copyClipboardText = func(ctx context.Context, text string, opts clipboard.Options) (clipboard.Result, error) {
	return clipboard.CopyText(ctx, text, opts)
}

func (a *App) executeCopyCommand(line string) (CommandExecution, error) {
	age, err := copyMessageAge(line)
	if err != nil {
		return CommandExecution{Handled: true}, err
	}
	texts, err := a.collectRecentAssistantTexts(copyMaxLookback)
	if err != nil {
		return CommandExecution{Handled: true}, err
	}
	if len(texts) == 0 {
		text := "No assistant message to copy"
		return CommandExecution{Handled: true, Text: text, LocalResult: buildCopyLocalResult(text, clipboard.Result{}, 0)}, nil
	}
	if age >= len(texts) {
		n := len(texts)
		word := "messages"
		if n == 1 {
			word = "message"
		}
		return CommandExecution{Handled: true}, fmt.Errorf("Only %d assistant %s available to copy", n, word)
	}

	result, err := copyClipboardText(a.ctx, texts[age], clipboard.Options{
		FallbackDir:      filepath.Join(os.TempDir(), "whale"),
		FallbackFilename: "response.md",
		TermWriter:       os.Stdout,
	})
	if err != nil {
		return CommandExecution{Handled: true}, err
	}
	text := clipboard.Summary(result)
	return CommandExecution{Handled: true, Text: text, LocalResult: buildCopyLocalResult(text, result, age)}, nil
}

func copyMessageAge(line string) (int, error) {
	fields := strings.Fields(strings.TrimSpace(line))
	if len(fields) == 0 || fields[0] != "/copy" {
		return 0, fmt.Errorf("usage: /copy [N]")
	}
	if len(fields) == 1 {
		return 0, nil
	}
	if len(fields) > 2 {
		return 0, fmt.Errorf("Usage: /copy [N] where N is 1 (latest), 2, 3, ...")
	}
	n, err := strconv.Atoi(fields[1])
	if err != nil || n < 1 {
		return 0, fmt.Errorf("Usage: /copy [N] where N is 1 (latest), 2, 3, ... Got: %s", fields[1])
	}
	return n - 1, nil
}

func (a *App) collectRecentAssistantTexts(limit int) ([]string, error) {
	msgs, err := a.msgStore.List(a.ctx, a.sessionID)
	if err != nil {
		return nil, err
	}
	return collectRecentAssistantTexts(msgs, limit), nil
}

func collectRecentAssistantTexts(msgs []core.Message, limit int) []string {
	out := make([]string, 0, limit)
	for i := len(msgs) - 1; i >= 0 && len(out) < limit; i-- {
		msg := msgs[i]
		if msg.Role != core.RoleAssistant || msg.Hidden || msg.FinishReason == core.FinishReasonError {
			continue
		}
		text := msg.Text
		if strings.TrimSpace(text) == "" {
			continue
		}
		out = append(out, text)
	}
	return out
}

func buildCopyLocalResult(text string, result clipboard.Result, age int) *LocalResult {
	fields := []LocalResultField{{Label: "Action", Value: "copy"}}
	if age > 0 {
		fields = append(fields, LocalResultField{Label: "Message", Value: fmt.Sprintf("%d latest", age+1)})
	} else if result.Chars > 0 {
		fields = append(fields, LocalResultField{Label: "Message", Value: "latest"})
	}
	if result.Chars > 0 {
		fields = append(fields,
			LocalResultField{Label: "Characters", Value: strconv.Itoa(result.Chars)},
			LocalResultField{Label: "Lines", Value: strconv.Itoa(result.Lines)},
		)
	}
	if strings.TrimSpace(result.FilePath) != "" {
		fields = append(fields, LocalResultField{Label: "Fallback file", Value: filepath.ToSlash(result.FilePath)})
	}
	methods := copyMethods(result)
	if methods != "" {
		fields = append(fields, LocalResultField{Label: "Methods", Value: methods})
	}
	return &LocalResult{Kind: "copy", Title: "Copy", Fields: fields, PlainText: text}
}

func copyMethods(result clipboard.Result) string {
	methods := make([]string, 0, 3)
	if result.Native {
		methods = append(methods, "native")
	}
	if result.Tmux {
		methods = append(methods, "tmux")
	}
	if result.OSC52 {
		methods = append(methods, "OSC52")
	}
	return strings.Join(methods, ", ")
}
