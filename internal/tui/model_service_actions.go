package tui

import (
	tea "github.com/charmbracelet/bubbletea"

	"github.com/usewhale/whale/internal/runtime/protocol"
)

type uiActionKind string

const (
	uiActionModelPicker     uiActionKind = "model_picker"
	uiActionPermissionsMenu uiActionKind = "permissions_menu"
	uiActionSkillsMenu      uiActionKind = "skills_menu"
	uiActionSkillsManager   uiActionKind = "skills_manager"
	uiActionPluginsManager  uiActionKind = "plugins_manager"
	uiActionReviewMenu      uiActionKind = "review_menu"
	uiActionClearScreen     uiActionKind = "clear_screen"
)

type uiAction struct {
	kind uiActionKind
	ev   protocol.Event
}

func uiActionFromServiceEvent(ev protocol.Event) (uiAction, bool) {
	switch ev.Kind {
	case protocol.EventModelSelectionRequested:
		return uiAction{kind: uiActionModelPicker, ev: ev}, true
	case protocol.EventPermissionsSelectionRequested:
		return uiAction{kind: uiActionPermissionsMenu, ev: ev}, true
	case protocol.EventSkillsSelectionRequested:
		return uiAction{kind: uiActionSkillsMenu, ev: ev}, true
	case protocol.EventSkillsManagerUpdated:
		return uiAction{kind: uiActionSkillsManager, ev: ev}, true
	case protocol.EventPluginsManagerUpdated:
		return uiAction{kind: uiActionPluginsManager, ev: ev}, true
	case protocol.EventReviewRequested:
		return uiAction{kind: uiActionReviewMenu, ev: ev}, true
	case protocol.EventScreenClearRequested:
		return uiAction{kind: uiActionClearScreen, ev: ev}, true
	default:
		return uiAction{}, false
	}
}

func (m *model) handleUIAction(action uiAction) (tea.Cmd, bool, bool) {
	switch action.kind {
	case uiActionModelPicker:
		m.handleModelPickerEvent(action.ev)
	case uiActionPermissionsMenu:
		m.handlePermissionsMenuEvent(action.ev)
	case uiActionSkillsMenu:
		m.handleSkillsMenuEvent(action.ev)
	case uiActionSkillsManager:
		m.handleSkillsManagerEvent(action.ev)
	case uiActionPluginsManager:
		m.handlePluginsManagerEvent(action.ev)
	case uiActionReviewMenu:
		m.handleReviewMenuEvent(action.ev)
	case uiActionClearScreen:
		return m.handleClearScreenEvent(), false, true
	}
	return nil, false, false
}
