package service

import "github.com/usewhale/whale/internal/policy"

const (
	ApprovalChoiceAskFirst           = "Ask before tools run"
	ApprovalChoiceAutoApproveSession = "Auto approve all tools for this session"
	ApprovalChoiceTrustProject       = "Trust this project..."
	ApprovalChoiceClearProject       = "Clear project default"
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
	return []string{
		ApprovalChoiceAskFirst,
		ApprovalChoiceAutoApproveSession,
		ApprovalChoiceTrustProject,
		ApprovalChoiceClearProject,
	}
}

func projectApprovalModeDisplay(mode policy.ApprovalMode) string {
	switch mode {
	case policy.ApprovalModeNever:
		return "auto-approve by default in this workspace"
	default:
		return "ask before tools run by default in this workspace"
	}
}
