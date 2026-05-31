package service

func (s *Service) emitRewindMessages(done bool) {
	messages, err := s.app.ListRewindMessages(s.ctx)
	if err != nil {
		s.emit(Event{Kind: EventError, Text: err.Error()})
		if done {
			s.emit(Event{Kind: EventTurnDone})
		}
		return
	}
	s.emit(Event{Kind: EventRewindMessagesListed, Messages: protocolMessages(messages)})
	if done {
		s.emit(Event{Kind: EventTurnDone})
	}
}
