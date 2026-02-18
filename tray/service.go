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

import "errors"

// ServiceStatus represents the state of the system service.
type ServiceStatus int

const (
	// ServiceNotRegistered indicates no service is installed.
	ServiceNotRegistered ServiceStatus = iota
	// ServiceRegistered indicates the service is installed but not running.
	ServiceRegistered
	// ServiceRunning indicates the service is installed and currently running.
	ServiceRunning
)

// String returns a human-readable representation of the ServiceStatus.
func (s ServiceStatus) String() string {
	switch s {
	case ServiceNotRegistered:
		return "not registered"
	case ServiceRegistered:
		return "registered"
	case ServiceRunning:
		return "running"
	default:
		return "unknown"
	}
}

// ServiceConfig holds the configuration needed for service registration.
type ServiceConfig struct {
	// BinaryPath is the absolute path to the adder binary.
	BinaryPath string
	// ConfigPath is the optional path to the adder configuration file.
	ConfigPath string
	// LogDir is the directory for log output (used by platforms that
	// do not support journal-style logging).
	LogDir string
}

// Validate checks that the ServiceConfig contains the minimum required fields.
func (c ServiceConfig) Validate() error {
	if c.BinaryPath == "" {
		return errors.New("binary path must not be empty")
	}
	return nil
}

// RegisterService installs adder as a system service using the
// platform-specific mechanism (systemd user unit, launchd plist,
// or Windows Task Scheduler).
func RegisterService(cfg ServiceConfig) error {
	if err := cfg.Validate(); err != nil {
		return err
	}
	return registerService(cfg)
}

// UnregisterService removes the adder system service.
func UnregisterService() error {
	return unregisterService()
}

// ServiceStatusCheck returns the current status of the adder system
// service.
func ServiceStatusCheck() (ServiceStatus, error) {
	return serviceStatusCheck()
}
