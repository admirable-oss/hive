//go:build windows

package terminal

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"unsafe"

	"golang.org/x/sys/windows"

	"github.com/admirable-oss/hive/internal/pgroup"
	"github.com/admirable-oss/hive/internal/platform"
)

// conPTY is a Windows pseudo console (ConPTY, Windows 10 1809 and later):
// Hive writes the program's input to one pipe and reads its output, as VT
// sequences, from another. The process starts suspended inside a job, so
// it and everything it starts can be stopped as a whole.
type conPTY struct {
	in, out *os.File
	console windows.Handle
	process windows.Handle
	pid     int

	closeOnce sync.Once
	waitOnce  sync.Once
	waitErr   error
}

// exitStatus is a failed exit, read the way the process supervisor reads
// *exec.ExitError.
type exitStatus struct{ code int }

func (e *exitStatus) Error() string { return fmt.Sprintf("exit status %d", e.code) }
func (e *exitStatus) ExitCode() int { return e.code }

func openPTY(cmd Command, size Size, env []string) (ptyDevice, error) {
	line, err := commandLine(cmd.Path, cmd.Args)
	if err != nil {
		return nil, err
	}
	var inRead, inWrite, outRead, outWrite windows.Handle
	if err := windows.CreatePipe(&inRead, &inWrite, nil, 0); err != nil {
		return nil, fmt.Errorf("create input pipe: %w", err)
	}
	if err := windows.CreatePipe(&outRead, &outWrite, nil, 0); err != nil {
		closeHandles(inRead, inWrite)
		return nil, fmt.Errorf("create output pipe: %w", err)
	}
	// A restored pane shows replayed text before its program starts.
	// ConPTY clears the screen when it starts, unless told to inherit the
	// cursor: then it asks where the cursor is (the emulator answers) and
	// starts there, below the replay.
	var consoleFlags uint32
	if len(cmd.Replay) > 0 {
		consoleFlags |= pseudoConsoleInheritCursor
	}
	var console windows.Handle
	if err := windows.CreatePseudoConsole(coord(size), inRead, outWrite, consoleFlags, &console); err != nil {
		closeHandles(inRead, inWrite, outRead, outWrite)
		return nil, fmt.Errorf("create pseudo console (Windows 10 1809 or later): %w", err)
	}
	// The pseudo console holds its own copies of its ends.
	closeHandles(inRead, outWrite)
	p := &conPTY{in: os.NewFile(uintptr(inWrite), "conpty-in"), out: os.NewFile(uintptr(outRead), "conpty-out"), console: console}
	fail := func(err error) (ptyDevice, error) {
		windows.ClosePseudoConsole(console)
		_ = p.in.Close()
		_ = p.out.Close()
		return nil, err
	}

	attrs, err := windows.NewProcThreadAttributeList(1)
	if err != nil {
		return fail(err)
	}
	defer attrs.Delete()
	// The attribute's value is the console handle itself.
	if err := attrs.Update(windows.PROC_THREAD_ATTRIBUTE_PSEUDOCONSOLE, *(*unsafe.Pointer)(unsafe.Pointer(&console)), unsafe.Sizeof(console)); err != nil { //nolint:gosec // the attribute's value is the handle itself
		return fail(err)
	}
	var si windows.StartupInfoEx
	si.Cb = uint32(unsafe.Sizeof(si))
	// Std handles must not be inherited from the (detached, redirected)
	// daemon or shim, or the program would write there, not to the console.
	si.Flags = windows.STARTF_USESTDHANDLES
	si.ProcThreadAttributeList = attrs.List()

	cmdLine, err := windows.UTF16PtrFromString(line)
	if err != nil {
		return fail(err)
	}
	var dir *uint16
	if cmd.WorkingDir != "" {
		if dir, err = windows.UTF16PtrFromString(cmd.WorkingDir); err != nil {
			return fail(err)
		}
	}
	block, err := envBlock(env)
	if err != nil {
		return fail(err)
	}
	var pi windows.ProcessInformation
	flags := uint32(windows.EXTENDED_STARTUPINFO_PRESENT | windows.CREATE_UNICODE_ENVIRONMENT | windows.CREATE_SUSPENDED)
	if err := windows.CreateProcess(nil, cmdLine, nil, nil, false, flags, &block[0], dir, &si.StartupInfo, &pi); err != nil {
		return fail(fmt.Errorf("start %s: %w", cmd.Path, err))
	}
	defer func() { _ = windows.CloseHandle(pi.Thread) }()
	job, err := pgroup.NewJob()
	if err == nil {
		if aerr := windows.AssignProcessToJobObject(job, pi.Process); aerr != nil {
			_ = windows.CloseHandle(job) // a job that forbids nesting: taskkill then
			job = 0
		}
	}
	if _, err := windows.ResumeThread(pi.Thread); err != nil {
		_ = windows.TerminateProcess(pi.Process, 1)
		_ = windows.CloseHandle(pi.Process)
		return fail(err)
	}
	p.process, p.pid = pi.Process, int(pi.ProcessId)
	if job != 0 {
		// Closing the console sends its programs CTRL_CLOSE_EVENT: the
		// polite request to stop, before the job is ended.
		pgroup.Track(p.pid, job, p.closeConsole)
	}
	// ConPTY keeps its output open after the program exits; close the
	// console then, so the reader sees the end of the output.
	go func() {
		_ = p.Wait()
		p.closeConsole()
	}()
	return p, nil
}

// pseudoConsoleInheritCursor is PSEUDOCONSOLE_INHERIT_CURSOR.
const pseudoConsoleInheritCursor = 0x1

func coord(size Size) windows.Coord {
	return windows.Coord{X: int16(size.Width), Y: int16(size.Height)}
}

func closeHandles(hs ...windows.Handle) {
	for _, h := range hs {
		_ = windows.CloseHandle(h)
	}
}

// commandLine builds the command line CreateProcess takes for path and
// args. The program is found on PATH with PATHEXT, as a shell would; batch
// files run through cmd.exe, which CreateProcess needs for them.
func commandLine(path string, args []string) (string, error) {
	exe, err := exec.LookPath(path)
	if err != nil {
		return "", err
	}
	if abs, err := filepath.Abs(exe); err == nil {
		exe = abs
	}
	line := windows.ComposeCommandLine(append([]string{exe}, args...))
	switch strings.ToLower(filepath.Ext(exe)) {
	case ".cmd", ".bat":
		comspec := os.Getenv("ComSpec")
		if comspec == "" {
			comspec = `C:\Windows\System32\cmd.exe`
		}
		line = windows.ComposeCommandLine([]string{comspec}) + ` /d /s /c "` + line + `"`
	}
	return line, nil
}

// envBlock is env as a Unicode environment block: NUL-separated entries,
// ending with an extra NUL.
func envBlock(env []string) ([]uint16, error) {
	var block []uint16
	for _, kv := range env {
		if strings.IndexByte(kv, 0) >= 0 {
			return nil, fmt.Errorf("environment entry %q contains NUL", kv)
		}
		u, err := windows.UTF16FromString(kv)
		if err != nil {
			return nil, err
		}
		block = append(block, u...) // ends with its NUL
	}
	if len(block) == 0 {
		block = append(block, 0)
	}
	return append(block, 0), nil
}

func (p *conPTY) Read(b []byte) (int, error) { return p.out.Read(b) }

func (p *conPTY) Write(b []byte) (int, error) { return p.in.Write(b) }

func (p *conPTY) Resize(size Size) error {
	return windows.ResizePseudoConsole(p.console, coord(size))
}

func (p *conPTY) Pid() int { return p.pid }

func (p *conPTY) Wait() error {
	p.waitOnce.Do(func() {
		if _, err := windows.WaitForSingleObject(p.process, windows.INFINITE); err != nil {
			p.waitErr = err
			return
		}
		var code uint32
		if err := windows.GetExitCodeProcess(p.process, &code); err != nil {
			p.waitErr = err
			return
		}
		if code != 0 {
			p.waitErr = &exitStatus{code: int(code)}
		}
	})
	return p.waitErr
}

func (p *conPTY) Foreground() (int, error) { return platform.NewestDescendant(p.pid) }

// closeConsole ends the pseudo console: its programs get CTRL_CLOSE_EVENT,
// and its output pipe ends once drained.
func (p *conPTY) closeConsole() {
	p.closeOnce.Do(func() { windows.ClosePseudoConsole(p.console) })
}

func (p *conPTY) Close() error {
	p.closeConsole()
	pgroup.Release(p.pid)
	err := errors.Join(p.in.Close(), p.out.Close())
	if p.process != 0 {
		_ = windows.CloseHandle(p.process)
		p.process = 0
	}
	return err
}
