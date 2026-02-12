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

	"github.com/blinklabs-io/adder/event"
)

const (
	stopTimeout = 10 * time.Second

	// Backoff constants for automatic restart.
	initialBackoff = 1 * time.Second
	maxBackoff     = 60 * time.Second
	healthyRunTime = 30 * time.Second
	stdoutBufSize  = 1024 * 1024 // 1 MB scanner buffer
)

// ProcessManager manages the lifecycle of the adder subprocess.
type ProcessManager struct {
	mu      sync.Mutex
	cmd     *exec.Cmd
	done    chan struct{}
	binary  string
	cfgFile string

	// Status tracking
	status *StatusTracker

	// Health polling
	healthPoller *HealthPoller
	apiAddress   string
	apiPort      uint

	// Event parsing
	eventParser *EventParser
	events      chan event.Event

	// Auto-restart with backoff
	autoRestart  bool
	restartCount int
	lastStart    time.Time
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

// WithStatusTracker sets a StatusTracker for the process manager
// to report status changes.
func WithStatusTracker(t *StatusTracker) ProcessManagerOption {
	return func(pm *ProcessManager) {
		pm.status = t
	}
}

// WithAPIEndpoint configures the adder API address and port for
// health polling.
func WithAPIEndpoint(
	address string,
	port uint,
) ProcessManagerOption {
	return func(pm *ProcessManager) {
		pm.apiAddress = address
		pm.apiPort = port
	}
}

// WithAutoRestart enables or disables automatic restart with
// exponential backoff when the adder process crashes.
func WithAutoRestart(enabled bool) ProcessManagerOption {
	return func(pm *ProcessManager) {
		pm.autoRestart = enabled
	}
}

// NewProcessManager creates a new ProcessManager with the given options.
func NewProcessManager(
	opts ...ProcessManagerOption,
) *ProcessManager {
	pm := &ProcessManager{
		binary: "adder",
		events: make(chan event.Event, eventChannelBuffer),
	}
	for _, opt := range opts {
		opt(pm)
	}
	return pm
}

// Events returns a read-only channel of events parsed from the
// adder process stdout.
func (pm *ProcessManager) Events() <-chan event.Event {
	return pm.events
}

// Start launches the adder process. Returns an error if it is already
// running.
func (pm *ProcessManager) Start() error {
	pm.mu.Lock()
	defer pm.mu.Unlock()

	if pm.cmd != nil && pm.cmd.Process != nil {
		return errors.New("adder process is already running")
	}

	if pm.status != nil {
		pm.status.Set(StatusStarting)
	}

	args := []string{}
	if pm.cfgFile != "" {
		args = append(args, "--config", pm.cfgFile)
	}

	pm.cmd = exec.Command(pm.binary, args...) //nolint:gosec // binary path from user config
	pm.cmd.Stderr = os.Stderr

	// Capture stdout for event parsing
	stdout, err := pm.cmd.StdoutPipe()
	if err != nil {
		pm.cmd = nil
		if pm.status != nil {
			pm.status.Set(StatusError)
		}
		return fmt.Errorf("creating stdout pipe: %w", err)
	}

	if err := pm.cmd.Start(); err != nil {
		pm.cmd = nil
		if pm.status != nil {
			pm.status.Set(StatusError)
		}
		return fmt.Errorf("starting adder: %w", err)
	}

	slog.Info(
		"adder process started",
		"pid", pm.cmd.Process.Pid,
	)

	pm.lastStart = time.Now()

	// Start event parser on stdout
	pm.eventParser = NewEventParser(stdout, stdoutBufSize)
	pm.eventParser.Start()

	// Forward parsed events to the ProcessManager events channel
	go func() {
		for evt := range pm.eventParser.Events() {
			select {
			case pm.events <- evt:
			default:
				slog.Debug("event channel full, dropping event")
			}
		}
	}()

	// Start health poller if API endpoint is configured
	if pm.apiAddress != "" {
		tracker := pm.status
		if tracker == nil {
			tracker = NewStatusTracker()
		}
		pm.healthPoller = NewHealthPoller(
			pm.apiAddress,
			pm.apiPort,
			tracker,
		)
		pm.healthPoller.Start()
	}

	// Wait for process in background so we can detect exits
	pm.done = make(chan struct{})
	go pm.waitForExit()

	return nil
}

func (pm *ProcessManager) waitForExit() {
	waitErr := pm.cmd.Wait()

	pm.mu.Lock()

	// Stop event parser
	if pm.eventParser != nil {
		pm.eventParser.Stop()
		pm.eventParser = nil
	}

	// Stop health poller
	if pm.healthPoller != nil {
		pm.healthPoller.Stop()
		pm.healthPoller = nil
	}

	lastStart := pm.lastStart
	shouldRestart := pm.autoRestart && waitErr != nil
	restartCount := pm.restartCount

	pm.cmd = nil
	close(pm.done)

	if waitErr != nil {
		if pm.status != nil {
			pm.status.Set(StatusError)
		}
		slog.Warn("adder process exited with error", "error", waitErr)
	} else {
		if pm.status != nil {
			pm.status.Set(StatusStopped)
		}
		slog.Info("adder process exited")
	}

	pm.mu.Unlock()

	// Auto-restart with exponential backoff on crash
	if shouldRestart {
		// Reset restart count if the process ran long enough
		if time.Since(lastStart) >= healthyRunTime {
			restartCount = 0
		}
		delay := backoffDelay(restartCount)

		pm.mu.Lock()
		pm.restartCount = restartCount + 1
		pm.mu.Unlock()

		slog.Info(
			"scheduling automatic restart",
			"delay", delay,
			"restart_count", restartCount+1,
		)

		go func() {
			time.Sleep(delay)
			if err := pm.Start(); err != nil {
				slog.Error(
					"automatic restart failed",
					"error", err,
				)
			}
		}()
	}
}

// backoffDelay calculates the exponential backoff delay for the
// given restart count. The delay starts at 1s, doubles each time,
// and is capped at 60s.
func backoffDelay(restartCount int) time.Duration {
	delay := initialBackoff
	for range restartCount {
		delay *= 2
		if delay >= maxBackoff {
			return maxBackoff
		}
	}
	return delay
}

// Stop terminates the running adder process gracefully.
func (pm *ProcessManager) Stop() error {
	pm.mu.Lock()

	if pm.cmd == nil || pm.cmd.Process == nil {
		pm.mu.Unlock()
		return nil
	}

	// Disable auto-restart when explicitly stopped
	pm.autoRestart = false

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
