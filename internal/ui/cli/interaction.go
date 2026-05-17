package cli

import (
	"bufio"
	"fmt"
	"os"
	"strconv"
	"strings"

	"github.com/usewhale/whale/internal/agent"
	"github.com/usewhale/whale/internal/core"
	"github.com/usewhale/whale/internal/policy"
)

func promptUserInputCLI(req agent.UserInputRequest) (core.UserInputResponse, bool) {
	reader := bufio.NewReader(os.Stdin)
	answers := make([]core.UserInputAnswer, 0, len(req.Questions))
	fmt.Printf("\n[user-input] tool:%s(%s)\n", req.ToolCall.Name, req.ToolCall.ID)
	for i, q := range req.Questions {
		fmt.Printf("Q%d [%s] %s\n", i+1, q.Header, q.Question)
		for j, opt := range q.Options {
			fmt.Printf("  %d) %s - %s\n", j+1, opt.Label, opt.Description)
		}
		fmt.Printf("  0) Other (type your own)\n")
		fmt.Printf("  c) Cancel\n")
		for {
			fmt.Print("choice> ")
			raw, _ := reader.ReadString('\n')
			v := strings.TrimSpace(raw)
			if strings.EqualFold(v, "c") || strings.EqualFold(v, "cancel") {
				return core.UserInputResponse{}, false
			}
			if v == "0" {
				fmt.Print("other> ")
				txt, _ := reader.ReadString('\n')
				txt = strings.TrimSpace(txt)
				if txt == "" {
					fmt.Println("empty input, try again")
					continue
				}
				answers = append(answers, core.UserInputAnswer{ID: q.ID, Label: "Other", Value: txt, IsOther: true})
				break
			}
			idx, err := strconv.Atoi(v)
			if err != nil || idx < 1 || idx > len(q.Options) {
				fmt.Println("invalid choice, try again")
				continue
			}
			opt := q.Options[idx-1]
			answers = append(answers, core.UserInputAnswer{ID: q.ID, Label: opt.Label, Value: opt.Label})
			break
		}
	}
	return core.UserInputResponse{Answers: answers}, true
}

func promptApprovalCLI(req policy.ApprovalRequest) policy.ApprovalDecision {
	scope := strings.TrimSpace(policy.ApprovalSessionScope(req.ToolCall))
	fmt.Printf("\n[approval] %s\n", policy.ApprovalSummary(req.ToolCall))
	if scope != "" {
		fmt.Printf("[approval] Allow for session = %s\n", scope)
	}
	fmt.Printf("[approval] allow %s (%s)? [y/s/N]: ", req.ToolCall.Name, req.Reason)
	reader := bufio.NewReader(os.Stdin)
	raw, _ := reader.ReadString('\n')
	v := strings.ToLower(strings.TrimSpace(raw))
	switch v {
	case "y", "yes":
		return policy.ApprovalAllow
	case "s", "session":
		return policy.ApprovalAllowForSession
	default:
		return policy.ApprovalDeny
	}
}
