package shell

import (
	"errors"
	"reflect"
	"strings"
	"testing"
)

func TestResolvePOSIXShells(t *testing.T) {
	for _, goos := range []string{"linux", "darwin"} {
		t.Run(goos, func(t *testing.T) {
			spec, err := Resolver{
				GOOS: goos,
				LookPath: func(file string) (string, error) {
					t.Fatalf("LookPath should not be called for %s", goos)
					return "", errors.New("unexpected lookup")
				},
				Env: []string{"ComSpec=C:\\Windows\\System32\\cmd.exe"},
			}.Resolve("printf hi")
			if err != nil {
				t.Fatalf("Resolve returned error: %v", err)
			}
			want := Spec{
				Kind:        KindPOSIX,
				DisplayName: "/bin/sh",
				Bin:         "/bin/sh",
				Args:        []string{"-lc", "printf hi"},
			}
			if !reflect.DeepEqual(spec, want) {
				t.Fatalf("spec = %#v, want %#v", spec, want)
			}
		})
	}
}

func TestResolveWindowsPrefersPwsh(t *testing.T) {
	spec, err := Resolver{
		GOOS: "windows",
		LookPath: func(file string) (string, error) {
			switch file {
			case "pwsh":
				return `C:\Program Files\PowerShell\7\pwsh.exe`, nil
			case "cmd.exe":
				return `C:\Windows\System32\cmd.exe`, nil
			default:
				return "", errors.New("unexpected lookup")
			}
		},
		Env: []string{"ComSpec=C:\\Windows\\System32\\cmd.exe"},
	}.Resolve("Get-ChildItem")
	if err != nil {
		t.Fatalf("Resolve returned error: %v", err)
	}
	if spec.Kind != KindPowerShell {
		t.Fatalf("Kind = %q, want %q", spec.Kind, KindPowerShell)
	}
	if spec.DisplayName != "PowerShell" {
		t.Fatalf("DisplayName = %q, want PowerShell", spec.DisplayName)
	}
	if spec.Bin != `C:\Program Files\PowerShell\7\pwsh.exe` {
		t.Fatalf("Bin = %q", spec.Bin)
	}
	wantArgs := []string{"-NoLogo", "-NoProfile", "-NonInteractive", "-Command", powerShellUTF8OutputPrefix + "& {\nGet-ChildItem\n}"}
	if !reflect.DeepEqual(spec.Args, wantArgs) {
		t.Fatalf("Args = %#v, want %#v", spec.Args, wantArgs)
	}
	for _, arg := range spec.Args {
		if strings.EqualFold(arg, "-ExecutionPolicy") || strings.EqualFold(arg, "Bypass") {
			t.Fatalf("PowerShell args should not include execution policy bypass: %#v", spec.Args)
		}
	}
}

func TestResolveWindowsFallsBackToComSpec(t *testing.T) {
	lookups := []string{}
	spec, err := Resolver{
		GOOS: "windows",
		LookPath: func(file string) (string, error) {
			lookups = append(lookups, file)
			return "", errors.New("not found")
		},
		Env: []string{"ComSpec=C:\\Windows\\System32\\cmd.exe"},
	}.Resolve("dir")
	if err != nil {
		t.Fatalf("Resolve returned error: %v", err)
	}
	if !reflect.DeepEqual(lookups, []string{"pwsh"}) {
		t.Fatalf("lookups = %#v, want pwsh only before ComSpec fallback", lookups)
	}
	want := Spec{
		Kind:        KindCmd,
		DisplayName: "cmd.exe",
		Bin:         `C:\Windows\System32\cmd.exe`,
		Args:        []string{"/d", "/s", "/c", "chcp 65001 >nul & dir"},
	}
	if !reflect.DeepEqual(spec, want) {
		t.Fatalf("spec = %#v, want %#v", spec, want)
	}
}

func TestResolveWindowsFallsBackToCmdExeLookup(t *testing.T) {
	lookups := []string{}
	spec, err := Resolver{
		GOOS: "windows",
		LookPath: func(file string) (string, error) {
			lookups = append(lookups, file)
			if file == "cmd.exe" {
				return `C:\Windows\System32\cmd.exe`, nil
			}
			return "", errors.New("not found")
		},
		Env: []string{},
	}.Resolve("dir")
	if err != nil {
		t.Fatalf("Resolve returned error: %v", err)
	}
	if !reflect.DeepEqual(lookups, []string{"pwsh", "cmd.exe"}) {
		t.Fatalf("lookups = %#v", lookups)
	}
	if spec.Kind != KindCmd || spec.Bin != `C:\Windows\System32\cmd.exe` {
		t.Fatalf("unexpected spec: %#v", spec)
	}
}

func TestResolveWindowsLastResortCmdExe(t *testing.T) {
	spec, err := Resolver{
		GOOS: "windows",
		LookPath: func(file string) (string, error) {
			return "", errors.New("not found")
		},
		Env: []string{},
	}.Resolve("dir")
	if err != nil {
		t.Fatalf("Resolve returned error: %v", err)
	}
	want := Spec{
		Kind:        KindCmd,
		DisplayName: "cmd.exe",
		Bin:         "cmd.exe",
		Args:        []string{"/d", "/s", "/c", "chcp 65001 >nul & dir"},
	}
	if !reflect.DeepEqual(spec, want) {
		t.Fatalf("spec = %#v, want %#v", spec, want)
	}
}

func TestResolveWindowsDoesNotDuplicateUTF8Setup(t *testing.T) {
	t.Run("powershell", func(t *testing.T) {
		command := powerShellUTF8OutputPrefix + "Get-ChildItem"
		spec, err := Resolver{
			GOOS: "windows",
			LookPath: func(file string) (string, error) {
				if file == "pwsh" {
					return `C:\Program Files\PowerShell\7\pwsh.exe`, nil
				}
				return "", errors.New("not found")
			},
		}.Resolve(command)
		if err != nil {
			t.Fatalf("Resolve returned error: %v", err)
		}
		if got := strings.Count(spec.Args[len(spec.Args)-1], powerShellUTF8OutputPrefix); got != 1 {
			t.Fatalf("PowerShell UTF-8 prefix count = %d in %#v", got, spec.Args)
		}
	})

	t.Run("cmd", func(t *testing.T) {
		command := "chcp 65001 >nul & dir"
		spec, err := Resolver{
			GOOS: "windows",
			LookPath: func(file string) (string, error) {
				return "", errors.New("not found")
			},
			Env: []string{"ComSpec=C:\\Windows\\System32\\cmd.exe"},
		}.Resolve(command)
		if err != nil {
			t.Fatalf("Resolve returned error: %v", err)
		}
		if got := strings.Count(strings.ToLower(spec.Args[len(spec.Args)-1]), "chcp 65001"); got != 1 {
			t.Fatalf("cmd UTF-8 codepage setup count = %d in %#v", got, spec.Args)
		}
	})
}

func TestResolveWindowsPowerShellPreservesFirstStatementCommands(t *testing.T) {
	for _, command := range []string{
		"using namespace System.IO; [Path]::GetFileName('C:\\tmp\\marker.txt')",
		"param([string]$Name) $Name",
	} {
		t.Run(command, func(t *testing.T) {
			spec, err := Resolver{
				GOOS: "windows",
				LookPath: func(file string) (string, error) {
					if file == "pwsh" {
						return `C:\Program Files\PowerShell\7\pwsh.exe`, nil
					}
					return "", errors.New("not found")
				},
			}.Resolve(command)
			if err != nil {
				t.Fatalf("Resolve returned error: %v", err)
			}
			if got := spec.Args[len(spec.Args)-1]; got != command {
				t.Fatalf("PowerShell command = %q, want first-statement command unchanged", got)
			}
		})
	}
}
