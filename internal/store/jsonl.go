package store

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/usewhale/whale/internal/core"
	"github.com/usewhale/whale/internal/securefs"
)

type JSONLStore struct {
	mu          sync.Mutex
	sessionsDir string
	seqBySess   map[string]int
}

type approvalsFile struct {
	Approvals []string `json:"approvals"`
}

const (
	DataDirEnv = "WHALE_HOME"
)

func NewJSONLStore(sessionsDir string) (*JSONLStore, error) {
	if sessionsDir == "" {
		return nil, fmt.Errorf("sessionsDir is required")
	}
	if err := securefs.MkdirPrivate(sessionsDir); err != nil {
		return nil, fmt.Errorf("create sessions dir: %w", err)
	}
	return &JSONLStore{
		sessionsDir: sessionsDir,
		seqBySess:   map[string]int{},
	}, nil
}

func DefaultDataDir() string {
	return defaultDataDir(runtime.GOOS, os.Getenv, os.UserHomeDir)
}

func defaultDataDir(goos string, getenv func(string) string, userHomeDir func() (string, error)) string {
	if dataDir := strings.TrimSpace(getenv(DataDirEnv)); dataDir != "" {
		return dataDir
	}
	if goos == "windows" {
		home, err := userHomeDir()
		if err != nil || strings.TrimSpace(home) == "" {
			return ".whale"
		}
		return filepath.Join(strings.TrimSpace(home), ".whale")
	}
	if home := strings.TrimSpace(getenv("HOME")); home != "" {
		return filepath.Join(home, ".whale")
	}
	home, err := userHomeDir()
	if err != nil || strings.TrimSpace(home) == "" {
		return ".whale"
	}
	return filepath.Join(strings.TrimSpace(home), ".whale")
}

func DefaultSessionsDir(dataDir string) string {
	if strings.TrimSpace(dataDir) == "" {
		dataDir = DefaultDataDir()
	}
	return filepath.Join(dataDir, "sessions")
}

func MostRecentSessionID(sessionsDir string) (string, error) {
	entries, err := os.ReadDir(sessionsDir)
	if err != nil {
		if os.IsNotExist(err) {
			return "", nil
		}
		return "", err
	}
	type candidate struct {
		id    string
		mtime time.Time
	}
	cands := make([]candidate, 0, len(entries))
	for _, e := range entries {
		if e.IsDir() || !core.IsSessionJSONLName(e.Name()) {
			continue
		}
		id := strings.TrimSuffix(e.Name(), ".jsonl")
		if id == "" {
			continue
		}
		info, err := e.Info()
		if err != nil {
			continue
		}
		cands = append(cands, candidate{id: id, mtime: info.ModTime()})
	}
	if len(cands) == 0 {
		return "", nil
	}
	sort.Slice(cands, func(i, j int) bool { return cands[i].mtime.After(cands[j].mtime) })
	return cands[0].id, nil
}

func (s *JSONLStore) List(_ context.Context, sessionID string) ([]core.Message, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	msgs, _, err := s.readSessionLocked(sessionID)
	return msgs, err
}

func (s *JSONLStore) Create(_ context.Context, msg core.Message) (core.Message, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	msgs, seq, err := s.readSessionLocked(msg.SessionID)
	if err != nil {
		return core.Message{}, err
	}
	if cached := s.seqBySess[msg.SessionID]; cached > seq {
		seq = cached
	}
	seq++
	s.seqBySess[msg.SessionID] = seq
	msg.ID = fmt.Sprintf("m-%d", seq)
	now := time.Now()
	msg.CreatedAt = now
	msg.UpdatedAt = now

	if err := s.appendMessageLocked(msg.SessionID, msg); err != nil {
		return core.Message{}, err
	}
	_ = msgs
	return msg, nil
}

func (s *JSONLStore) Update(_ context.Context, msg core.Message) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	msgs, seq, err := s.readSessionLocked(msg.SessionID)
	if err != nil {
		return err
	}
	found := false
	for i := range msgs {
		if msgs[i].ID == msg.ID {
			msg.CreatedAt = msgs[i].CreatedAt
			msg.UpdatedAt = time.Now()
			msgs[i] = msg
			found = true
			break
		}
	}
	if !found {
		return fmt.Errorf("message not found: %s", msg.ID)
	}
	s.seqBySess[msg.SessionID] = seq
	return s.rewriteSessionLocked(msg.SessionID, msgs)
}

func (s *JSONLStore) readSessionLocked(sessionID string) ([]core.Message, int, error) {
	path := s.sessionPath(sessionID)
	f, err := os.Open(path)
	if err != nil {
		if os.IsNotExist(err) {
			return []core.Message{}, 0, nil
		}
		return nil, 0, fmt.Errorf("open session: %w", err)
	}
	defer f.Close()

	scanner := bufio.NewScanner(f)
	buf := make([]byte, 0, 64*1024)
	scanner.Buffer(buf, 2*1024*1024)

	out := make([]core.Message, 0, 64)
	maxSeq := 0
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" {
			continue
		}
		var m core.Message
		if err := json.Unmarshal([]byte(line), &m); err != nil {
			continue
		}
		out = append(out, m)
		if n := parseMessageSeq(m.ID); n > maxSeq {
			maxSeq = n
		}
	}
	if err := scanner.Err(); err != nil {
		return nil, 0, fmt.Errorf("scan session: %w", err)
	}
	return out, maxSeq, nil
}

func (s *JSONLStore) appendMessageLocked(sessionID string, msg core.Message) error {
	path := s.sessionPath(sessionID)
	f, err := securefs.OpenPrivateAppend(path)
	if err != nil {
		return fmt.Errorf("open append: %w", err)
	}
	defer f.Close()
	b, err := json.Marshal(msg)
	if err != nil {
		return fmt.Errorf("marshal message: %w", err)
	}
	if _, err := f.Write(append(b, '\n')); err != nil {
		return fmt.Errorf("append message: %w", err)
	}
	return nil
}

func (s *JSONLStore) rewriteSessionLocked(sessionID string, msgs []core.Message) error {
	path := s.sessionPath(sessionID)
	tmp := path + ".tmp"
	f, err := securefs.OpenPrivateTruncate(tmp)
	if err != nil {
		return fmt.Errorf("open temp: %w", err)
	}
	for _, msg := range msgs {
		b, err := json.Marshal(msg)
		if err != nil {
			_ = f.Close()
			return fmt.Errorf("marshal message: %w", err)
		}
		if _, err := f.Write(append(b, '\n')); err != nil {
			_ = f.Close()
			return fmt.Errorf("write temp: %w", err)
		}
	}
	if err := f.Close(); err != nil {
		return fmt.Errorf("close temp: %w", err)
	}
	if err := os.Rename(tmp, path); err != nil {
		return fmt.Errorf("replace session file: %w", err)
	}
	return nil
}

func (s *JSONLStore) sessionPath(sessionID string) string {
	return filepath.Join(s.sessionsDir, core.SanitizeSessionID(sessionID)+".jsonl")
}

func (s *JSONLStore) approvalsPath(sessionID string) string {
	return filepath.Join(s.sessionsDir, core.SanitizeSessionID(sessionID)+".approvals.json")
}

func (s *JSONLStore) GetApprovals(_ context.Context, sessionID string) (map[string]bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	path := s.approvalsPath(sessionID)
	b, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return map[string]bool{}, nil
		}
		return nil, fmt.Errorf("read approvals: %w", err)
	}
	var af approvalsFile
	if err := json.Unmarshal(b, &af); err != nil {
		return nil, fmt.Errorf("unmarshal approvals: %w", err)
	}
	out := map[string]bool{}
	for _, k := range af.Approvals {
		out[k] = true
	}
	return out, nil
}

func (s *JSONLStore) GrantApproval(_ context.Context, sessionID, key string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	path := s.approvalsPath(sessionID)
	cur := map[string]bool{}
	if b, err := os.ReadFile(path); err == nil {
		var af approvalsFile
		if json.Unmarshal(b, &af) == nil {
			for _, k := range af.Approvals {
				cur[k] = true
			}
		}
	}
	cur[key] = true
	keys := make([]string, 0, len(cur))
	for k := range cur {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	payload, err := json.Marshal(approvalsFile{Approvals: keys})
	if err != nil {
		return fmt.Errorf("marshal approvals: %w", err)
	}
	if err := securefs.WritePrivateFile(path, payload); err != nil {
		return fmt.Errorf("write approvals: %w", err)
	}
	return nil
}

func (s *JSONLStore) RewriteSession(_ context.Context, sessionID string, msgs []core.Message) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.rewriteSessionLocked(sessionID, msgs)
}

func parseMessageSeq(id string) int {
	if !strings.HasPrefix(id, "m-") {
		return 0
	}
	n, err := strconv.Atoi(strings.TrimPrefix(id, "m-"))
	if err != nil {
		return 0
	}
	return n
}
