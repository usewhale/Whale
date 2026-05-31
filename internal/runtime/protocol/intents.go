package protocol

type IntentKind string

const (
	IntentSubmit              IntentKind = "submit"
	IntentSubmitLocal         IntentKind = "submit_local"
	IntentAllowTool           IntentKind = "allow_tool"
	IntentAllowToolForSession IntentKind = "allow_tool_for_session"
	IntentDenyTool            IntentKind = "deny_tool"
	IntentCancelToolApproval  IntentKind = "cancel_tool_approval"
	IntentSubmitUserInput     IntentKind = "submit_user_input"
	IntentCancelUserInput     IntentKind = "cancel_user_input"
	IntentSelectSession       IntentKind = "select_session"
	IntentSelectRewindMessage IntentKind = "select_rewind_message"
	IntentRequestSessions     IntentKind = "request_sessions"
	IntentRequestExit         IntentKind = "request_exit"
	IntentShutdown            IntentKind = "shutdown"
	IntentSetModelAndEffort   IntentKind = "set_model_and_effort"
	IntentSetApprovalMode     IntentKind = "set_approval_mode"
	IntentSetViewMode         IntentKind = "set_view_mode"
	IntentToggleMode          IntentKind = "toggle_mode"
	IntentImplementPlan       IntentKind = "implement_plan"
	IntentDeclinePlan         IntentKind = "decline_plan"
	IntentRequestSkillsManage IntentKind = "request_skills_manage"
	IntentSetSkillEnabled     IntentKind = "set_skill_enabled"
	IntentSetPluginEnabled    IntentKind = "set_plugin_enabled"
	IntentWorktreeExitChoice  IntentKind = "worktree_exit_choice"
)

type Intent struct {
	Kind           IntentKind         `json:"kind"`
	Input          string             `json:"input,omitempty"`
	HiddenInput    bool               `json:"hidden_input,omitempty"`
	ToolCallID     string             `json:"tool_call_id,omitempty"`
	UserInput      *UserInputResponse `json:"user_input,omitempty"`
	SessionInput   string             `json:"session_input,omitempty"`
	MessageID      string             `json:"message_id,omitempty"`
	Model          string             `json:"model,omitempty"`
	Effort         string             `json:"effort,omitempty"`
	Thinking       string             `json:"thinking,omitempty"`
	ApprovalMode   string             `json:"approval_mode,omitempty"`
	ViewMode       string             `json:"view_mode,omitempty"`
	SkillName      string             `json:"skill_name,omitempty"`
	SkillEnabled   bool               `json:"skill_enabled,omitempty"`
	PluginID       string             `json:"plugin_id,omitempty"`
	PluginEnabled  bool               `json:"plugin_enabled,omitempty"`
	SkillBinding   *SkillBinding      `json:"skill_binding,omitempty"`
	WorktreeAction string             `json:"worktree_action,omitempty"`
}

type SkillBinding struct {
	Name          string `json:"name,omitempty"`
	SkillFilePath string `json:"skill_file_path,omitempty"`
}

type UserInputAnswer struct {
	ID      string `json:"id"`
	Label   string `json:"label"`
	Value   string `json:"value"`
	IsOther bool   `json:"is_other,omitempty"`
}

type UserInputResponse struct {
	Answers []UserInputAnswer `json:"answers"`
}

type ClientMessageType string

const (
	ClientMessageIntent ClientMessageType = "intent"
)

type ClientMessage struct {
	Type   ClientMessageType `json:"type"`
	Intent *Intent           `json:"intent,omitempty"`
}

type ServerMessageType string

const (
	ServerMessageReady  ServerMessageType = "ready"
	ServerMessageEvent  ServerMessageType = "event"
	ServerMessageError  ServerMessageType = "error"
	ServerMessageClosed ServerMessageType = "closed"
)

type ServerMessage struct {
	Type          ServerMessageType `json:"type"`
	Event         *Event            `json:"event,omitempty"`
	Error         string            `json:"error,omitempty"`
	SessionID     string            `json:"session_id,omitempty"`
	WorkspaceRoot string            `json:"workspace_root,omitempty"`
}
