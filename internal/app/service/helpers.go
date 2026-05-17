package service

import "github.com/usewhale/whale/internal/policy"

const (
	ApprovalChoiceAskFirst           = "Ask before tools run"
	ApprovalChoiceAutoApproveSession = "Auto approve all tools for this session"
)

func onOff(v bool) string {
	if v {
		return "on"
	}
	return "off"
}

func approvalModeDisplay(mode policy.ApprovalMode) string {
	switch mode {
	case policy.ApprovalModeNever:
		return ApprovalChoiceAutoApproveSession
	default:
		return ApprovalChoiceAskFirst
	}
}

func approvalModeChoices() []string {
	return []string{ApprovalChoiceAskFirst, ApprovalChoiceAutoApproveSession}
}
