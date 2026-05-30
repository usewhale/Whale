package tui

import (
	"github.com/usewhale/whale/internal/runtime/protocol"
	"testing"
)

func TestUIActionFromServiceEvent(t *testing.T) {
	tests := []struct {
		name string
		kind protocol.EventKind
		want uiActionKind
	}{
		{name: "model picker", kind: protocol.EventModelSelectionRequested, want: uiActionModelPicker},
		{name: "permissions menu", kind: protocol.EventPermissionsSelectionRequested, want: uiActionPermissionsMenu},
		{name: "skills menu", kind: protocol.EventSkillsSelectionRequested, want: uiActionSkillsMenu},
		{name: "skills manager", kind: protocol.EventSkillsManagerUpdated, want: uiActionSkillsManager},
		{name: "plugins manager", kind: protocol.EventPluginsManagerUpdated, want: uiActionPluginsManager},
		{name: "review menu", kind: protocol.EventReviewRequested, want: uiActionReviewMenu},
		{name: "clear screen", kind: protocol.EventScreenClearRequested, want: uiActionClearScreen},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, ok := uiActionFromServiceEvent(protocol.Event{Kind: tt.kind})
			if !ok {
				t.Fatalf("expected UI action for %s", tt.kind)
			}
			if got.kind != tt.want {
				t.Fatalf("action kind = %q, want %q", got.kind, tt.want)
			}
		})
	}
}

func TestUIActionFromServiceEventIgnoresRuntimeEvent(t *testing.T) {
	if _, ok := uiActionFromServiceEvent(protocol.Event{Kind: protocol.EventAssistantDelta}); ok {
		t.Fatal("assistant delta should stay on the runtime event path")
	}
}
