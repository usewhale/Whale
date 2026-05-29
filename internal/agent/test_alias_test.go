package agent

import (
	"github.com/usewhale/whale/internal/core"
	"github.com/usewhale/whale/internal/llm"
	"github.com/usewhale/whale/internal/policy"
	"github.com/usewhale/whale/internal/session"
	"github.com/usewhale/whale/internal/store"
)

type Message = core.Message
type Role = core.Role
type FinishReason = core.FinishReason
type ToolCall = core.ToolCall
type ToolResult = core.ToolResult
type Tool = core.Tool
type ToolSpec = core.ToolSpec
type ToolRegistry = core.ToolRegistry

const (
	RoleSystem    = core.RoleSystem
	RoleUser      = core.RoleUser
	RoleAssistant = core.RoleAssistant
	RoleTool      = core.RoleTool
)

const (
	FinishReasonEndTurn  = core.FinishReasonEndTurn
	FinishReasonToolUse  = core.FinishReasonToolUse
	FinishReasonCanceled = core.FinishReasonCanceled
	FinishReasonError    = core.FinishReasonError
)

type EventType = llm.EventType
type ToolArgsDelta = llm.ToolArgsDelta
type Usage = llm.Usage
type ProviderEvent = llm.ProviderEvent
type ProviderResponse = llm.ProviderResponse
type Provider = llm.Provider

const (
	EventContentDelta   = llm.EventContentDelta
	EventReasoningDelta = llm.EventReasoningDelta
	EventToolArgsDelta  = llm.EventToolArgsDelta
	EventToolUseStart   = llm.EventToolUseStart
	EventToolUseStop    = llm.EventToolUseStop
	EventComplete       = llm.EventComplete
	EventError          = llm.EventError
)

type MessageStore = store.MessageStore
type ApprovalStore = store.ApprovalStore

type PolicyDecision = policy.PolicyDecision
type ToolPolicy = policy.ToolPolicy
type DefaultToolPolicy = policy.DefaultToolPolicy
type RulePolicy = policy.RulePolicy
type PermissionRule = policy.PermissionRule
type ApprovalRequest = policy.ApprovalRequest
type ApprovalDecision = policy.ApprovalDecision
type ApprovalFunc = policy.ApprovalFunc

const (
	ApprovalDeny            = policy.ApprovalDeny
	ApprovalAllow           = policy.ApprovalAllow
	ApprovalAllowForSession = policy.ApprovalAllowForSession
	ApprovalCancel          = policy.ApprovalCancel
	PermissionAllow         = policy.PermissionAllow
	PermissionAsk           = policy.PermissionAsk
)

var (
	NewToolRegistry        = core.NewToolRegistry
	NewToolRegistryChecked = core.NewToolRegistryChecked
	NewInMemoryStore       = store.NewInMemoryStore
	NewJSONLStore          = store.NewJSONLStore
	MostRecentSessionID    = store.MostRecentSessionID
	ListSessions           = session.ListSessions
	DefaultRules           = policy.DefaultRules
)
