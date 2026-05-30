package app

import (
	"fmt"
	"github.com/usewhale/whale/internal/securefs"
	"github.com/usewhale/whale/internal/store"
	"os"
	"path/filepath"
	"strings"
)

func doctorCheckDataDir(dataDir string) DoctorCheck {
	sessionsDir := store.DefaultSessionsDir(dataDir)
	if err := securefs.MkdirPrivate(dataDir); err != nil {
		return DoctorCheck{
			Label:  "data dir",
			Level:  DoctorFail,
			Detail: fmt.Sprintf("%s create failed — %v", dataDir, err),
		}
	}
	if err := securefs.MkdirPrivate(sessionsDir); err != nil {
		return DoctorCheck{
			Label:  "data dir",
			Level:  DoctorFail,
			Detail: fmt.Sprintf("%s create failed — %v", sessionsDir, err),
		}
	}
	probe, err := os.CreateTemp(dataDir, ".doctor-probe-*")
	if err != nil {
		return DoctorCheck{
			Label:  "data dir",
			Level:  DoctorFail,
			Detail: fmt.Sprintf("%s not writable — %v", dataDir, err),
		}
	}
	probePath := probe.Name()
	_ = probe.Close()
	_ = os.Remove(probePath)
	return DoctorCheck{
		Label:  "data dir",
		Level:  DoctorOK,
		Detail: fmt.Sprintf("%s writable · sessions %s", dataDir, sessionsDir),
	}
}

func doctorCheckDataDirOverride(goos string, getenv func(string) string, dataDir string) DoctorCheck {
	whaleHome := strings.TrimSpace(getenv(store.DataDirEnv))
	if whaleHome != "" {
		detail := fmt.Sprintf("using %s=%s", store.DataDirEnv, whaleHome)
		if strings.TrimSpace(dataDir) != "" && filepath.Clean(whaleHome) != filepath.Clean(dataDir) {
			detail = fmt.Sprintf("%s is set; current data dir is %s", store.DataDirEnv, dataDir)
		}
		return DoctorCheck{
			Label:  "data dir override",
			Level:  DoctorOK,
			Detail: detail,
		}
	}
	if goos == "windows" {
		return DoctorCheck{
			Label:  "data dir override",
			Level:  DoctorOK,
			Detail: fmt.Sprintf("set %s to use a custom Whale data directory", store.DataDirEnv),
		}
	}
	return DoctorCheck{}
}

func doctorCheckDataDirACL(goos, dataDir string) DoctorCheck {
	if goos != "windows" {
		return DoctorCheck{}
	}
	status := securefs.CheckPrivatePath(dataDir)
	level := DoctorOK
	if !status.Protected {
		level = DoctorWarn
	}
	return DoctorCheck{
		Label:  "data dir acl",
		Level:  level,
		Detail: status.Detail,
	}
}
