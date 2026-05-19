package main

import (
	"bytes"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
)

const versionPackage = "github.com/usewhale/whale/internal/build.Version"

type devEnv struct {
	root       string
	bin        string
	goCacheDir string
	ldflags    string
}

func main() {
	if err := run(os.Args[1:]); err != nil {
		fmt.Fprintln(os.Stderr, "dev:", err)
		os.Exit(1)
	}
}

func run(args []string) error {
	if len(args) == 0 {
		printHelp()
		return nil
	}

	env, err := newDevEnv()
	if err != nil {
		return err
	}

	cmd := args[0]
	rest := args[1:]
	switch cmd {
	case "help", "-h", "--help":
		printHelp()
	case "build":
		return env.build()
	case "fmt-check":
		return env.fmtCheck()
	case "vet":
		return env.runGo("go", "vet", "./...")
	case "test":
		return env.runGo("go", "test", "./...")
	case "test-tui":
		return env.runGo("go", "test", "./internal/tui", "./internal/tui/render")
	case "test-evals":
		return env.runGo("go", "test", "./internal/evals")
	case "test-windows":
		return env.testWindows()
	case "run":
		if err := env.build(); err != nil {
			return err
		}
		return env.runBinary(rest)
	case "clean":
		return env.clean()
	default:
		return fmt.Errorf("unknown command %q", cmd)
	}
	return nil
}

func newDevEnv() (devEnv, error) {
	root, err := os.Getwd()
	if err != nil {
		return devEnv{}, err
	}
	version := envOrDefault("VERSION", "dev")
	ldflags := os.Getenv("LDFLAGS")
	if strings.TrimSpace(ldflags) == "" {
		ldflags = "-X " + versionPackage + "=" + version
	}
	return devEnv{
		root:       root,
		bin:        envOrDefault("BIN", defaultBinPath(runtime.GOOS)),
		goCacheDir: envOrDefault("GOCACHE_DIR", filepath.Join(root, ".gocache")),
		ldflags:    ldflags,
	}, nil
}

func envOrDefault(name, fallback string) string {
	if value := strings.TrimSpace(os.Getenv(name)); value != "" {
		return value
	}
	return fallback
}

func defaultBinPath(goos string) string {
	name := "whale"
	if goos == "windows" {
		name += ".exe"
	}
	return filepath.Join("bin", name)
}

func printHelp() {
	fmt.Println(strings.TrimSpace(`Whale dev runner

Usage:
  go run ./cmd/dev <command>

Commands:
  build         Build Whale into BIN
  fmt-check     Check Go formatting
  vet           Run go vet ./...
  test          Run all offline Go tests
  test-tui      Run the TUI-focused test subset
  test-evals    Run the eval-focused test subset
  test-windows  Run the supported Windows CI test subset
  run           Build and run Whale
  clean         Remove build output and repo-local Go cache

Environment:
  BIN           Output binary path (default: bin/whale, bin/whale.exe on Windows)
  GOCACHE_DIR   Go build cache directory (default: .gocache)
  VERSION       Version injected into the binary (default: dev)
  LDFLAGS       Override linker flags`))
}

func (d devEnv) build() error {
	if err := os.MkdirAll(filepath.Dir(d.bin), 0o755); err != nil {
		return err
	}
	if err := os.MkdirAll(d.goCacheDir, 0o755); err != nil {
		return err
	}
	return d.runGo("go", "build", "-ldflags", d.ldflags, "-o", d.bin, "./cmd/whale")
}

func (d devEnv) fmtCheck() error {
	files, err := goFiles(d.root)
	if err != nil {
		return err
	}
	if len(files) == 0 {
		return nil
	}
	cmd := exec.Command("gofmt", append([]string{"-l"}, files...)...)
	cmd.Dir = d.root
	cmd.Stderr = os.Stderr
	var out bytes.Buffer
	cmd.Stdout = &out
	if err := cmd.Run(); err != nil {
		return err
	}
	formatted := strings.TrimSpace(out.String())
	if formatted == "" {
		return nil
	}
	fmt.Fprintln(os.Stderr, "gofmt needs to be run on:")
	fmt.Fprintln(os.Stderr, formatted)
	return errors.New("formatting check failed")
}

func goFiles(root string) ([]string, error) {
	var files []string
	err := filepath.WalkDir(root, func(path string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if entry.IsDir() {
			switch entry.Name() {
			case ".git", ".gocache", "bin":
				return filepath.SkipDir
			}
			return nil
		}
		if filepath.Ext(path) != ".go" {
			return nil
		}
		rel, err := filepath.Rel(root, path)
		if err != nil {
			return err
		}
		files = append(files, rel)
		return nil
	})
	sort.Strings(files)
	return files, err
}

func (d devEnv) testWindows() error {
	steps := [][]string{
		{"go", "test", "./internal/tui", "./internal/tui/render", "-count=1"},
		{"go", "test", "./internal/shell", "-count=1"},
		{"go", "test", "./internal/tools", "-run", "Test(ReadFileNormalizesCRLFContent|EditFileMatchesLFSearchAndPreservesCRLF|EditFilePreservesMixedLineEndings|ApplyPatchMatchesLFHunksAndPreservesCRLF|ApplyPatchPreservesMixedLineEndings|WindowsShellRunForegroundAndBackground|WindowsShellRunCancelKillsProcessTree|WindowsShellRunKeepsLaunchedChildOnSuccess)$", "-count=1"},
		{"go", "test", "./internal/agent", "-run", "TestWindowsHook", "-count=1"},
		{"go", "test", "./internal/ui/cli", "-count=1"},
	}
	for _, step := range steps {
		if err := d.runGo(step[0], step[1:]...); err != nil {
			return err
		}
	}
	return d.build()
}

func (d devEnv) clean() error {
	if err := os.RemoveAll(filepath.Join(d.root, "bin")); err != nil {
		return err
	}
	cacheDir, err := filepath.Abs(d.goCacheDir)
	if err != nil {
		return err
	}
	root, err := filepath.Abs(d.root)
	if err != nil {
		return err
	}
	if !pathWithin(root, cacheDir) {
		fmt.Fprintf(os.Stderr, "Skipping GOCACHE_DIR outside repo: %s\n", d.goCacheDir)
		return nil
	}
	if err := os.RemoveAll(cacheDir); err != nil {
		return err
	}
	return nil
}

func pathWithin(root, path string) bool {
	rel, err := filepath.Rel(root, path)
	if err != nil {
		return false
	}
	return rel == "." || (!strings.HasPrefix(rel, "..") && !filepath.IsAbs(rel))
}

func (d devEnv) runGo(name string, args ...string) error {
	if err := os.MkdirAll(d.goCacheDir, 0o755); err != nil {
		return err
	}
	return d.runCmd(name, args...)
}

func (d devEnv) runBinary(args []string) error {
	path := d.bin
	if !filepath.IsAbs(path) {
		path = filepath.Join(d.root, path)
	}
	return d.runCmd(path, args...)
}

func (d devEnv) runCmd(name string, args ...string) error {
	cmd := exec.Command(name, args...)
	cmd.Dir = d.root
	cmd.Env = appendEnv(os.Environ(), "GOCACHE", d.goCacheDir)
	cmd.Stdin = os.Stdin
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	return cmd.Run()
}

func appendEnv(env []string, key, value string) []string {
	prefix := key + "="
	out := make([]string, 0, len(env)+1)
	replaced := false
	for _, entry := range env {
		if strings.HasPrefix(entry, prefix) {
			out = append(out, prefix+value)
			replaced = true
			continue
		}
		out = append(out, entry)
	}
	if !replaced {
		out = append(out, prefix+value)
	}
	return out
}
