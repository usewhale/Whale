package app

type LocalResult struct {
	Kind                  string
	Title                 string
	Fields                []LocalResultField
	Sections              []LocalResultSection
	Actions               []LocalResultAction
	PlainText             string
	WorkflowPanelSnapshot *WorkflowPanelSnapshot
}

type LocalResultSection struct {
	Title  string
	Fields []LocalResultField
}

type LocalResultField struct {
	Label string
	Value string
	Tone  string
}

type LocalResultAction struct {
	Label          string
	Description    string
	Command        string
	Tone           string
	WorkflowName   string
	WorkflowArgs   string
	WorkflowResume string
	WorkflowTrust  bool
}
