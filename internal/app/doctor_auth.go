package app

import (
	"fmt"
	"strings"
)

func doctorCheckAPIKey(dataDir string) (DoctorCheck, apiKeySource, string) {
	key, source, err := resolveDeepSeekAPIKey(dataDir)
	if err != nil {
		return DoctorCheck{
			Label:  "api key",
			Level:  DoctorFail,
			Detail: err.Error(),
		}, apiKeySourceMissing, ""
	}
	if strings.TrimSpace(key) == "" {
		return DoctorCheck{
			Label:  "api key",
			Level:  DoctorFail,
			Detail: "not configured — run `whale setup` or set `DEEPSEEK_API_KEY`",
		}, apiKeySourceMissing, ""
	}
	switch source {
	case apiKeySourceEnv:
		return DoctorCheck{
			Label:  "api key",
			Level:  DoctorOK,
			Detail: fmt.Sprintf("set via env DEEPSEEK_API_KEY (%s)", tailKey(key)),
		}, source, key
	case apiKeySourceCredentials:
		return DoctorCheck{
			Label:  "api key",
			Level:  DoctorOK,
			Detail: fmt.Sprintf("from %s (%s)", credentialsPath(dataDir), tailKey(key)),
		}, source, key
	default:
		return DoctorCheck{
			Label:  "api key",
			Level:  DoctorFail,
			Detail: "not configured — run `whale setup` or set `DEEPSEEK_API_KEY`",
		}, apiKeySourceMissing, ""
	}
}

func doctorCheckCredentials(dataDir string) DoctorCheck {
	st := readCredentialsState(dataDir)
	switch {
	case st.Err != nil:
		return DoctorCheck{
			Label:  "credentials",
			Level:  DoctorFail,
			Detail: fmt.Sprintf("%s unreadable — %v", st.Path, st.Err),
		}
	case !st.Present:
		return DoctorCheck{
			Label:  "credentials",
			Level:  DoctorWarn,
			Detail: fmt.Sprintf("%s missing — `whale setup` writes one", st.Path),
		}
	default:
		return DoctorCheck{
			Label:  "credentials",
			Level:  DoctorOK,
			Detail: st.Path,
		}
	}
}
