package execboundary

type DecisionRequest struct {
	Program string   `json:"program"`
	Argv    []string `json:"argv"`
	CWD     string   `json:"cwd"`
}

type DecisionResponse struct {
	Allow            bool   `json:"allow"`
	RequiresApproval bool   `json:"requires_approval"`
	Reason           string `json:"reason,omitempty"`
	Code             string `json:"code,omitempty"`
	Phase            string `json:"phase,omitempty"`
	MatchedRule      string `json:"matched_rule,omitempty"`
	Permission       string `json:"permission,omitempty"`
	Pattern          string `json:"pattern,omitempty"`
}
