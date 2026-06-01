package service

func (s *Service) emitSessionHydrated() {
	s.emitSessionHydratedWithMetadata(nil)
}

func (s *Service) emitSessionHydratedWithMetadata(metadata map[string]any) {
	msgs, err := s.app.ListMessages()
	if err != nil {
		s.emit(Event{Kind: EventError, Text: err.Error()})
		return
	}
	s.emit(Event{
		Kind:            EventSessionHydrated,
		SessionID:       s.app.SessionID(),
		Messages:        protocolMessages(msgs),
		AutoAccept:      s.app.AutoAcceptPermissions(),
		AutoAcceptKnown: true,
		Metadata:        metadata,
		Plugins:         protocolPlugins(s.PluginsForManager()),
	})
}

func (s *Service) emitSessionChoices() bool {
	choices, err := s.app.ListResumeChoices(20)
	if err != nil {
		s.emit(Event{Kind: EventError, Text: err.Error()})
		return false
	}
	if len(choices) == 0 {
		s.emit(Event{Kind: EventInfo, Text: "no saved sessions"})
		return false
	}
	s.emit(Event{Kind: EventSessionsListed, Choices: choices})
	return true
}

func (s *Service) emitLocalSessionChoices() bool {
	choices, err := s.app.ListResumeChoices(20)
	if err != nil {
		s.emit(localSubmitResultEvent("error", err.Error()))
		return false
	}
	if len(choices) == 0 {
		s.emit(localSubmitResultEvent("info", "no saved sessions"))
		return false
	}
	s.emit(Event{Kind: EventSessionsListed, Choices: choices})
	return true
}
