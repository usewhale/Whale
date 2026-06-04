package core

import "testing"

func TestNormalizeMessageContentBackfillsTextPart(t *testing.T) {
	msg := NormalizeMessageContent(Message{Role: RoleUser, Text: "hello"})

	if len(msg.Parts) != 1 {
		t.Fatalf("parts len = %d, want 1", len(msg.Parts))
	}
	if msg.Parts[0].Type != MessagePartText || msg.Parts[0].Text != "hello" {
		t.Fatalf("unexpected text part: %#v", msg.Parts[0])
	}
	if MessagePlainText(msg) != "hello" {
		t.Fatalf("plain text = %q, want hello", MessagePlainText(msg))
	}
}

func TestUserMessageFromPartsBuildsPlainTextMirror(t *testing.T) {
	msg := UserMessageFromParts("s1", []MessagePart{
		{Type: MessagePartText, Text: "inspect this"},
		{Type: MessagePartAttachment, Attachment: &AttachmentRef{
			Kind:        AttachmentKindImage,
			DisplayName: "screen.png",
		}},
	}, false)

	want := "inspect this\n[image: screen.png]"
	if msg.Text != want {
		t.Fatalf("text = %q, want %q", msg.Text, want)
	}
	if MessagePlainText(msg) != want {
		t.Fatalf("plain text = %q, want %q", MessagePlainText(msg), want)
	}
}

func TestUserMessageFromPartsClonesAttachmentRef(t *testing.T) {
	att := &AttachmentRef{Kind: AttachmentKindPDF, DisplayName: "paper.pdf"}
	msg := UserMessageFromParts("s1", []MessagePart{{Type: MessagePartAttachment, Attachment: att}}, false)

	att.DisplayName = "changed.pdf"
	if got := msg.Parts[0].Attachment.DisplayName; got != "paper.pdf" {
		t.Fatalf("attachment display name = %q, want paper.pdf", got)
	}
}
