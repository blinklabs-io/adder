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

package tray

import (
	"errors"
	"fmt"
	"log/slog"
	"os"
	"os/exec"
	"runtime"
	"sync"
	"time"
)

const stopTimeout = 10 * time.Second

// ProcessManager manages the lifecycle of the adder subprocess.
type ProcessManager struct {
	mu      sync.Mutex
	cmd     *exec.Cmd
	done    chan struct{}
	binary  string
	cfgFile string
}

// ProcessManagerOption is a functional option for ProcessManager.
type ProcessManagerOption func(*ProcessManager)

// WithBinary sets the path to the adder binary.
func WithBinary(path string) ProcessManagerOption {
	return func(pm *ProcessManager) {
		pm.binary = path
	}
}

// WithConfigFile sets the path to the adder configuration file.
func WithConfigFile(path string) ProcessManagerOption {
	return func(pm *ProcessManager) {
		pm.cfgFile = path
	}
}

// NewProcessManager creates a new ProcessManager with the given options.
func NewProcessManager(
	opts ...ProcessManagerOption,
) *ProcessManager {
	pm := &ProcessManager{
		binary: "adder",
	}
	for _, opt := range opts {
		opt(pm)
	}
	return pm
}

// Start launches the adder process. Returns an error if it is already
// running.
func (pm *ProcessManager) Start() error {
	pm.mu.Lock()
	defer pm.mu.Unlock()

	if pm.cmd != nil && pm.cmd.Process != nil {
		return errors.New("adder process is already running")
	}

	args := []string{}
	if pm.cfgFile != "" {
		args = append(args, "--config", pm.cfgFile)
	}

	pm.cmd = exec.Command(pm.binary, args...) //nolint:gosec // binary path from user config
	pm.cmd.Stdout = os.Stdout
	pm.cmd.Stderr = os.Stderr

	if err := pm.cmd.Start(); err != nil {
		pm.cmd = nil
		return fmt.Errorf("starting adder: %w", err)
	}

	slog.Info(
		"adder process started",
		"pid", pm.cmd.Process.Pid,
	)

	// Wait for process in background so we can detect exits
	pm.done = make(chan struct{})
	go func() {
		err := pm.cmd.Wait()
		pm.mu.Lock()
		defer pm.mu.Unlock()
		pm.cmd = nil
		close(pm.done)
		if err != nil {
			slog.Warn("adder process exited with error", "error", err)
		} else {
			slog.Info("adder process exited")
		}
	}()

	return nil
}

// Stop terminates the running adder process gracefully.
func (pm *ProcessManager) Stop() error {
	pm.mu.Lock()

	if pm.cmd == nil || pm.cmd.Process == nil {
		pm.mu.Unlock()
		return nil
	}

	slog.Info("stopping adder process", "pid", pm.cmd.Process.Pid)

	done := pm.done

	if runtime.GOOS == "windows" {
		// os.Interrupt is not supported on Windows
		if err := pm.cmd.Process.Kill(); err != nil {
			if errors.Is(err, os.ErrProcessDone) {
				pm.mu.Unlock()
				<-done
				return nil
			}
			pm.mu.Unlock()
			return fmt.Errorf("killing adder: %w", err)
		}
	} else {
		if err := pm.cmd.Process.Signal(os.Interrupt); err != nil {
			if errors.Is(err, os.ErrProcessDone) {
				pm.mu.Unlock()
				<-done
				return nil
			}
			pm.mu.Unlock()
			return fmt.Errorf("sending interrupt to adder: %w", err)
		}
	}

	pm.mu.Unlock()

	// Wait for the process to exit, with a timeout to avoid
	// blocking forever if the process ignores the signal.
	select {
	case <-done:
	case <-time.After(stopTimeout):
		slog.Warn("adder process did not exit in time, force killing")
		pm.mu.Lock()
		if pm.cmd == nil || pm.cmd.Process == nil {
			pm.mu.Unlock()
			return nil
		}
		if err := pm.cmd.Process.Kill(); err != nil {
			pm.mu.Unlock()
			if errors.Is(err, os.ErrProcessDone) {
				<-done
				return nil
			}
			return fmt.Errorf("force killing adder: %w", err)
		}
		pm.mu.Unlock()
		<-done
	}

	return nil
}

// Restart stops and then starts the adder process.
func (pm *ProcessManager) Restart() error {
	if err := pm.Stop(); err != nil {
		return fmt.Errorf("stopping for restart: %w", err)
	}
	// Stop() waits for the process to exit, so Start() is safe here
	return pm.Start()
}

// IsRunning reports whether the adder process is currently running.
func (pm *ProcessManager) IsRunning() bool {
	pm.mu.Lock()
	defer pm.mu.Unlock()
	return pm.cmd != nil && pm.cmd.Process != nil
}
