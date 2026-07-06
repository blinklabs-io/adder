// Copyright 2026 Blink Labs Software
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

//go:build windows

package setup

// Windows service management for a *per-user, non-elevated* tray app.
//
// We deliberately do NOT use Task Scheduler: `schtasks /Create` writes to the
// root Task Scheduler folder, which per Microsoft only Administrators may do
// ("Only Administrators can schedule tasks."), so a non-elevated tray gets
// "Access is denied".
//
// Autostart model (mirrors how mainstream tray apps like Slack/Dropbox work):
//
//   - The per-user HKCU\...\Run value autostarts the TRAY (adder-tray.exe),
//     which is a GUI-subsystem binary (-H=windowsgui) and therefore starts
//     silently with no console window.
//   - The engine (adder.exe) is a CONSOLE binary. It is never placed in the
//     Run key directly, because Explorer would allocate a visible console
//     window for it at every logon. Instead the tray launches the engine as a
//     DETACHED, windowless child and manages its lifecycle.
//   - The engine command line is recorded in a mirror file so the
//     platform-agnostic ServiceManager (service.go) can diff config changes,
//     and so startService knows what to launch.
//
// None of this requires elevation.

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"time"
	"unsafe"

	"golang.org/x/sys/windows"
	"golang.org/x/sys/windows/registry"
)

const (
	// runKeyPath is the per-user "run at logon" registry key.
	runKeyPath = `Software\Microsoft\Windows\CurrentVersion\Run`
	// runValueName is the value we own under runKeyPath. It launches the TRAY.
	runValueName = "Adder"
	// engineImage is the engine process image name, matched to determine
	// running state and to stop the engine.
	engineImage = "adder.exe"
	// stopWaitTimeout bounds how long we wait for a terminated engine to
	// actually exit before proceeding (so a restart does not race the old
	// process for the API port).
	stopWaitTimeout = 10 * time.Second
)

// serviceCommandFile returns the path of a file mirroring the engine command.
// There is no readable "unit file" behind a registry Run value, so this mirror
// (a) gives service.go something to diff desired-vs-existing against, which is
// what lets RestartIfConfigChanged detect config changes, and (b) records the
// exact command startService must launch.
func serviceCommandFile() string {
	return filepath.Join(ConfigDir(), "service-command.txt")
}

// engineLogFile returns the path the detached engine's stdout/stderr are
// redirected to. A DETACHED_PROCESS has no console, so without this the
// engine's logs (which go to stderr) would be lost.
func engineLogFile() string {
	return filepath.Join(LogDir(), "adder-engine.log")
}

// renderUnit returns the canonical, correctly-quoted engine command line
// stored in the mirror file. ComposeCommandLine produces quoting that
// DecomposeCommandLine (and CreateProcess) parse back identically, including
// paths with spaces such as %ProgramFiles%\Adder.
func renderUnit(cfg ServiceConfig) ([]byte, error) {
	args := []string{cfg.BinaryPath}
	if cfg.ConfigPath != "" {
		args = append(args, "--config", cfg.ConfigPath)
	}
	return []byte(windows.ComposeCommandLine(args)), nil
}

// serviceUnitPath returns the mirror-file path so service.go reads and diffs
// it like any other platform's unit file.
func serviceUnitPath() string {
	return serviceCommandFile()
}

// trayExecutable returns the path of the running tray executable, which is what
// the Run value autostarts. registerService is only ever called from within
// adder-tray, so os.Executable() resolves to adder-tray.exe.
func trayExecutable() (string, error) {
	exe, err := os.Executable()
	if err != nil {
		return "", fmt.Errorf("resolving tray executable path: %w", err)
	}
	return exe, nil
}

func registerService(cfg ServiceConfig) error {
	data, err := renderUnit(cfg)
	if err != nil {
		return err
	}

	// 1. Autostart the tray at logon via the per-user Run key (GUI subsystem =
	//    silent; no elevation needed).
	trayExe, err := trayExecutable()
	if err != nil {
		return err
	}
	runCommand := windows.ComposeCommandLine([]string{trayExe})
	k, _, err := registry.CreateKey(
		registry.CURRENT_USER, runKeyPath, registry.SET_VALUE,
	)
	if err != nil {
		return fmt.Errorf("opening Run registry key: %w", err)
	}
	defer k.Close()
	if err := k.SetStringValue(runValueName, runCommand); err != nil {
		return fmt.Errorf("writing Run registry value: %w", err)
	}

	// 2. Record the engine command so startService knows what to launch and
	//    service.go can diff config changes.
	if err := os.MkdirAll(filepath.Dir(serviceCommandFile()), 0o755); err != nil {
		return fmt.Errorf("creating service state directory: %w", err)
	}
	if err := os.WriteFile(serviceCommandFile(), data, 0o600); err != nil {
		return fmt.Errorf("writing service command file: %w", err)
	}
	return nil
}

func unregisterService() error {
	// Best-effort stop of any running engine first.
	_ = stopService()

	k, err := registry.OpenKey(
		registry.CURRENT_USER, runKeyPath, registry.SET_VALUE,
	)
	switch {
	case err == nil:
		defer k.Close()
		if derr := k.DeleteValue(runValueName); derr != nil &&
			!errors.Is(derr, registry.ErrNotExist) {
			return fmt.Errorf("deleting Run registry value: %w", derr)
		}
	case errors.Is(err, registry.ErrNotExist):
		// Key absent: nothing registered, treat as success.
	default:
		return fmt.Errorf("opening Run registry key: %w", err)
	}

	if err := os.Remove(serviceCommandFile()); err != nil &&
		!errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("removing service command file: %w", err)
	}
	return nil
}

func serviceStatusCheck() (ServiceStatus, error) {
	running, err := engineRunning()
	if err != nil {
		return ServiceNotRegistered, err
	}
	if running {
		return ServiceRunning, nil
	}
	registered, err := serviceRegistered()
	if err != nil {
		return ServiceNotRegistered, err
	}
	if registered {
		return ServiceRegistered, nil
	}
	return ServiceNotRegistered, nil
}

// startService (re)starts the engine. It first terminates any running engine
// AND waits for it to exit, so a config change can never leave two instances
// racing for the API port, then launches the recorded command detached and
// windowless with output redirected to the engine log.
func startService() error {
	command, err := registeredCommand()
	if err != nil {
		return err
	}
	argv, err := windows.DecomposeCommandLine(command)
	if err != nil {
		return fmt.Errorf("parsing engine command %q: %w", command, err)
	}
	if len(argv) == 0 {
		return errors.New("recorded engine command is empty")
	}

	// Ensure the previous engine is fully gone before starting a new one.
	if err := stopService(); err != nil {
		return fmt.Errorf("stopping existing engine before start: %w", err)
	}

	// Redirect the detached engine's output to a log file; a DETACHED_PROCESS
	// has no console, so its stderr would otherwise be an invalid handle and
	// its logs lost. Failure to open the log is non-fatal (engine still runs).
	var logHandle *os.File
	if err := os.MkdirAll(filepath.Dir(engineLogFile()), 0o755); err == nil {
		logHandle, _ = os.OpenFile(
			engineLogFile(),
			os.O_CREATE|os.O_WRONLY|os.O_APPEND,
			0o600,
		)
	}
	if logHandle != nil {
		defer logHandle.Close()
	}

	cmd := exec.Command(argv[0], argv[1:]...) //nolint:gosec // argv from our own recorded command
	// DETACHED_PROCESS: the engine is a console app; give it no console so it
	// runs windowless and independent of the (windowsgui, console-less) tray.
	// CREATE_NO_WINDOW would be ignored alongside DETACHED_PROCESS, so it is
	// not set. HideWindow guards any incidental GUI window.
	cmd.SysProcAttr = &syscall.SysProcAttr{
		HideWindow:    true,
		CreationFlags: windows.DETACHED_PROCESS,
	}
	if logHandle != nil {
		cmd.Stdout = logHandle
		cmd.Stderr = logHandle
	}
	if err := cmd.Start(); err != nil {
		return fmt.Errorf("starting adder engine: %w", err)
	}
	// Detach: do not Wait; the engine outlives the tray.
	if cmd.Process != nil {
		_ = cmd.Process.Release()
	}
	return nil
}

func stopService() error {
	return terminateEngine()
}

// registeredCommand returns the engine command recorded by registerService, or
// an error if the service is not registered.
func registeredCommand() (string, error) {
	data, err := os.ReadFile(serviceCommandFile())
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return "", errors.New("adder service is not registered")
		}
		return "", fmt.Errorf("reading service command file: %w", err)
	}
	command := strings.TrimSpace(string(data))
	if command == "" {
		return "", errors.New("recorded engine command is empty")
	}
	return command, nil
}

// serviceRegistered reports whether the tray autostart Run value exists.
func serviceRegistered() (bool, error) {
	k, err := registry.OpenKey(
		registry.CURRENT_USER, runKeyPath, registry.QUERY_VALUE,
	)
	if err != nil {
		if errors.Is(err, registry.ErrNotExist) {
			return false, nil
		}
		return false, fmt.Errorf("opening Run registry key: %w", err)
	}
	defer k.Close()
	if _, _, err := k.GetStringValue(runValueName); err != nil {
		if errors.Is(err, registry.ErrNotExist) {
			return false, nil
		}
		return false, fmt.Errorf("reading Run registry value: %w", err)
	}
	return true, nil
}

// engineRunning reports whether at least one engine process (engineImage) is
// currently running.
func engineRunning() (bool, error) {
	found := false
	err := forEachEngineProcess(func(uint32) bool {
		found = true
		return false // stop at the first match
	})
	return found, err
}

// terminateEngine terminates every running engine process and waits (bounded)
// for each to exit so a subsequent start does not race for the API port.
// Missing processes are not an error; it returns the first failure, if any.
func terminateEngine() error {
	var firstErr error
	err := forEachEngineProcess(func(pid uint32) bool {
		h, oerr := windows.OpenProcess(
			windows.PROCESS_TERMINATE|windows.SYNCHRONIZE, false, pid,
		)
		if oerr != nil {
			// The process may have exited between enumeration and open, or be
			// otherwise inaccessible; record but keep going.
			if firstErr == nil {
				firstErr = fmt.Errorf("opening engine process %d: %w", pid, oerr)
			}
			return true
		}
		defer windows.CloseHandle(h)
		if terr := windows.TerminateProcess(h, 1); terr != nil {
			if firstErr == nil {
				firstErr = fmt.Errorf(
					"terminating engine process %d: %w", pid, terr,
				)
			}
			return true
		}
		// Wait (bounded) for the process to actually exit. Surface a
		// timeout/error as a stop failure so the caller does not launch a
		// second engine that races the dying one for the API port.
		waitResult, werr := windows.WaitForSingleObject(
			h, uint32(stopWaitTimeout.Milliseconds()),
		)
		if werr != nil {
			if firstErr == nil {
				firstErr = fmt.Errorf(
					"waiting for engine process %d to exit: %w", pid, werr)
			}
			return true
		}
		if waitResult == uint32(windows.WAIT_TIMEOUT) && firstErr == nil {
			firstErr = fmt.Errorf(
				"engine process %d did not exit within %s",
				pid, stopWaitTimeout)
		}
		return true
	})
	if err != nil {
		return err
	}
	return firstErr
}

// forEachEngineProcess walks the process table and invokes fn with the PID of
// every process whose image name matches engineImage. fn returns false to stop
// iteration early.
func forEachEngineProcess(fn func(pid uint32) bool) error {
	snap, err := windows.CreateToolhelp32Snapshot(windows.TH32CS_SNAPPROCESS, 0)
	if err != nil {
		return fmt.Errorf("snapshotting processes: %w", err)
	}
	defer windows.CloseHandle(snap)

	var entry windows.ProcessEntry32
	entry.Size = uint32(unsafe.Sizeof(entry))
	err = windows.Process32First(snap, &entry)
	for err == nil {
		name := windows.UTF16ToString(entry.ExeFile[:])
		if strings.EqualFold(name, engineImage) {
			if !fn(entry.ProcessID) {
				return nil
			}
		}
		err = windows.Process32Next(snap, &entry)
	}
	// ERROR_NO_MORE_FILES marks the clean end of enumeration.
	if errors.Is(err, windows.ERROR_NO_MORE_FILES) {
		return nil
	}
	return err
}
