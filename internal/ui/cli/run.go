package cli

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"os"
	"os/signal"
	"strings"
	"sync"

	"github.com/usewhale/whale/internal/agent"
	"github.com/usewhale/whale/internal/app"
	llmretry "github.com/usewhale/whale/internal/llm/retry"
	"github.com/usewhale/whale/internal/plugins"
)

func Run(cfg app.Config, start app.StartOptions) error {
	ctx := context.Background()
	start.ApprovalFunc = promptApprovalCLI
	start.UserInputFunc = promptUserInputCLI
	coreApp, err := app.New(ctx, cfg, start)
	if err != nil {
		if app.IsCrossWorkspaceResumeError(err) {
			fmt.Println(err.Error())
			return nil
		}
		return err
	}
	defer coreApp.Close()
	coreApp.InitializeMCP(ctx, nil)
	for _, line := range coreApp.StartupLines() {
		fmt.Println(line)
	}

	scanner := bufio.NewScanner(os.Stdin)
	if start.ResumeMenu {
		if err := promptResumeChoice(scanner, coreApp); err != nil {
			return err
		}
	}
	turn := 0
	sigCh := make(chan os.Signal, 2)
	signal.Notify(sigCh, cliInterruptSignals()...)
	defer signal.Stop(sigCh)
	var turnCancelMu sync.Mutex
	var turnCancel context.CancelFunc

	for {
		fmt.Print("> ")
		if !scanner.Scan() {
			break
		}
		line := strings.TrimSpace(scanner.Text())
		if line == "" {
			continue
		}
		hiddenInput := false
		skipHooks := false
		turnOptions := agent.RunOptions{HiddenInput: hiddenInput}
		if coreApp.IsResumeMenu(line) {
			choices, err := coreApp.ListResumeChoices(20)
			if err != nil {
				fmt.Fprintf(os.Stderr, "error: %v\n", err)
				continue
			}
			if len(choices) == 0 {
				fmt.Println("no saved sessions")
				continue
			}
			for _, c := range choices {
				fmt.Println(c)
			}
			fmt.Print("resume> choose number or session id (blank to cancel): ")
			if !scanner.Scan() {
				break
			}
			res, err := coreApp.ApplyResumeChoice(scanner.Text())
			if err != nil {
				fmt.Fprintf(os.Stderr, "error: %v\n", err)
				continue
			}
			fmt.Println(res.Message)
			continue
		}

		cmd, err := coreApp.ExecuteSlash(line)
		if err != nil {
			fmt.Fprintf(os.Stderr, "error: %v\n", err)
			continue
		}
		if cmd.Handled {
			if cmd.ClearScreen {
				clearCLIOutput(os.Stdout)
			}
			if cmd.Text != "" {
				fmt.Println(cmd.Text)
			}
			if cmd.ShouldExit {
				break
			}
			if cmd.Turn == nil {
				continue
			}
			line = cmd.Turn.Input
			turnOptions = cliRunOptionsFromCommandTurn(cmd.Turn, hiddenInput)
			hiddenInput = turnOptions.HiddenInput
			skipHooks = cmd.Turn.SkipUserPromptHooks
		}
		cmd, err = coreApp.ExecuteLocalCommand(line)
		if err != nil {
			fmt.Fprintf(os.Stderr, "error: %v\n", err)
			continue
		}
		if cmd.Handled {
			if cmd.Text != "" {
				fmt.Println(cmd.Text)
			}
			if cmd.Turn == nil {
				continue
			}
			line = cmd.Turn.Input
			turnOptions = cliRunOptionsFromCommandTurn(cmd.Turn, hiddenInput)
			hiddenInput = turnOptions.HiddenInput
			skipHooks = cmd.Turn.SkipUserPromptHooks
		}
		if strings.HasPrefix(line, "/") {
			fmt.Fprintf(os.Stderr, "error: unknown command\n")
			continue
		}
		if !skipHooks {
			hookOutBlocked, hookOut, updated := coreApp.RunUserPromptSubmitHook(line)
			line = updated
			if hookOut != "" {
				fmt.Println(hookOut)
			}
			if hookOutBlocked {
				continue
			}
		}

		turnCtx, cancelTurn := context.WithCancel(ctx)
		turnCancelMu.Lock()
		turnCancel = cancelTurn
		turnCancelMu.Unlock()
		done := make(chan struct{})
		go func() {
			defer close(done)
			for {
				select {
				case <-turnCtx.Done():
					return
				case <-sigCh:
					turnCancelMu.Lock()
					if turnCancel != nil {
						turnCancel()
					}
					turnCancelMu.Unlock()
				}
			}
		}()
		events, runErr := coreApp.RunTurnWithOptions(turnCtx, line, turnOptions)
		if runErr != nil {
			cancelTurn()
			turnCancelMu.Lock()
			turnCancel = nil
			turnCancelMu.Unlock()
			<-done
			fmt.Fprintf(os.Stderr, "error: %v\n", runErr)
			continue
		}

		fmt.Print("assistant> ")
		printedText := false
		lastAssistantText := ""
		for ev := range events {
			renderEvent(ev, &printedText, &lastAssistantText)
		}
		cancelTurn()
		turnCancelMu.Lock()
		turnCancel = nil
		turnCancelMu.Unlock()
		<-done
		turn++
		if err := coreApp.FinalizeTurn(lastAssistantText); err != nil {
			fmt.Fprintf(os.Stderr, "patch session meta failed: %v\n", err)
		}
		if hookOut := coreApp.RunStopHook(lastAssistantText, turn); hookOut != "" {
			fmt.Println(hookOut)
		}
	}
	return scanner.Err()
}

func cliRunOptionsFromCommandTurn(turn *plugins.CommandTurn, fallbackHidden bool) agent.RunOptions {
	opts := agent.RunOptions{HiddenInput: fallbackHidden}
	if turn == nil {
		return opts
	}
	opts.HiddenInput = turn.Hidden
	opts.ReadOnly = turn.ReadOnly
	opts.ShellAllowPrefixes = append([]string(nil), turn.ShellAllowPrefixes...)
	return opts
}

func clearCLIOutput(out io.Writer) {
	fmt.Fprint(out, "\033[H\033[2J\033[3J")
	fmt.Fprintln(out, "terminal cleared")
}

func promptResumeChoice(scanner *bufio.Scanner, app *app.App) error {
	choices, err := app.ListResumeChoices(20)
	if err != nil {
		return err
	}
	if len(choices) == 0 {
		fmt.Println("no saved sessions")
		return nil
	}
	for _, c := range choices {
		fmt.Println(c)
	}
	fmt.Print("resume> choose number or session id (blank to cancel): ")
	if !scanner.Scan() {
		return scanner.Err()
	}
	res, err := app.ApplyResumeChoice(scanner.Text())
	if err != nil {
		return err
	}
	fmt.Println(res.Message)
	return nil
}

func renderEvent(ev agent.AgentEvent, printedText *bool, lastAssistantText *string) {
	switch ev.Type {
	case agent.AgentEventTypeAssistantDelta:
		if ev.Content != "" {
			*printedText = true
		}
		fmt.Print(ev.Content)
	case agent.AgentEventTypeReasoningDelta:
		if ev.ReasoningDelta != "" {
			fmt.Printf("\n[think] %s\n", ev.ReasoningDelta)
		}
	case agent.AgentEventTypeToolArgsDelta:
		if ev.ToolArgs != nil {
			fmt.Printf("\n[tool-args] %s#%d %d chars ready:%d\n", ev.ToolArgs.ToolName, ev.ToolArgs.ToolCallIndex, ev.ToolArgs.ArgsChars, ev.ToolArgs.ReadyCount)
		}
	case agent.AgentEventTypeToolArgsRepaired:
		if ev.ToolArgsRepair != nil {
			fmt.Printf("\n[tool-repair] %s#%d repaired\n", ev.ToolArgsRepair.ToolName, ev.ToolArgsRepair.ToolCallIndex)
		}
	case agent.AgentEventTypeProviderRetryScheduled:
		if ev.ProviderRetry != nil {
			fmt.Printf("\n[api-retry] %s\n", llmretry.FormatInfo(*ev.ProviderRetry))
		}
	case agent.AgentEventTypeToolCallBlocked, agent.AgentEventTypeToolModeBlocked:
		if ev.ToolBlocked != nil {
			tag := "tool-blocked"
			if ev.Type == agent.AgentEventTypeToolModeBlocked {
				tag = "tool-mode-blocked"
			}
			fmt.Printf("\n[%s] %s(%s) code:%s\n", tag, ev.ToolBlocked.ToolName, ev.ToolBlocked.ToolCallID, ev.ToolBlocked.ReasonCode)
		}
	case agent.AgentEventTypeToolApprovalRequired:
		if ev.Approval != nil {
			fmt.Printf("\n[tool-approval-required] %s(%s) code:%s scope:%s\n", ev.Approval.ToolName, ev.Approval.ToolCallID, ev.Approval.Code, ev.Approval.Scope)
			if ev.Approval.Summary != "" {
				fmt.Printf("[tool-approval-summary] %s\n", ev.Approval.Summary)
			}
		}
	case agent.AgentEventTypeToolCallScavenged:
		if ev.Scavenged != nil {
			fmt.Printf("\n[tool-scavenge] recovered:%d\n", ev.Scavenged.Count)
		}
	case agent.AgentEventTypeToolPolicyDecision:
		if ev.Policy != nil {
			fmt.Printf("\n[tool-policy] %s(%s) phase:%s code:%s allow:%v need_approval:%v rule:%s\n", ev.Policy.ToolName, ev.Policy.ToolCallID, ev.Policy.Phase, ev.Policy.Code, ev.Policy.Allow, ev.Policy.NeedsApproval, ev.Policy.MatchedRule)
		}
	case agent.AgentEventTypeToolCall:
		if ev.ToolCall != nil {
			fmt.Printf("\n[tool call] %s(%s)\n", ev.ToolCall.Name, ev.ToolCall.ID)
		}
	case agent.AgentEventTypeUserInputRequired:
		if ev.ToolCall != nil && ev.UserInputReq != nil {
			fmt.Printf("\n[user-input-required] %s(%s) questions:%d\n", ev.ToolCall.Name, ev.ToolCall.ID, len(ev.UserInputReq.Questions))
		}
	case agent.AgentEventTypeUserInputSubmitted:
		if ev.ToolCall != nil && ev.UserInputResp != nil {
			fmt.Printf("\n[user-input-submitted] %s(%s) answers:%d\n", ev.ToolCall.Name, ev.ToolCall.ID, len(ev.UserInputResp.Answers))
		}
	case agent.AgentEventTypeUserInputCancelled:
		if ev.ToolCall != nil {
			fmt.Printf("\n[user-input-cancelled] %s(%s)\n", ev.ToolCall.Name, ev.ToolCall.ID)
		}
	case agent.AgentEventTypeToolResult:
		if ev.Result != nil {
			fmt.Printf("[tool result] %s\n", ev.Result.Content)
		}
	case agent.AgentEventTypeDone:
		if !*printedText && ev.Message != nil && ev.Message.Text != "" {
			fmt.Print(ev.Message.Text)
		}
		if ev.Message != nil {
			*lastAssistantText = ev.Message.Text
		}
		fmt.Print("\n")
	case agent.AgentEventTypeError:
		if ev.Err != nil {
			fmt.Fprintf(os.Stderr, "\nerror: %v\n", ev.Err)
		}
	default:
		if ev.Content != "" {
			fmt.Printf("\n[%s] %s\n", ev.Type, strings.TrimSpace(ev.Content))
		}
	}
}
