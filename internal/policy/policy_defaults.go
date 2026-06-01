package policy

import (
	"fmt"
)

func DefaultRules() []PermissionRule {
	rules, err := RulesFromConfig(DefaultPermissionConfig())
	if err != nil {
		panic(fmt.Sprintf("invalid default permission config: %v", err))
	}
	return rules
}
func DefaultPermissionConfig() PermissionConfig {
	return PermissionConfig{
		Read: map[string]string{
			"*":             "allow",
			"*.env":         "ask",
			"*.env.*":       "ask",
			"*.env.example": "allow",
		},
		Edit: map[string]string{
			"*": "allow",
		},
		Shell: map[string]string{
			"*":                       "allow",
			"rm *":                    "ask",
			"rm -r*":                  "deny",
			"rm -R*":                  "deny",
			"rm -f -r*":               "deny",
			"rm -r -f*":               "deny",
			"rm -fr*":                 "deny",
			"rm -rf*":                 "deny",
			"rm --recursive*":         "deny",
			"rm --force -r*":          "deny",
			"rm --force -R*":          "deny",
			"rm --force --recursive*": "deny",
			"rm --recursive --force*": "deny",
			"curl *":                  "ask",
			"wget *":                  "ask",
			"npm install*":            "ask",
			"pnpm install*":           "ask",
			"yarn add*":               "ask",
			"git reset*":              "ask",
			"git checkout -- *":       "ask",
			"git restore*":            "ask",
			"git rm*":                 "ask",
			"git clean*":              "ask",
			"git push*":               "ask",
			"gh pr merge*":            "ask",
			"sudo *":                  "ask",
			"dd *":                    "ask",
			"mkfs*":                   "deny",
			"diskutil erase*":         "deny",
		},
		ExternalDirectory: map[string]string{
			"*": "ask",
		},
		MCP: map[string]string{
			"*": "ask",
		},
		Memory: map[string]string{
			"*": "ask",
		},
		MutatingTool: map[string]string{
			"*": "ask",
		},
	}
}
