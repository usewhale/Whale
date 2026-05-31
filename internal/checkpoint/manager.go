package checkpoint

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/usewhale/whale/internal/core"
	"github.com/usewhale/whale/internal/securefs"
)

const maxSnapshots = 100

type contextKey struct{}

var ErrNoCheckpoint = errors.New("no checkpoint")

type Recorder interface {
	TrackFileBeforeMutation(absPath string) error
	TrackFilePreimage(absPath string, data []byte, missing bool, mode fs.FileMode) error
}

func WithRecorder(ctx context.Context, r Recorder) context.Context {
	if r == nil {
		return ctx
	}
	return context.WithValue(ctx, contextKey{}, r)
}

func RecorderFromContext(ctx context.Context) Recorder {
	if ctx == nil {
		return nil
	}
	r, _ := ctx.Value(contextKey{}).(Recorder)
	return r
}

type Manager struct {
	sessionsDir string
	workspace   string
	mu          sync.Mutex
}

type State struct {
	Snapshots []Snapshot `json:"snapshots"`
}

type Snapshot struct {
	MessageID string          `json:"message_id"`
	CreatedAt time.Time       `json:"created_at"`
	Files     map[string]File `json:"files"`
}

type File struct {
	RelPath string `json:"rel_path"`
	Backup  string `json:"backup,omitempty"`
	Missing bool   `json:"missing,omitempty"`
	Mode    uint32 `json:"mode,omitempty"`
	SHA256  string `json:"sha256,omitempty"`
}

type RestoreReport struct {
	FilesRestored int
	FilesDeleted  int
	FilesSkipped  int
}

func NewManager(sessionsDir, workspace string) *Manager {
	return &Manager{sessionsDir: strings.TrimSpace(sessionsDir), workspace: cleanAbs(workspace)}
}

func (m *Manager) Recorder(sessionID, messageID string) Recorder {
	if m == nil || strings.TrimSpace(sessionID) == "" || strings.TrimSpace(messageID) == "" {
		return nil
	}
	return recorder{m: m, sessionID: sessionID, messageID: messageID}
}

type recorder struct {
	m         *Manager
	sessionID string
	messageID string
}

func (r recorder) TrackFileBeforeMutation(absPath string) error {
	return r.m.TrackFile(r.sessionID, r.messageID, absPath)
}

func (r recorder) TrackFilePreimage(absPath string, data []byte, missing bool, mode fs.FileMode) error {
	return r.m.TrackFilePreimage(r.sessionID, r.messageID, absPath, data, missing, mode)
}

func (m *Manager) CreateSnapshot(sessionID, messageID string) error {
	if m == nil || strings.TrimSpace(sessionID) == "" || strings.TrimSpace(messageID) == "" {
		return nil
	}
	m.mu.Lock()
	defer m.mu.Unlock()

	st, err := m.loadStateLocked(sessionID)
	if err != nil {
		return err
	}
	for _, snap := range st.Snapshots {
		if snap.MessageID == messageID {
			return nil
		}
	}
	files, err := m.snapshotTrackedFilesLocked(sessionID, st)
	if err != nil {
		return err
	}
	st.Snapshots = append(st.Snapshots, Snapshot{
		MessageID: messageID,
		CreatedAt: time.Now(),
		Files:     files,
	})
	if len(st.Snapshots) > maxSnapshots {
		st.Snapshots = append([]Snapshot(nil), st.Snapshots[len(st.Snapshots)-maxSnapshots:]...)
	}
	return m.saveStateLocked(sessionID, st)
}

func (m *Manager) snapshotTrackedFilesLocked(sessionID string, st State) (map[string]File, error) {
	files := map[string]File{}
	if len(st.Snapshots) == 0 {
		return files, nil
	}
	previous := st.Snapshots[len(st.Snapshots)-1]
	for rel := range previous.Files {
		file, err := m.backupFileLocked(sessionID, previous.MessageID+"-next", rel)
		if err != nil {
			return nil, err
		}
		files[rel] = file
	}
	return files, nil
}

func (m *Manager) TrackFile(sessionID, messageID, absPath string) error {
	if m == nil || strings.TrimSpace(sessionID) == "" || strings.TrimSpace(messageID) == "" {
		return nil
	}
	rel, ok := m.relPath(absPath)
	if !ok {
		return nil
	}
	m.mu.Lock()
	defer m.mu.Unlock()

	st, err := m.loadStateLocked(sessionID)
	if err != nil {
		return err
	}
	idx := snapshotIndex(st.Snapshots, messageID)
	if idx < 0 {
		st.Snapshots = append(st.Snapshots, Snapshot{
			MessageID: messageID,
			CreatedAt: time.Now(),
			Files:     map[string]File{},
		})
		idx = len(st.Snapshots) - 1
	}
	if st.Snapshots[idx].Files == nil {
		st.Snapshots[idx].Files = map[string]File{}
	}
	if _, exists := st.Snapshots[idx].Files[rel]; exists {
		return nil
	}
	file, err := m.backupFileLocked(sessionID, messageID, rel)
	if err != nil {
		return err
	}
	st.Snapshots[idx].Files[rel] = file
	if len(st.Snapshots) > maxSnapshots {
		st.Snapshots = append([]Snapshot(nil), st.Snapshots[len(st.Snapshots)-maxSnapshots:]...)
	}
	return m.saveStateLocked(sessionID, st)
}

func (m *Manager) TrackFilePreimage(sessionID, messageID, absPath string, data []byte, missing bool, mode fs.FileMode) error {
	if m == nil || strings.TrimSpace(sessionID) == "" || strings.TrimSpace(messageID) == "" {
		return nil
	}
	rel, ok := m.relPath(absPath)
	if !ok {
		return nil
	}
	m.mu.Lock()
	defer m.mu.Unlock()

	st, err := m.loadStateLocked(sessionID)
	if err != nil {
		return err
	}
	idx := snapshotIndex(st.Snapshots, messageID)
	if idx < 0 {
		st.Snapshots = append(st.Snapshots, Snapshot{
			MessageID: messageID,
			CreatedAt: time.Now(),
			Files:     map[string]File{},
		})
		idx = len(st.Snapshots) - 1
	}
	if st.Snapshots[idx].Files == nil {
		st.Snapshots[idx].Files = map[string]File{}
	}
	if _, exists := st.Snapshots[idx].Files[rel]; exists {
		return nil
	}
	file, err := m.backupPreimageLocked(sessionID, messageID, rel, data, missing, mode)
	if err != nil {
		return err
	}
	st.Snapshots[idx].Files[rel] = file
	if len(st.Snapshots) > maxSnapshots {
		st.Snapshots = append([]Snapshot(nil), st.Snapshots[len(st.Snapshots)-maxSnapshots:]...)
	}
	return m.saveStateLocked(sessionID, st)
}

func (m *Manager) CanRestore(sessionID, messageID string) bool {
	st, err := m.loadState(sessionID)
	if err != nil {
		return false
	}
	idx := snapshotIndex(st.Snapshots, messageID)
	return idx >= 0 && len(st.Snapshots[idx].Files) > 0
}

func (m *Manager) Restore(sessionID, messageID string) (RestoreReport, error) {
	if m == nil {
		return RestoreReport{}, errors.New("checkpoint manager is not configured")
	}
	m.mu.Lock()
	defer m.mu.Unlock()

	st, err := m.loadStateLocked(sessionID)
	if err != nil {
		return RestoreReport{}, err
	}
	idx := snapshotIndex(st.Snapshots, messageID)
	if idx < 0 {
		return RestoreReport{}, fmt.Errorf("%w for message %s", ErrNoCheckpoint, messageID)
	}
	var report RestoreReport
	for rel, file := range st.Snapshots[idx].Files {
		if rel == "" {
			rel = file.RelPath
		}
		target, ok := m.absFromRel(rel)
		if !ok {
			report.FilesSkipped++
			continue
		}
		if file.Missing {
			if err := os.Remove(target); err != nil {
				if os.IsNotExist(err) {
					report.FilesSkipped++
					continue
				}
				return report, fmt.Errorf("remove %s: %w", rel, err)
			}
			report.FilesDeleted++
			continue
		}
		if strings.TrimSpace(file.Backup) == "" {
			report.FilesSkipped++
			continue
		}
		backup := filepath.Join(m.backupsDir(sessionID), file.Backup)
		data, err := os.ReadFile(backup)
		if err != nil {
			return report, fmt.Errorf("read backup for %s: %w", rel, err)
		}
		if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
			return report, fmt.Errorf("create parent for %s: %w", rel, err)
		}
		mode := fs.FileMode(file.Mode)
		if mode == 0 {
			mode = 0o644
		}
		if err := os.WriteFile(target, data, mode); err != nil {
			return report, fmt.Errorf("restore %s: %w", rel, err)
		}
		report.FilesRestored++
	}
	return report, nil
}

func (m *Manager) loadState(sessionID string) (State, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.loadStateLocked(sessionID)
}

func (m *Manager) loadStateLocked(sessionID string) (State, error) {
	var st State
	path := m.statePath(sessionID)
	if path == "" {
		return st, nil
	}
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return st, nil
		}
		return st, fmt.Errorf("read checkpoints: %w", err)
	}
	if len(strings.TrimSpace(string(data))) == 0 {
		return st, nil
	}
	if err := json.Unmarshal(data, &st); err != nil {
		return st, fmt.Errorf("parse checkpoints: %w", err)
	}
	return st, nil
}

func (m *Manager) saveStateLocked(sessionID string, st State) error {
	path := m.statePath(sessionID)
	if path == "" {
		return nil
	}
	data, err := json.MarshalIndent(st, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal checkpoints: %w", err)
	}
	return securefs.WritePrivateFile(path, append(data, '\n'))
}

func (m *Manager) backupFileLocked(sessionID, messageID, rel string) (File, error) {
	out := File{RelPath: rel}
	abs, ok := m.absFromRel(rel)
	if !ok {
		return out, nil
	}
	data, err := os.ReadFile(abs)
	if err != nil {
		if os.IsNotExist(err) {
			out.Missing = true
			return out, nil
		}
		return out, fmt.Errorf("read %s: %w", rel, err)
	}
	if info, err := os.Stat(abs); err == nil {
		out.Mode = uint32(info.Mode().Perm())
	}
	sum := sha256.Sum256(data)
	out.SHA256 = hex.EncodeToString(sum[:])
	name := shortHash(messageID + "\x00" + rel + "\x00" + out.SHA256)
	out.Backup = name
	if err := securefs.WritePrivateFile(filepath.Join(m.backupsDir(sessionID), name), data); err != nil {
		return out, fmt.Errorf("write backup for %s: %w", rel, err)
	}
	return out, nil
}

func (m *Manager) backupPreimageLocked(sessionID, messageID, rel string, data []byte, missing bool, mode fs.FileMode) (File, error) {
	out := File{RelPath: rel}
	if missing {
		out.Missing = true
		return out, nil
	}
	out.Mode = uint32(mode.Perm())
	sum := sha256.Sum256(data)
	out.SHA256 = hex.EncodeToString(sum[:])
	name := shortHash(messageID + "\x00" + rel + "\x00" + out.SHA256)
	out.Backup = name
	if err := securefs.WritePrivateFile(filepath.Join(m.backupsDir(sessionID), name), data); err != nil {
		return out, fmt.Errorf("write backup for %s: %w", rel, err)
	}
	return out, nil
}

func (m *Manager) statePath(sessionID string) string {
	if m == nil || strings.TrimSpace(m.sessionsDir) == "" || strings.TrimSpace(sessionID) == "" {
		return ""
	}
	return filepath.Join(m.sessionsDir, core.SanitizeSessionID(sessionID)+".checkpoints.json")
}

func (m *Manager) backupsDir(sessionID string) string {
	return filepath.Join(m.sessionsDir, core.SanitizeSessionID(sessionID)+".files")
}

func (m *Manager) relPath(absPath string) (string, bool) {
	abs := cleanAbs(absPath)
	if abs == "" || m.workspace == "" {
		return "", false
	}
	rel, err := filepath.Rel(m.workspace, abs)
	if err != nil || rel == "." || rel == "" || strings.HasPrefix(rel, ".."+string(os.PathSeparator)) || rel == ".." || filepath.IsAbs(rel) {
		return "", false
	}
	return filepath.ToSlash(filepath.Clean(rel)), true
}

func (m *Manager) absFromRel(rel string) (string, bool) {
	rel = filepath.Clean(filepath.FromSlash(strings.TrimSpace(rel)))
	if rel == "" || rel == "." || strings.HasPrefix(rel, ".."+string(os.PathSeparator)) || rel == ".." || filepath.IsAbs(rel) || m.workspace == "" {
		return "", false
	}
	return filepath.Join(m.workspace, rel), true
}

func snapshotIndex(snaps []Snapshot, messageID string) int {
	for i := range snaps {
		if snaps[i].MessageID == messageID {
			return i
		}
	}
	return -1
}

func cleanAbs(path string) string {
	path = strings.TrimSpace(path)
	if path == "" {
		return ""
	}
	if abs, err := filepath.Abs(path); err == nil {
		return filepath.Clean(abs)
	}
	return filepath.Clean(path)
}

func shortHash(s string) string {
	sum := sha256.Sum256([]byte(s))
	return hex.EncodeToString(sum[:])[:24]
}
