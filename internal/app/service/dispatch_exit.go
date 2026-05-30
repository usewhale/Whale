package service

import (
	"strings"
)

func (s *Service) requestExit() {
	summary, ok, err := s.app.BuildWorktreeExitSummary()
	if !ok {
		s.emit(Event{Kind: EventExitRequested})
		return
	}
	if err != nil {
		res, clearErr := s.app.ForgetCurrentWorktree()
		if clearErr != nil {
			s.emit(Event{Kind: EventError, Text: err.Error() + "\n" + clearErr.Error()})
			s.emit(Event{Kind: EventExitRequested})
			return
		}
		s.emit(Event{Kind: EventInfo, Text: res.Message})
		s.emit(Event{Kind: EventExitRequested})
		return
	}
	if summary.ChangedFiles == 0 && summary.IgnoredFiles == 0 && summary.Commits == 0 {
		res, err := s.app.RemoveCurrentWorktree(false)
		if err != nil {
			s.emit(Event{Kind: EventError, Text: err.Error()})
			return
		}
		s.emit(Event{Kind: EventInfo, Text: res.Message})
		s.emit(Event{Kind: EventExitRequested})
		return
	}
	s.emit(Event{Kind: EventWorktreeExitPrompt, WorktreeExit: protocolWorktreeExitSummary(summary)})
}

func (s *Service) handleWorktreeExitChoice(action string) {
	switch strings.TrimSpace(action) {
	case "keep":
		res, err := s.app.KeepCurrentWorktree()
		if err != nil {
			s.emit(Event{Kind: EventError, Text: err.Error()})
			return
		}
		s.emit(Event{Kind: EventInfo, Text: res.Message})
		s.emit(Event{Kind: EventExitRequested})
	case "remove":
		res, err := s.app.RemoveCurrentWorktree(true)
		if err != nil {
			s.emit(Event{Kind: EventError, Text: err.Error()})
			return
		}
		s.emit(Event{Kind: EventInfo, Text: res.Message})
		s.emit(Event{Kind: EventExitRequested})
	case "cancel":
		s.emit(Event{Kind: EventInfo, Text: "Exit canceled"})
	default:
		s.emit(Event{Kind: EventError, Text: "unknown worktree exit action"})
	}
}
