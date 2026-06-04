package store

import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/usewhale/whale/internal/core"
)

type MessageStore interface {
	List(ctx context.Context, sessionID string) ([]core.Message, error)
	Create(ctx context.Context, msg core.Message) (core.Message, error)
	Update(ctx context.Context, msg core.Message) error
}

type SessionRewriteStore interface {
	RewriteSession(ctx context.Context, sessionID string, msgs []core.Message) error
}

type ApprovalStore interface {
	GetApprovals(ctx context.Context, sessionID string) (map[string]bool, error)
	GrantApproval(ctx context.Context, sessionID, key string) error
}

type InMemoryStore struct {
	mu        sync.Mutex
	seq       int
	bySesID   map[string][]core.Message
	approvals map[string]map[string]bool
}

func NewInMemoryStore() *InMemoryStore {
	return &InMemoryStore{bySesID: map[string][]core.Message{}, approvals: map[string]map[string]bool{}}
}

func (s *InMemoryStore) List(_ context.Context, sessionID string) ([]core.Message, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	msgs := s.bySesID[sessionID]
	out := make([]core.Message, len(msgs))
	for i, msg := range msgs {
		out[i] = core.NormalizeMessageContent(msg)
	}
	return out, nil
}

func (s *InMemoryStore) Create(_ context.Context, msg core.Message) (core.Message, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	msg = core.NormalizeMessageContent(msg)
	s.seq++
	msg.ID = fmt.Sprintf("m-%d", s.seq)
	now := time.Now()
	msg.CreatedAt = now
	msg.UpdatedAt = now
	s.bySesID[msg.SessionID] = append(s.bySesID[msg.SessionID], msg)
	return msg, nil
}

func (s *InMemoryStore) Update(_ context.Context, msg core.Message) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	msg = core.NormalizeMessageContent(msg)
	msgs := s.bySesID[msg.SessionID]
	for i := range msgs {
		if msgs[i].ID == msg.ID {
			msg.UpdatedAt = time.Now()
			msgs[i] = msg
			s.bySesID[msg.SessionID] = msgs
			return nil
		}
	}
	return fmt.Errorf("message not found: %s", msg.ID)
}

func (s *InMemoryStore) GetApprovals(_ context.Context, sessionID string) (map[string]bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	src := s.approvals[sessionID]
	out := map[string]bool{}
	for k, v := range src {
		out[k] = v
	}
	return out, nil
}

func (s *InMemoryStore) GrantApproval(_ context.Context, sessionID, key string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.approvals[sessionID] == nil {
		s.approvals[sessionID] = map[string]bool{}
	}
	s.approvals[sessionID][key] = true
	return nil
}

func (s *InMemoryStore) RewriteSession(_ context.Context, sessionID string, msgs []core.Message) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]core.Message, len(msgs))
	for i, msg := range msgs {
		out[i] = core.NormalizeMessageContent(msg)
	}
	s.bySesID[sessionID] = out
	return nil
}
