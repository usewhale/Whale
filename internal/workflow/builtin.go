package workflow

import _ "embed"

const BuiltinDeepResearchName = "deep-research"

var builtinWorkflowScripts = map[string]string{
	BuiltinDeepResearchName: deepResearchWorkflowScript,
}

func BuiltinWorkflowScript(name string) (string, bool) {
	script, ok := builtinWorkflowScripts[name]
	return script, ok
}

func builtinWorkflowDefinitions() []Definition {
	defs := make([]Definition, 0, len(builtinWorkflowScripts))
	for name, script := range builtinWorkflowScripts {
		def := inspectWorkflowScript("builtin", name+WorkflowFileExt, script)
		def.Path = "builtin:" + name
		def.Root = "builtin"
		def.Source = "builtin"
		defs = append(defs, def)
	}
	return defs
}

//go:embed testdata/claude_code_deep_research.js
var deepResearchWorkflowScript string
