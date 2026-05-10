package shell

import (
	"errors"
	"reflect"
	"strings"
	"testing"
)

func TestResolveWindowsPrefersPwsh(t *testing.T) {
	spec, err := Resolver{
		GOOS: "windows",
		LookPath: func(file string) (string, error) {
			switch file {
			case "pwsh":
				return `C:\Program Files\PowerShell\7\pwsh.exe`, nil
			case "powershell.exe":
				return `C:\Windows\System32\WindowsPowerShell\v1.0\powershell.exe`, nil
			default:
				return "", errors.New("unexpected lookup")
			}
		},
	}.Resolve("Get-ChildItem")
	if err != nil {
		t.Fatalf("Resolve returned error: %v", err)
	}

	wantArgs := []string{
		"-NoLogo",
		"-NoProfile",
		"-NonInteractive",
		"-ExecutionPolicy",
		"Bypass",
		"-Command",
		"Get-ChildItem",
	}
	if spec.Name != "pwsh" {
		t.Fatalf("Name = %q, want pwsh", spec.Name)
	}
	if spec.DisplayName != "PowerShell" {
		t.Fatalf("DisplayName = %q, want PowerShell", spec.DisplayName)
	}
	if spec.Bin != `C:\Program Files\PowerShell\7\pwsh.exe` {
		t.Fatalf("Bin = %q", spec.Bin)
	}
	if !reflect.DeepEqual(spec.Args, wantArgs) {
		t.Fatalf("Args = %#v, want %#v", spec.Args, wantArgs)
	}
}

func TestResolveWindowsFallsBackToWindowsPowerShell(t *testing.T) {
	lookups := []string{}
	spec, err := Resolver{
		GOOS: "windows",
		LookPath: func(file string) (string, error) {
			lookups = append(lookups, file)
			if file == "powershell.exe" {
				return `C:\Windows\System32\WindowsPowerShell\v1.0\powershell.exe`, nil
			}
			return "", errors.New("not found")
		},
	}.Resolve("Write-Output hi")
	if err != nil {
		t.Fatalf("Resolve returned error: %v", err)
	}

	if !reflect.DeepEqual(lookups, []string{"pwsh", "powershell.exe"}) {
		t.Fatalf("lookups = %#v", lookups)
	}
	if spec.Name != "powershell.exe" {
		t.Fatalf("Name = %q, want powershell.exe", spec.Name)
	}
	if spec.Bin != `C:\Windows\System32\WindowsPowerShell\v1.0\powershell.exe` {
		t.Fatalf("Bin = %q", spec.Bin)
	}
	if got := spec.Args[len(spec.Args)-1]; got != "Write-Output hi" {
		t.Fatalf("command arg = %q", got)
	}
}

func TestResolveWindowsRequiresPowerShell(t *testing.T) {
	lookups := []string{}
	_, err := Resolver{
		GOOS: "windows",
		LookPath: func(file string) (string, error) {
			lookups = append(lookups, file)
			return "", errors.New("not found")
		},
	}.Resolve("Write-Output hi")
	if err == nil {
		t.Fatal("Resolve returned nil error")
	}

	if !reflect.DeepEqual(lookups, []string{"pwsh", "powershell.exe"}) {
		t.Fatalf("lookups = %#v", lookups)
	}
	if !strings.Contains(err.Error(), "PowerShell is required") {
		t.Fatalf("error = %q, want PowerShell is required", err.Error())
	}
}

func TestResolveUnixShells(t *testing.T) {
	for _, goos := range []string{"linux", "darwin"} {
		t.Run(goos, func(t *testing.T) {
			spec, err := Resolver{
				GOOS: goos,
				LookPath: func(file string) (string, error) {
					t.Fatalf("LookPath should not be called for %s", goos)
					return "", errors.New("unexpected lookup")
				},
			}.Resolve("printf hi")
			if err != nil {
				t.Fatalf("Resolve returned error: %v", err)
			}
			want := ShellSpec{
				Name:        "sh",
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

func TestShellDisplayNameForGOOS(t *testing.T) {
	tests := []struct {
		goos string
		want string
	}{
		{goos: "windows", want: "PowerShell"},
		{goos: "linux", want: "/bin/sh"},
		{goos: "darwin", want: "/bin/sh"},
	}

	for _, tt := range tests {
		t.Run(tt.goos, func(t *testing.T) {
			if got := ShellDisplayNameForGOOS(tt.goos); got != tt.want {
				t.Fatalf("ShellDisplayNameForGOOS(%q) = %q, want %q", tt.goos, got, tt.want)
			}
		})
	}
}
