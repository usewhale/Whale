package skillsimprover

import (
	"bufio"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/usewhale/whale/internal/skills"
)

const (
	PluginID           = "skills-improver"
	evidenceFileName   = "evidence.jsonl"
	maxEvidenceTextLen = 2000
	maxProposalTextLen = 200000
)

type Store struct {
	dataDir       string
	workspaceRoot string
}

type Evidence struct {
	ID                string         `json:"id"`
	CreatedAt         time.Time      `json:"created_at"`
	Kind              string         `json:"kind"`
	SessionID         string         `json:"session_id,omitempty"`
	Turn              int            `json:"turn,omitempty"`
	Skill             string         `json:"skill,omitempty"`
	Prompt            string         `json:"prompt,omitempty"`
	ToolName          string         `json:"tool_name,omitempty"`
	ToolArgsSummary   string         `json:"tool_args_summary,omitempty"`
	ToolResultSummary string         `json:"tool_result_summary,omitempty"`
	AssistantSummary  string         `json:"assistant_summary,omitempty"`
	Metadata          map[string]any `json:"metadata,omitempty"`
}

type Proposal struct {
	ID                 string    `json:"id"`
	CreatedAt          time.Time `json:"created_at"`
	Skill              string    `json:"skill"`
	SkillFilePath      string    `json:"skill_file_path"`
	OriginalSHA256     string    `json:"original_sha256"`
	Summary            string    `json:"summary"`
	Risk               string    `json:"risk,omitempty"`
	ProposedSkillMD    string    `json:"proposed_skill_md"`
	EvidenceIDs        []string  `json:"evidence_ids,omitempty"`
	AppliedAt          time.Time `json:"applied_at,omitempty"`
	AppliedSkillSHA256 string    `json:"applied_skill_sha256,omitempty"`
}

type Status struct {
	EvidenceCount int
	ProposalCount int
	DataDir       string
	Recent        []Evidence
}

func NewStore(dataDir, workspaceRoot string) *Store {
	return &Store{dataDir: strings.TrimSpace(dataDir), workspaceRoot: strings.TrimSpace(workspaceRoot)}
}

func (s *Store) DataDir() string {
	return s.dataDir
}

func (s *Store) AppendEvidence(ev Evidence) (Evidence, error) {
	if strings.TrimSpace(s.dataDir) == "" {
		return Evidence{}, fmt.Errorf("skills-improver data dir is empty")
	}
	if err := os.MkdirAll(s.dataDir, 0o700); err != nil {
		return Evidence{}, fmt.Errorf("create skills-improver data dir: %w", err)
	}
	ev.CreatedAt = time.Now().UTC()
	ev.Kind = cleanToken(ev.Kind)
	ev.Skill = cleanSkillName(ev.Skill)
	ev.Prompt = truncate(ev.Prompt, maxEvidenceTextLen)
	ev.ToolArgsSummary = truncate(ev.ToolArgsSummary, maxEvidenceTextLen)
	ev.ToolResultSummary = truncate(ev.ToolResultSummary, maxEvidenceTextLen)
	ev.AssistantSummary = truncate(ev.AssistantSummary, maxEvidenceTextLen)
	if ev.ID == "" {
		ev.ID = evidenceID(ev)
	}
	b, err := json.Marshal(ev)
	if err != nil {
		return Evidence{}, err
	}
	f, err := os.OpenFile(s.evidencePath(), os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600)
	if err != nil {
		return Evidence{}, fmt.Errorf("open evidence log: %w", err)
	}
	defer f.Close()
	if _, err := f.Write(append(b, '\n')); err != nil {
		return Evidence{}, fmt.Errorf("write evidence log: %w", err)
	}
	return ev, nil
}

func (s *Store) ListEvidence(skill string, limit int) ([]Evidence, error) {
	if limit <= 0 {
		limit = 20
	}
	all, err := s.readEvidence()
	if err != nil {
		return nil, err
	}
	skill = cleanSkillName(skill)
	filtered := make([]Evidence, 0, len(all))
	for _, ev := range all {
		if skill == "" || ev.Skill == skill || strings.Contains(strings.ToLower(ev.Prompt), strings.ToLower("$"+skill)) {
			filtered = append(filtered, ev)
		}
	}
	sort.SliceStable(filtered, func(i, j int) bool { return filtered[i].CreatedAt.After(filtered[j].CreatedAt) })
	if len(filtered) > limit {
		filtered = filtered[:limit]
	}
	return filtered, nil
}

func (s *Store) Status() (Status, error) {
	all, err := s.readEvidence()
	if err != nil {
		return Status{}, err
	}
	proposals, err := s.ListProposals()
	if err != nil {
		return Status{}, err
	}
	recent := append([]Evidence(nil), all...)
	sort.SliceStable(recent, func(i, j int) bool { return recent[i].CreatedAt.After(recent[j].CreatedAt) })
	if len(recent) > 5 {
		recent = recent[:5]
	}
	return Status{EvidenceCount: len(all), ProposalCount: len(proposals), DataDir: s.dataDir, Recent: recent}, nil
}

func (s *Store) LatestUnsummarizedSessionEvidence(sessionID string) (Evidence, bool) {
	sessionID = strings.TrimSpace(sessionID)
	if sessionID == "" {
		return Evidence{}, false
	}
	all, err := s.readEvidence()
	if err != nil {
		return Evidence{}, false
	}
	for i := len(all) - 1; i >= 0; i-- {
		if all[i].SessionID != sessionID {
			continue
		}
		if all[i].Kind == "turn-summary" {
			return Evidence{}, false
		}
		if all[i].Kind != "" {
			return all[i], true
		}
	}
	return Evidence{}, false
}

func (s *Store) HasUnsummarizedSessionEvidence(sessionID string) bool {
	_, ok := s.LatestUnsummarizedSessionEvidence(sessionID)
	return ok
}

func (s *Store) SaveProposal(p Proposal) (Proposal, error) {
	if strings.TrimSpace(s.dataDir) == "" {
		return Proposal{}, fmt.Errorf("skills-improver data dir is empty")
	}
	p.ID = ""
	p.Skill = strings.TrimSpace(p.Skill)
	p.SkillFilePath = strings.TrimSpace(p.SkillFilePath)
	p.Summary = truncate(p.Summary, maxEvidenceTextLen)
	p.Risk = truncate(p.Risk, maxEvidenceTextLen)
	p.ProposedSkillMD = strings.TrimSpace(p.ProposedSkillMD)
	if p.Skill == "" {
		return Proposal{}, fmt.Errorf("skill is required")
	}
	if p.SkillFilePath == "" {
		return Proposal{}, fmt.Errorf("skill_file_path is required")
	}
	if strings.HasPrefix(p.SkillFilePath, "plugin://") {
		return Proposal{}, fmt.Errorf("plugin skills cannot be applied in phase 1")
	}
	if len(p.ProposedSkillMD) == 0 {
		return Proposal{}, fmt.Errorf("proposed_skill_md is required")
	}
	if len(p.ProposedSkillMD) > maxProposalTextLen {
		return Proposal{}, fmt.Errorf("proposed_skill_md is too large")
	}
	if err := validateSkillContent(p.Skill, p.ProposedSkillMD); err != nil {
		return Proposal{}, err
	}
	abs, err := filepath.Abs(p.SkillFilePath)
	if err != nil {
		return Proposal{}, err
	}
	if err := requireKnownSkillPath(s.workspaceRoot, p.Skill, abs); err != nil {
		return Proposal{}, err
	}
	current, err := os.ReadFile(abs)
	if err != nil {
		return Proposal{}, fmt.Errorf("read target skill: %w", err)
	}
	currentSHA := sha256Hex(current)
	if strings.TrimSpace(p.OriginalSHA256) == "" {
		return Proposal{}, fmt.Errorf("original_sha256 is required")
	}
	if p.OriginalSHA256 != currentSHA {
		return Proposal{}, fmt.Errorf("target skill changed: sha mismatch")
	}
	p.SkillFilePath = abs
	p.CreatedAt = time.Now().UTC()
	p.ID = proposalID(p)
	if err := os.MkdirAll(s.proposalsDir(), 0o700); err != nil {
		return Proposal{}, fmt.Errorf("create proposals dir: %w", err)
	}
	b, err := json.MarshalIndent(p, "", "  ")
	if err != nil {
		return Proposal{}, err
	}
	path := filepath.Join(s.proposalsDir(), p.ID+".json")
	if err := os.WriteFile(path, append(b, '\n'), 0o600); err != nil {
		return Proposal{}, fmt.Errorf("write proposal: %w", err)
	}
	return p, nil
}

func (s *Store) ListProposals() ([]Proposal, error) {
	dir := s.proposalsDir()
	entries, err := os.ReadDir(dir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	out := make([]Proposal, 0, len(entries))
	for _, ent := range entries {
		if ent.IsDir() || !strings.HasSuffix(ent.Name(), ".json") {
			continue
		}
		p, err := s.ReadProposal(strings.TrimSuffix(ent.Name(), ".json"))
		if err == nil {
			out = append(out, p)
		}
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].CreatedAt.After(out[j].CreatedAt) })
	return out, nil
}

func (s *Store) ReadProposal(id string) (Proposal, error) {
	id = cleanID(id)
	if id == "" {
		return Proposal{}, fmt.Errorf("proposal id is required")
	}
	b, err := os.ReadFile(filepath.Join(s.proposalsDir(), id+".json"))
	if err != nil {
		return Proposal{}, err
	}
	var p Proposal
	if err := json.Unmarshal(b, &p); err != nil {
		return Proposal{}, err
	}
	return p, nil
}

func (s *Store) ApplyProposal(id string) (Proposal, error) {
	p, err := s.ReadProposal(id)
	if err != nil {
		return Proposal{}, err
	}
	if !p.AppliedAt.IsZero() {
		return Proposal{}, fmt.Errorf("proposal already applied")
	}
	if strings.HasPrefix(p.SkillFilePath, "plugin://") {
		return Proposal{}, fmt.Errorf("plugin skills cannot be applied in phase 1")
	}
	if err := validateSkillContent(p.Skill, p.ProposedSkillMD); err != nil {
		return Proposal{}, err
	}
	if err := requireKnownSkillPath(s.workspaceRoot, p.Skill, p.SkillFilePath); err != nil {
		return Proposal{}, err
	}
	current, err := os.ReadFile(p.SkillFilePath)
	if err != nil {
		return Proposal{}, fmt.Errorf("read target skill: %w", err)
	}
	if sha256Hex(current) != p.OriginalSHA256 {
		return Proposal{}, fmt.Errorf("target skill changed: sha mismatch")
	}
	if err := os.WriteFile(p.SkillFilePath, []byte(strings.TrimSpace(p.ProposedSkillMD)+"\n"), 0o600); err != nil {
		return Proposal{}, fmt.Errorf("write target skill: %w", err)
	}
	p.AppliedAt = time.Now().UTC()
	p.AppliedSkillSHA256 = sha256Hex([]byte(strings.TrimSpace(p.ProposedSkillMD) + "\n"))
	b, err := json.MarshalIndent(p, "", "  ")
	if err != nil {
		return Proposal{}, err
	}
	if err := os.WriteFile(filepath.Join(s.proposalsDir(), p.ID+".json"), append(b, '\n'), 0o600); err != nil {
		return Proposal{}, fmt.Errorf("mark proposal applied: %w", err)
	}
	return p, nil
}

func (s *Store) readEvidence() ([]Evidence, error) {
	f, err := os.Open(s.evidencePath())
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	defer f.Close()
	var out []Evidence
	scanner := bufio.NewScanner(f)
	scanner.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	for scanner.Scan() {
		var ev Evidence
		if err := json.Unmarshal(scanner.Bytes(), &ev); err == nil {
			out = append(out, ev)
		}
	}
	if err := scanner.Err(); err != nil {
		return nil, err
	}
	return out, nil
}

func (s *Store) evidencePath() string {
	return filepath.Join(s.dataDir, evidenceFileName)
}

func (s *Store) proposalsDir() string {
	return filepath.Join(s.dataDir, "proposals")
}

func validateSkillContent(expectedName, content string) error {
	s, err := skills.ParseContent([]byte(content))
	if err != nil {
		return fmt.Errorf("invalid proposed SKILL.md: %w", err)
	}
	if err := s.Validate(); err != nil {
		return fmt.Errorf("invalid proposed SKILL.md: %w", err)
	}
	if cleanSkillName(s.Name) != cleanSkillName(expectedName) {
		return fmt.Errorf("proposed skill name mismatch: got %s want %s", s.Name, expectedName)
	}
	return nil
}

func requireKnownSkillPath(workspaceRoot, skillName, path string) error {
	abs, err := normalizedExistingPath(path)
	if err != nil {
		return err
	}
	roots := skills.DefaultRoots(workspaceRoot)
	found, ok := findSkillCaseInsensitive(roots, skillName)
	if !ok {
		return fmt.Errorf("skill not found in filesystem roots: %s", skillName)
	}
	foundPath, err := normalizedExistingPath(found.SkillFilePath)
	if err != nil {
		return err
	}
	if abs != foundPath {
		return fmt.Errorf("target path does not match discovered skill path")
	}
	return nil
}

func findSkillCaseInsensitive(roots []string, skillName string) (*skills.Skill, bool) {
	if found, _, ok := skills.Find(roots, skillName); ok {
		return found, true
	}
	target := strings.EqualFold
	for _, skill := range skills.Discover(roots) {
		if skill != nil && target(skill.Name, skillName) {
			return skill, true
		}
	}
	return nil, false
}

func normalizedExistingPath(path string) (string, error) {
	abs, err := filepath.Abs(path)
	if err != nil {
		return "", err
	}
	resolved, err := filepath.EvalSymlinks(abs)
	if err == nil {
		abs = resolved
	}
	return filepath.Clean(abs), nil
}

func evidenceID(ev Evidence) string {
	h := sha256.New()
	_, _ = fmt.Fprintf(h, "%s\n%s\n%s\n%s\n%d\n", ev.CreatedAt.Format(time.RFC3339Nano), ev.Kind, ev.SessionID, ev.Skill, ev.Turn)
	return "ev-" + hex.EncodeToString(h.Sum(nil))[:12]
}

func proposalID(p Proposal) string {
	h := sha256.New()
	_, _ = fmt.Fprintf(h, "%s\n%s\n%s\n%s\n", p.CreatedAt.Format(time.RFC3339Nano), p.Skill, p.SkillFilePath, p.Summary)
	return "sp-" + hex.EncodeToString(h.Sum(nil))[:12]
}

func sha256Hex(b []byte) string {
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:])
}

func cleanSkillName(v string) string {
	return strings.ToLower(strings.TrimSpace(v))
}

func cleanToken(v string) string {
	v = strings.ToLower(strings.TrimSpace(v))
	v = strings.Map(func(r rune) rune {
		if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') || r == '-' || r == '_' {
			return r
		}
		return '-'
	}, v)
	return strings.Trim(v, "-_")
}

func cleanID(v string) string {
	return cleanToken(v)
}

func truncate(v string, max int) string {
	v = strings.TrimSpace(v)
	if max <= 0 || len([]rune(v)) <= max {
		return v
	}
	r := []rune(v)
	return string(r[:max]) + "...[truncated]"
}
