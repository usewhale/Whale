//go:build windows

package shell

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"strconv"
	"sync"
	"syscall"
	"time"
	"unsafe"

	"golang.org/x/sys/windows"
)

// ConfigureCommand applies platform process settings for shell commands.
func ConfigureCommand(cmd *exec.Cmd) {
	cmd.WaitDelay = 2 * time.Second
}

func RunCommand(ctx context.Context, cmd *exec.Cmd) error {
	if ctx == nil {
		ctx = context.Background()
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	ConfigureCommand(cmd)
	cancel := func() error {
		return killCommandTree(cmd)
	}
	job, jobErr := createCommandJob()
	if err := cmd.Start(); err != nil {
		if jobErr == nil {
			closeJob(job)
		}
		return err
	}
	if jobErr == nil {
		if err := assignProcessToJob(job, cmd.Process.Pid); err == nil {
			// Keep cancellation explicit; normal completion should not terminate job members.
			cancel = func() error {
				return cancelCommandTreeAndJob(cmd, job)
			}
		} else {
			closeJob(job)
			jobErr = err
		}
	}
	if jobErr == nil {
		defer closeJob(job)
	}
	return waitCommandContext(ctx, cmd, cancel)
}

type CommandCleanup struct {
	cmd  *exec.Cmd
	job  syscall.Handle
	once sync.Once
	err  error
}

func AttachCommandCleanup(cmd *exec.Cmd) *CommandCleanup {
	cleanup := &CommandCleanup{cmd: cmd}
	job, err := createCommandJob()
	if err != nil {
		return cleanup
	}
	if cmd != nil && cmd.Process != nil {
		if err := assignProcessToJob(job, cmd.Process.Pid); err == nil {
			cleanup.job = job
			return cleanup
		}
	}
	closeJob(job)
	return cleanup
}

func (c *CommandCleanup) Cleanup() error {
	if c == nil {
		return os.ErrProcessDone
	}
	c.once.Do(func() {
		if c.job != 0 {
			c.err = cancelCommandTreeAndJob(c.cmd, c.job)
			closeJob(c.job)
			c.job = 0
			return
		}
		c.err = killCommandTree(c.cmd)
	})
	return c.err
}

func cancelCommandTreeAndJob(cmd *exec.Cmd, job syscall.Handle) error {
	treeErr := killCommandTreeFunc(cmd)
	jobErr := terminateJobFunc(job)
	if treeErr != nil && !errors.Is(treeErr, os.ErrProcessDone) {
		return treeErr
	}
	if jobErr != nil {
		return jobErr
	}
	return nil
}

func killCommandTree(cmd *exec.Cmd) error {
	if cmd == nil || cmd.Process == nil {
		return os.ErrProcessDone
	}
	pid := cmd.Process.Pid
	if pid <= 0 {
		return os.ErrProcessDone
	}
	_ = killDescendantProcesses(uint32(pid))
	if proc, err := os.FindProcess(pid); err == nil {
		_ = proc.Kill()
		_ = proc.Release()
	}
	taskkill := exec.Command("taskkill", "/pid", strconv.Itoa(pid), "/T", "/F")
	taskkill.SysProcAttr = &syscall.SysProcAttr{HideWindow: true}
	_ = taskkill.Run()
	return nil
}

func killDescendantProcesses(root uint32) error {
	children, err := snapshotProcessChildren()
	if err != nil {
		return err
	}
	seen := map[uint32]bool{}
	var firstErr error
	var killChildren func(uint32)
	killChildren = func(parent uint32) {
		for _, child := range children[parent] {
			if child == 0 || seen[child] {
				continue
			}
			seen[child] = true
			killChildren(child)
			if err := killProcessID(child); err != nil && firstErr == nil {
				firstErr = err
			}
		}
	}
	killChildren(root)
	return firstErr
}

func snapshotProcessChildren() (map[uint32][]uint32, error) {
	snapshot, err := windows.CreateToolhelp32Snapshot(windows.TH32CS_SNAPPROCESS, 0)
	if err != nil {
		return nil, err
	}
	defer windows.CloseHandle(snapshot)

	children := make(map[uint32][]uint32)
	var entry windows.ProcessEntry32
	entry.Size = uint32(unsafe.Sizeof(entry))
	if err := windows.Process32First(snapshot, &entry); err != nil {
		return nil, err
	}
	for {
		children[entry.ParentProcessID] = append(children[entry.ParentProcessID], entry.ProcessID)
		err = windows.Process32Next(snapshot, &entry)
		if errors.Is(err, windows.ERROR_NO_MORE_FILES) {
			break
		}
		if err != nil {
			return nil, err
		}
	}
	return children, nil
}

func killProcessID(pid uint32) error {
	proc, err := os.FindProcess(int(pid))
	if err != nil {
		return err
	}
	defer proc.Release()
	return proc.Kill()
}

const (
	processTerminate = 0x0001
	processSetQuota  = 0x0100
)

var (
	kernel32               = syscall.NewLazyDLL("kernel32.dll")
	procCreateJobObjectW   = kernel32.NewProc("CreateJobObjectW")
	procAssignProcessToJob = kernel32.NewProc("AssignProcessToJobObject")
	procTerminateJobObject = kernel32.NewProc("TerminateJobObject")

	killCommandTreeFunc = killCommandTree
	terminateJobFunc    = terminateJob
)

func createCommandJob() (syscall.Handle, error) {
	r, _, e := procCreateJobObjectW.Call(0, 0)
	if r == 0 {
		return 0, e
	}
	return syscall.Handle(r), nil
}

func assignProcessToJob(job syscall.Handle, pid int) error {
	if pid <= 0 {
		return os.ErrProcessDone
	}
	process, err := syscall.OpenProcess(processTerminate|processSetQuota, false, uint32(pid))
	if err != nil {
		return err
	}
	defer syscall.CloseHandle(process)
	r, _, e := procAssignProcessToJob.Call(uintptr(job), uintptr(process))
	if r == 0 {
		return e
	}
	return nil
}

func terminateJob(job syscall.Handle) error {
	r, _, e := procTerminateJobObject.Call(uintptr(job), 1)
	if r == 0 {
		return e
	}
	return nil
}

func closeJob(job syscall.Handle) {
	if job != 0 {
		_ = syscall.CloseHandle(job)
	}
}
