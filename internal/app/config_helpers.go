package app

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
)

func trimList(in []string) []string {
	out := make([]string, 0, len(in))
	for _, v := range in {
		if v = strings.TrimSpace(v); v != "" {
			out = append(out, v)
		}
	}
	return out
}

func expandUserPath(path string) string {
	path = strings.TrimSpace(path)
	if runtime.GOOS == "windows" {
		path = expandWindowsPercentEnv(path)
	}
	if path == "~" {
		if home, err := os.UserHomeDir(); err == nil && home != "" {
			return home
		}
	}
	if strings.HasPrefix(path, "~/") {
		if home, err := os.UserHomeDir(); err == nil && home != "" {
			return filepath.Join(home, strings.TrimPrefix(path, "~/"))
		}
	}
	return path
}

func expandWindowsPercentEnv(path string) string {
	var out strings.Builder
	for i := 0; i < len(path); {
		if path[i] != '%' {
			out.WriteByte(path[i])
			i++
			continue
		}
		end := strings.IndexByte(path[i+1:], '%')
		if end < 0 {
			out.WriteByte(path[i])
			i++
			continue
		}
		end += i + 1
		name := path[i+1 : end]
		if name == "" {
			out.WriteString("%%")
			i = end + 1
			continue
		}
		if value, ok := os.LookupEnv(name); ok {
			out.WriteString(value)
		} else {
			out.WriteString(path[i : end+1])
		}
		i = end + 1
	}
	return out.String()
}
