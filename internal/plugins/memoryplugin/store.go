package memoryplugin

import (
	"crypto/sha1"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"
)

const (
	PluginID        = "memory"
	IndexFileName   = "MEMORY.md"
	indexMaxBytes   = 4 * 1024
	globalScope     = "global"
	projectScope    = "project"
	defaultFileMode = 0o600
)

var validMemoryName = regexp.MustCompile(`^[A-Za-z0-9_-][A-Za-z0-9_.-]{1,38}[A-Za-z0-9]$`)

type Store struct {
	root          string
	workspaceRoot string
	now           func() time.Time
}

type Entry struct {
	Name            string
	Type            string
	Scope           string
	Description     string
	Content         string
	Created         string
	Updated         string
	Path            string
	UpdatedExisting bool
}

type WriteInput struct {
	Name        string
	Type        string
	Scope       string
	Description string
	Content     string
}

func NewStore(root, workspaceRoot string) *Store {
	return &Store{
		root:          strings.TrimSpace(root),
		workspaceRoot: strings.TrimSpace(workspaceRoot),
		now:           time.Now,
	}
}

func (s *Store) Root() string { return s.root }

func (s *Store) ScopeDir(scope string) (string, error) {
	scope, err := normalizeScope(scope)
	if err != nil {
		return "", err
	}
	if s.root == "" {
		return "", errors.New("memory data root is required")
	}
	if scope == globalScope {
		return filepath.Join(s.root, "data", "global"), nil
	}
	if strings.TrimSpace(s.workspaceRoot) == "" {
		return "", errors.New("project memory requires a workspace root")
	}
	return filepath.Join(s.root, "projects", WorkspaceHash(s.workspaceRoot)), nil
}

func (s *Store) IndexPath(scope string) (string, error) {
	dir, err := s.ScopeDir(scope)
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, IndexFileName), nil
}

func (s *Store) Write(in WriteInput) (Entry, error) {
	name, err := sanitizeName(in.Name)
	if err != nil {
		return Entry{}, err
	}
	scope, err := normalizeScope(in.Scope)
	if err != nil {
		return Entry{}, err
	}
	typ, err := normalizeType(in.Type)
	if err != nil {
		return Entry{}, err
	}
	description := oneLine(in.Description)
	if description == "" {
		return Entry{}, errors.New("description is required")
	}
	content := strings.TrimSpace(in.Content)
	if content == "" {
		return Entry{}, errors.New("content is required")
	}
	dir, err := s.ScopeDir(scope)
	if err != nil {
		return Entry{}, err
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return Entry{}, fmt.Errorf("create memory dir: %w", err)
	}
	path := filepath.Join(dir, name+".md")
	created := s.today()
	updatedExisting := false
	if existing, err := s.Read(scope, name); err == nil && strings.TrimSpace(existing.Created) != "" {
		created = existing.Created
		updatedExisting = true
	}
	entry := Entry{
		Name:            name,
		Type:            typ,
		Scope:           scope,
		Description:     description,
		Content:         content,
		Created:         created,
		Updated:         s.today(),
		Path:            path,
		UpdatedExisting: updatedExisting,
	}
	if err := os.WriteFile(path, []byte(formatEntry(entry)), defaultFileMode); err != nil {
		return Entry{}, fmt.Errorf("write memory: %w", err)
	}
	if err := s.rebuildIndex(scope); err != nil {
		return Entry{}, err
	}
	return entry, nil
}

func (s *Store) Read(scope, rawName string) (Entry, error) {
	name, err := sanitizeName(rawName)
	if err != nil {
		return Entry{}, err
	}
	scope, err = normalizeScope(scope)
	if err != nil {
		return Entry{}, err
	}
	dir, err := s.ScopeDir(scope)
	if err != nil {
		return Entry{}, err
	}
	path := filepath.Join(dir, name+".md")
	b, err := os.ReadFile(path)
	if err != nil {
		return Entry{}, fmt.Errorf("read memory: %w", err)
	}
	entry := parseEntry(string(b))
	entry.Name = firstNonEmpty(entry.Name, name)
	entry.Scope = firstNonEmpty(entry.Scope, scope)
	entry.Path = path
	return entry, nil
}

func (s *Store) Delete(scope, rawName string) (bool, error) {
	name, err := sanitizeName(rawName)
	if err != nil {
		return false, err
	}
	scope, err = normalizeScope(scope)
	if err != nil {
		return false, err
	}
	dir, err := s.ScopeDir(scope)
	if err != nil {
		return false, err
	}
	path := filepath.Join(dir, name+".md")
	if err := os.Remove(path); err != nil {
		if os.IsNotExist(err) {
			return false, nil
		}
		return false, fmt.Errorf("delete memory: %w", err)
	}
	if err := s.rebuildIndex(scope); err != nil {
		return true, err
	}
	return true, nil
}

func (s *Store) Index(scope string) (string, string, error) {
	path, err := s.IndexPath(scope)
	if err != nil {
		return "", "", err
	}
	b, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return path, "", nil
		}
		return path, "", err
	}
	return path, strings.TrimSpace(string(b)), nil
}

func (s *Store) CappedIndex(scope string, maxBytes int) (string, string, bool, error) {
	path, content, err := s.Index(scope)
	if err != nil {
		return "", "", false, err
	}
	if maxBytes <= 0 {
		maxBytes = indexMaxBytes
	}
	if len(content) <= maxBytes {
		return path, content, false, nil
	}
	truncated := content[:maxBytes]
	if cut := strings.LastIndex(truncated, "\n"); cut > 0 {
		truncated = truncated[:cut]
	}
	truncated = strings.TrimSpace(truncated) + "\n\n> MEMORY.md truncated; use recall_memory for details."
	return path, truncated, true, nil
}

func (s *Store) rebuildIndex(scope string) error {
	scope, err := normalizeScope(scope)
	if err != nil {
		return err
	}
	dir, err := s.ScopeDir(scope)
	if err != nil {
		return err
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return err
	}
	var memories []Entry
	for _, de := range entries {
		if de.IsDir() || de.Name() == IndexFileName || filepath.Ext(de.Name()) != ".md" {
			continue
		}
		name := strings.TrimSuffix(de.Name(), ".md")
		entry, err := s.Read(scope, name)
		if err != nil {
			continue
		}
		memories = append(memories, entry)
	}
	sort.Slice(memories, func(i, j int) bool {
		return memories[i].Name < memories[j].Name
	})
	var lines []string
	for _, e := range memories {
		lines = append(lines, fmt.Sprintf("- [%s](%s.md) - %s", e.Name, e.Name, oneLine(e.Description)))
	}
	index := strings.Join(lines, "\n")
	indexPath := filepath.Join(dir, IndexFileName)
	if index == "" {
		_ = os.Remove(indexPath)
		return nil
	}
	return os.WriteFile(indexPath, []byte(index+"\n"), defaultFileMode)
}

func (s *Store) today() string {
	if s.now == nil {
		return time.Now().Format("2006-01-02")
	}
	return s.now().Format("2006-01-02")
}

func WorkspaceHash(root string) string {
	abs, err := filepath.Abs(strings.TrimSpace(root))
	if err != nil {
		abs = strings.TrimSpace(root)
	}
	sum := sha1.Sum([]byte(filepath.Clean(abs)))
	return hex.EncodeToString(sum[:])[:16]
}

func sanitizeName(raw string) (string, error) {
	name := strings.TrimSpace(raw)
	if !validMemoryName.MatchString(name) || strings.Contains(name, string(filepath.Separator)) {
		return "", fmt.Errorf("invalid memory name: %q", raw)
	}
	return name, nil
}

func normalizeScope(raw string) (string, error) {
	switch strings.ToLower(strings.TrimSpace(raw)) {
	case globalScope:
		return globalScope, nil
	case projectScope:
		return projectScope, nil
	default:
		return "", fmt.Errorf("invalid memory scope: %s", raw)
	}
}

func normalizeType(raw string) (string, error) {
	switch strings.ToLower(strings.TrimSpace(raw)) {
	case "user", "feedback", "project", "reference":
		return strings.ToLower(strings.TrimSpace(raw)), nil
	default:
		return "", fmt.Errorf("invalid memory type: %s", raw)
	}
}

func oneLine(raw string) string {
	return strings.Join(strings.Fields(strings.TrimSpace(raw)), " ")
}

func formatEntry(e Entry) string {
	return strings.Join([]string{
		"---",
		"name: " + e.Name,
		"description: " + e.Description,
		"type: " + e.Type,
		"scope: " + e.Scope,
		"created: " + e.Created,
		"updated: " + e.Updated,
		"---",
		"",
		strings.TrimSpace(e.Content),
		"",
	}, "\n")
}

func parseEntry(raw string) Entry {
	lines := strings.Split(raw, "\n")
	entry := Entry{}
	bodyStart := 0
	if len(lines) > 0 && strings.TrimSpace(lines[0]) == "---" {
		for i := 1; i < len(lines); i++ {
			line := strings.TrimSpace(lines[i])
			if line == "---" {
				bodyStart = i + 1
				break
			}
			k, v, ok := strings.Cut(line, ":")
			if !ok {
				continue
			}
			v = strings.TrimSpace(v)
			switch strings.TrimSpace(k) {
			case "name":
				entry.Name = v
			case "description":
				entry.Description = v
			case "type":
				entry.Type = v
			case "scope":
				entry.Scope = v
			case "created":
				entry.Created = v
			case "updated":
				entry.Updated = v
			}
		}
	}
	entry.Content = strings.TrimSpace(strings.Join(lines[bodyStart:], "\n"))
	return entry
}

func firstNonEmpty(values ...string) string {
	for _, v := range values {
		if strings.TrimSpace(v) != "" {
			return strings.TrimSpace(v)
		}
	}
	return ""
}
