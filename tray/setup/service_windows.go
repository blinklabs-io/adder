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
// ("Only Administrators can schedule tasks."), so a non-elevated tray would get
// "Access is denied".
//
// Autostart model (mirrors mainstream tray apps like Slack/Dropbox):
//
//   - The per-user HKCU\...\Run value autostarts the TRAY (adder-tray.exe), a
//     GUI-subsystem binary (-H=windowsgui) that starts silently, no console.
//   - The engine (adder.exe) is a console binary. It is never placed in the
//     Run key directly, because Explorer would allocate a visible console
//     window for it at every logon. Instead the tray launches it as a
//     DETACHED, windowless child and manages its lifecycle by the PID it
//     recorded at launch.
//   - The engine command line is mirrored to a file so service.go can diff
//     config changes and startService knows exactly what to launch.
//
// None of this requires elevation.

import (
	"errors"
	"fmt"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
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
	// stopWaitTimeout bounds how long we wait for a terminated engine to
	// actually exit before proceeding, so a restart does not race the old
	// process for the API port.
	stopWaitTimeout = 10 * time.Second
)

// serviceUnitPath returns a virtual identifier: there is no on-disk unit file
// behind a registry Run value.
//
//nolint:unused // used in plan_paths_service_test.go
func serviceUnitPath() string {
	return "runkey://" + runValueName
}

var (
	runKeyRegistryHive = registry.CURRENT_USER
	runKeyRegistryPath = runKeyPath
	enginePIDMu        sync.Mutex
)

// existingUnit reads and verifies both the registry autostart value pointing
// to the tray, and the mirrored engine command file. If either is missing,
// stale, or points to the wrong target, it returns nil to trigger a repair.
func existingUnit() []byte {
	k, err := registry.OpenKey(
		runKeyRegistryHive, runKeyRegistryPath, registry.QUERY_VALUE,
	)
	if err != nil {
		return nil
	}
	defer k.Close()
	val, _, err := k.GetStringValue(runValueName)
	if err != nil {
		return nil
	}

	trayExe, err := trayExecutable()
	if err != nil {
		return nil
	}
	wantCommand := windows.ComposeCommandLine([]string{trayExe})
	if !strings.EqualFold(val, wantCommand) {
		return nil
	}

	data, err := os.ReadFile(serviceCommandFile())
	if err != nil {
		return nil
	}
	return data
}

// serviceCommandFile mirrors the engine command. There is no readable unit
// behind a Run value, so this file (a) records exactly what startService must
// launch and (b) lets configFingerprint detect command changes.
func serviceCommandFile() string {
	return filepath.Join(ConfigDir(), "service-command.txt")
}

// engineLogFile is where the detached engine's stdout/stderr are redirected. A
// DETACHED_PROCESS has no console, so without this its logs would be lost.
func engineLogFile() string {
	return filepath.Join(LogDir(), "adder-engine.log")
}

// enginePIDFile records the PID of the engine the tray launched, so stop can
// terminate that exact process even after its on-disk image is renamed.
func enginePIDFile() string {
	return filepath.Join(ConfigDir(), "engine.pid")
}

// renderUnit returns the canonical, correctly-quoted engine command line.
// ComposeCommandLine quoting round-trips through DecomposeCommandLine and
// CreateProcess, including paths with spaces such as %ProgramFiles%\Adder.
func renderUnit(cfg ServiceConfig) ([]byte, error) {
	args := []string{cfg.BinaryPath}
	if cfg.ConfigPath != "" {
		args = append(args, "--config", cfg.ConfigPath)
	}
	return []byte(windows.ComposeCommandLine(args)), nil
}

// trayExecutable returns the running tray executable path, which the Run value
// autostarts. registerService is only ever called from adder-tray, so
// os.Executable() resolves to adder-tray.exe.
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
		runKeyRegistryHive, runKeyRegistryPath, registry.SET_VALUE,
	)
	if err != nil {
		return fmt.Errorf("opening Run registry key: %w", err)
	}
	defer k.Close()
	if err := k.SetStringValue(runValueName, runCommand); err != nil {
		return fmt.Errorf("writing Run registry value: %w", err)
	}

	// 2. Record the engine command so startService knows what to launch.
	if err := os.MkdirAll(
		filepath.Dir(serviceCommandFile()), 0o755,
	); err != nil {
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
		runKeyRegistryHive, runKeyRegistryPath, registry.SET_VALUE,
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
	if pid, ok := readEnginePID(); ok {
		expected, expectedErr := registeredEnginePath()
		actual, alive, actualErr := runningProcessPath(pid)
		if expectedErr == nil && actualErr == nil &&
			alive && sameExecutablePath(expected, actual) {
			return ServiceRunning, nil
		}
		slog.Warn(
			"ignoring stale engine pid",
			"pid", pid,
			"expected", expected,
			"actual", actual,
			"expected_error", expectedErr,
			"actual_error", actualErr,
		)
		removeEnginePIDIf(pid)
	}
	if serviceRegistered() {
		return ServiceRegistered, nil
	}
	return ServiceNotRegistered, nil
}

// startService (re)starts the engine: it terminates any engine we previously
// launched AND waits for it to exit (so a restart cannot leave two instances
// racing for the API port), then launches the recorded command detached and
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

	// Redirect the detached engine's output to a log file; failure to open it
	// is non-fatal (the engine still runs).
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

	cmd := exec.Command(
		argv[0],
		argv[1:]...) //nolint:gosec // argv from our own recorded command
	// DETACHED_PROCESS: the engine is a console app; give it no console so it
	// runs windowless and independent of the (windowsgui) tray. HideWindow
	// guards any incidental GUI window.
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
	// Record the PID and detach: the engine outlives the tray.
	if cmd.Process != nil {
		if err := writeEnginePID(cmd.Process.Pid); err != nil {
			if killErr := cmd.Process.Kill(); killErr != nil {
				return fmt.Errorf(
					"recording engine pid: %w (cleanup failed: %v)",
					err, killErr,
				)
			}
			_, _ = cmd.Process.Wait()
			return fmt.Errorf("recording engine pid: %w", err)
		}
		_ = cmd.Process.Release()
	}
	return nil
}

// stopService terminates the engine the tray launched (by recorded PID) and
// waits for it to exit. A missing PID file or already-exited process is a
// no-op success.
func stopService() error {
	pid, ok := readEnginePID()
	if !ok {
		return terminateUntrackedEngines()
	}
	if err := stopTrackedEngine(pid, terminatePID); err != nil {
		return err
	}
	return terminateUntrackedEngines()
}

func stopTrackedEngine(
	pid uint32,
	terminate func(uint32, string) error,
) error {
	if err := terminate(pid, expectedEngineImage()); err != nil {
		return err
	}
	removeEnginePID()
	return nil
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

// serviceRegistered reports whether the tray autostart and command mirror are
// both present and current.
func serviceRegistered() bool {
	return existingUnit() != nil
}

func writeEnginePID(pid int) error {
	enginePIDMu.Lock()
	defer enginePIDMu.Unlock()
	if err := os.MkdirAll(filepath.Dir(enginePIDFile()), 0o755); err != nil {
		return fmt.Errorf("creating engine pid directory: %w", err)
	}
	if err := os.WriteFile(
		enginePIDFile(), []byte(strconv.Itoa(pid)), 0o600,
	); err != nil {
		return fmt.Errorf("writing engine pid file: %w", err)
	}
	return nil
}

func readEnginePID() (uint32, bool) {
	data, err := os.ReadFile(enginePIDFile())
	if err != nil {
		return 0, false
	}
	n, err := strconv.Atoi(strings.TrimSpace(string(data)))
	if err != nil || n <= 0 {
		return 0, false
	}
	return uint32(n), true
}

func removeEnginePID() {
	enginePIDMu.Lock()
	defer enginePIDMu.Unlock()
	removeEnginePIDUnlocked()
}

func removeEnginePIDUnlocked() {
	if err := os.Remove(enginePIDFile()); err != nil &&
		!errors.Is(err, os.ErrNotExist) {
		slog.Warn("could not remove engine pid file", "error", err)
	}
}

func removeEnginePIDIf(pid uint32) {
	enginePIDMu.Lock()
	defer enginePIDMu.Unlock()
	current, ok := readEnginePID()
	if !ok || current != pid {
		return
	}
	removeEnginePIDUnlocked()
}

// terminatePID terminates a single process and waits (bounded) for it to exit.
// An already-gone process is success.
//
// If the process cannot be opened for termination but is still alive, we must
// distinguish two cases. Our own engine (same user, same session) is always
// openable for PROCESS_TERMINATE, so an ERROR_ACCESS_DENIED means the recorded
// PID is stale and has been reused by a protected or other-user process (e.g.
// after a hard reboot). We only treat an unopenable-but-alive PID as a survivor
// we must not stack on when we can positively confirm its running image is our
// engine (expectImage). Otherwise we proceed, so a reused stale PID cannot
// permanently block engine startup.
func terminatePID(pid uint32, expectImage string) error {
	h, err := windows.OpenProcess(
		windows.PROCESS_TERMINATE|windows.SYNCHRONIZE, false, pid,
	)
	if err != nil {
		if !processAlive(pid) {
			return nil // already exited
		}
		actual, known := pidImageName(pid)
		if isOurEngine(expectImage, actual, known) {
			return fmt.Errorf(
				"cannot terminate engine process %d (still running): %w",
				pid, err)
		}
		slog.Warn(
			"stale engine pid reused by another process; ignoring",
			"pid", pid, "image", actual, "error", err,
		)
		return nil
	}
	defer func() { _ = windows.CloseHandle(h) }()
	if terr := windows.TerminateProcess(h, 1); terr != nil {
		return fmt.Errorf("terminating engine process %d: %w", pid, terr)
	}
	res, werr := windows.WaitForSingleObject(
		h, uint32(stopWaitTimeout.Milliseconds()),
	)
	if werr != nil {
		return fmt.Errorf(
			"waiting for engine process %d to exit: %w", pid, werr)
	}
	if res == uint32(windows.WAIT_TIMEOUT) {
		return fmt.Errorf(
			"engine process %d did not exit within %s", pid, stopWaitTimeout)
	}
	return nil
}

// processAlive reports whether pid refers to a live process. An access-denied
// on open means the process exists but is protected, so it counts as alive.
func processAlive(pid uint32) bool {
	h, err := windows.OpenProcess(
		windows.PROCESS_QUERY_LIMITED_INFORMATION, false, pid,
	)
	if err != nil {
		return errors.Is(err, windows.ERROR_ACCESS_DENIED)
	}
	defer func() { _ = windows.CloseHandle(h) }()
	var code uint32
	if err := windows.GetExitCodeProcess(h, &code); err != nil {
		return true
	}
	const stillActive = 259 // STILL_ACTIVE
	return code == stillActive
}

// isOurEngine reports whether an unopenable-but-alive PID should be treated as
// the engine we launched. It returns true only on positive confirmation that
// the running image (actual, present only when known) matches our recorded
// engine binary (expectImage). An unknown or mismatched image means the stale
// PID was reused by a foreign process, so the caller may safely proceed.
func isOurEngine(expectImage, actual string, known bool) bool {
	if !known || expectImage == "" {
		return false
	}
	return strings.EqualFold(expectImage, actual)
}

// expectedEngineImage returns the base name of the engine executable recorded
// by registerService (e.g. "adder.exe"), or "" if it cannot be determined.
func expectedEngineImage() string {
	path, err := registeredEnginePath()
	if err != nil {
		return ""
	}
	return filepath.Base(path)
}

func registeredEnginePath() (string, error) {
	command, err := registeredCommand()
	if err != nil {
		return "", err
	}
	argv, err := windows.DecomposeCommandLine(command)
	if err != nil {
		return "", fmt.Errorf("parsing engine command %q: %w", command, err)
	}
	if len(argv) == 0 || strings.TrimSpace(argv[0]) == "" {
		return "", errors.New("recorded engine command has no executable")
	}
	return argv[0], nil
}

func sameExecutablePath(expected, actual string) bool {
	if strings.TrimSpace(expected) == "" || strings.TrimSpace(actual) == "" {
		return false
	}
	return strings.EqualFold(filepath.Clean(expected), filepath.Clean(actual))
}

// terminateUntrackedEngines recovers from a missing PID file by terminating
// engine processes whose executable resides in the registered install
// directory. Full-path matching avoids touching an unrelated adder.exe.
func terminateUntrackedEngines() error {
	command, err := registeredCommand()
	if err != nil {
		return nil
	}
	argv, err := windows.DecomposeCommandLine(command)
	if err != nil || len(argv) == 0 {
		return nil
	}
	installDir := filepath.Dir(argv[0])
	self := uint32(os.Getpid())
	return forEachProcess(func(pid uint32) error {
		if pid == self {
			return nil
		}
		path, err := fullProcessPath(pid)
		if err != nil || !isUntrackedEnginePath(path, installDir) {
			return nil
		}
		return terminatePID(pid, filepath.Base(path))
	})
}

func isUntrackedEnginePath(path, installDir string) bool {
	return pathUnderDir(path, installDir) &&
		!strings.EqualFold(filepath.Base(path), "adder-tray.exe")
}

func forEachProcess(fn func(uint32) error) error {
	snap, err := windows.CreateToolhelp32Snapshot(
		windows.TH32CS_SNAPPROCESS, 0,
	)
	if err != nil {
		return fmt.Errorf("snapshotting processes: %w", err)
	}
	defer func() { _ = windows.CloseHandle(snap) }()
	var entry windows.ProcessEntry32
	entry.Size = uint32(unsafe.Sizeof(entry))
	for err = windows.Process32First(snap, &entry); err == nil; {
		if visitErr := fn(entry.ProcessID); visitErr != nil {
			return visitErr
		}
		err = windows.Process32Next(snap, &entry)
	}
	if errors.Is(err, windows.ERROR_NO_MORE_FILES) {
		return nil
	}
	return err
}

func fullProcessPath(pid uint32) (string, error) {
	handle, err := windows.OpenProcess(
		windows.PROCESS_QUERY_LIMITED_INFORMATION, false, pid,
	)
	if err != nil {
		return "", err
	}
	defer func() { _ = windows.CloseHandle(handle) }()
	return processPath(handle)
}

func runningProcessPath(pid uint32) (string, bool, error) {
	handle, err := windows.OpenProcess(
		windows.PROCESS_QUERY_LIMITED_INFORMATION, false, pid,
	)
	if err != nil {
		return "", false, err
	}
	defer func() { _ = windows.CloseHandle(handle) }()
	path, err := processPath(handle)
	if err != nil {
		return "", false, err
	}
	var code uint32
	if err := windows.GetExitCodeProcess(handle, &code); err != nil {
		return "", false, err
	}
	const stillActive = 259 // STILL_ACTIVE
	return path, code == stillActive, nil
}

func processPath(handle windows.Handle) (string, error) {
	buf := make([]uint16, 1024)
	size := uint32(len(buf))
	if err := windows.QueryFullProcessImageName(
		handle, 0, &buf[0], &size,
	); err != nil {
		return "", err
	}
	return windows.UTF16ToString(buf[:size]), nil
}

func pathUnderDir(path, dir string) bool {
	path = strings.ToLower(filepath.Clean(path))
	dir = strings.ToLower(filepath.Clean(dir))
	return strings.HasPrefix(path, strings.TrimRight(dir, `\/`)+`\`)
}

// pidImageName returns the executable base name of pid via a process snapshot.
// Unlike OpenProcess, a snapshot does not require any access right on the
// target process, so it can name protected or other-user processes that the
// terminate path would be denied. The bool reports whether pid was found.
func pidImageName(pid uint32) (string, bool) {
	snap, err := windows.CreateToolhelp32Snapshot(
		windows.TH32CS_SNAPPROCESS, 0,
	)
	if err != nil {
		return "", false
	}
	defer func() { _ = windows.CloseHandle(snap) }()
	var e windows.ProcessEntry32
	e.Size = uint32(unsafe.Sizeof(e))
	for err = windows.Process32First(snap, &e); err == nil; {
		if e.ProcessID == pid {
			return windows.UTF16ToString(e.ExeFile[:]), true
		}
		err = windows.Process32Next(snap, &e)
	}
	return "", false
}
