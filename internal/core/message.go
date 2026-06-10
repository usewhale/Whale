package core

import (
	"strings"
	"time"
)

type Role string

const (
	RoleSystem    Role = "system"
	RoleUser      Role = "user"
	RoleAssistant Role = "assistant"
	RoleTool      Role = "tool"
)

type FinishReason string

const (
	FinishReasonEndTurn  FinishReason = "end_turn"
	FinishReasonToolUse  FinishReason = "tool_use"
	FinishReasonCanceled FinishReason = "canceled"
	FinishReasonError    FinishReason = "error"
)

type Message struct {
	ID           string
	SessionID    string
	Role         Role
	Text         string
	Parts        []MessagePart `json:"parts,omitempty"`
	Hidden       bool
	Reasoning    string
	ToolCalls    []ToolCall
	ToolResults  []ToolResult
	FinishReason FinishReason
	CreatedAt    time.Time
	UpdatedAt    time.Time
}

type MessagePartType string

const (
	MessagePartText       MessagePartType = "text"
	MessagePartAttachment MessagePartType = "attachment"
	MessagePartPlan       MessagePartType = "plan"
)

type AttachmentKind string

const (
	AttachmentKindImage AttachmentKind = "image"
	AttachmentKindPDF   AttachmentKind = "pdf"
	AttachmentKindAudio AttachmentKind = "audio"
	AttachmentKindFile  AttachmentKind = "file"
)

type MessagePart struct {
	Type       MessagePartType `json:"type"`
	Text       string          `json:"text,omitempty"`
	Attachment *AttachmentRef  `json:"attachment,omitempty"`
}

type AttachmentRef struct {
	Kind         AttachmentKind `json:"kind"`
	Path         string         `json:"path,omitempty"`
	OriginalPath string         `json:"original_path,omitempty"`
	MIME         string         `json:"mime,omitempty"`
	Filename     string         `json:"filename,omitempty"`
	SizeBytes    int64          `json:"size_bytes,omitempty"`
	SHA256       string         `json:"sha256,omitempty"`
	DisplayName  string         `json:"display_name,omitempty"`
}

func TextMessage(sessionID string, role Role, text string, hidden bool) Message {
	return NormalizeMessageContent(Message{
		SessionID: sessionID,
		Role:      role,
		Text:      text,
		Hidden:    hidden,
	})
}

func UserMessageFromParts(sessionID string, parts []MessagePart, hidden bool) Message {
	return NormalizeMessageContent(Message{
		SessionID: sessionID,
		Role:      RoleUser,
		Parts:     cloneMessageParts(parts),
		Hidden:    hidden,
	})
}

func NormalizeMessageContent(msg Message) Message {
	if len(msg.Parts) == 0 && msg.Text != "" {
		msg.Parts = []MessagePart{{Type: MessagePartText, Text: msg.Text}}
	} else if msg.Text == "" && len(msg.Parts) > 0 {
		msg.Text = MessagePartsPlainText(msg.Parts)
	}
	return msg
}

func MessagePlainText(msg Message) string {
	if len(msg.Parts) == 0 {
		return msg.Text
	}
	return MessagePartsPlainText(msg.Parts)
}

func MessagePartsPlainText(parts []MessagePart) string {
	fragments := make([]string, 0, len(parts))
	for _, part := range parts {
		switch part.Type {
		case MessagePartText:
			if part.Text != "" {
				fragments = append(fragments, part.Text)
			}
		case MessagePartPlan:
			if part.Text != "" {
				fragments = append(fragments, part.Text)
			}
		case MessagePartAttachment:
			if label := attachmentPlaceholder(part.Attachment); label != "" {
				fragments = append(fragments, label)
			}
		}
	}
	return strings.Join(fragments, "\n")
}

func attachmentPlaceholder(att *AttachmentRef) string {
	if att == nil {
		return ""
	}
	kind := strings.TrimSpace(string(att.Kind))
	if kind == "" {
		kind = string(AttachmentKindFile)
	}
	name := strings.TrimSpace(att.DisplayName)
	if name == "" {
		name = strings.TrimSpace(att.Filename)
	}
	if name == "" {
		name = strings.TrimSpace(att.Path)
	}
	if name == "" {
		return "[" + kind + "]"
	}
	return "[" + kind + ": " + name + "]"
}

func cloneMessageParts(in []MessagePart) []MessagePart {
	if len(in) == 0 {
		return nil
	}
	out := make([]MessagePart, len(in))
	for i, part := range in {
		out[i] = part
		if part.Attachment != nil {
			att := *part.Attachment
			out[i].Attachment = &att
		}
	}
	return out
}

type ToolCall struct {
	ID    string
	Name  string
	Input string
}

type ToolResult struct {
	ToolCallID string
	Name       string
	Content    string
	Metadata   map[string]any `json:"metadata,omitempty"`
	IsError    bool
	// Channel-separated fields (phase 1): Outcome/Code/Payload are the
	// structured channel for the TUI, recovery, and evals; ModelText is
	// the only text the model sees, rendered once at creation and never
	// re-rendered. Content/IsError remain during the migration and are
	// removed in the final step.
	Outcome   ToolOutcome `json:"Outcome,omitempty"`
	Code      string      `json:"Code,omitempty"`
	Payload   any         `json:"Payload,omitempty"`
	ModelText string      `json:"ModelText,omitempty"`
}
