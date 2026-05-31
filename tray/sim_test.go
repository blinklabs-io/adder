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
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	"fyne.io/fyne/v2/test"
	"github.com/blinklabs-io/adder/tray/setup"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestSimulateStartAction simulates a user clicking "Start" in the tray menu.
// It uses real system calls and expects the sidecar to be present.
func TestSimulateStartAction(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping simulation in short mode")
	}
	if os.Getenv("ADDER_TRAY_INTEGRATION") != "1" {
		t.Skip("skipping integration simulation; set ADDER_TRAY_INTEGRATION=1 to run")
	}

	tmpDir := t.TempDir()
	t.Setenv("ADDER_TRAY_CONFIG_DIR", filepath.Join(tmpDir, "config"))
	t.Setenv("ADDER_TRAY_LOG_DIR", filepath.Join(tmpDir, "logs"))

	finder := &setup.AppBinaryFinder{}

	// Try to register service if not registered and binary exists
	status, _ := setup.ServiceStatusCheck()
	if status == setup.ServiceNotRegistered {
		bin, err := finder.Find()
		if err != nil {
			t.Skipf("skipping simulation: service not registered and binary not found: %v", err)
		}
		// Register temporary service for test
		err = setup.RegisterService(setup.ServiceConfig{
			BinaryPath: bin,
		})
		if err != nil {
			t.Skipf("skipping simulation: failed to register service: %v", err)
		}
		defer func() {
			_ = setup.UnregisterService()
		}()
	}

	// 1. Initialize App
	fyneApp := test.NewApp()
	a, err := NewApp(fyneApp)
	require.NoError(t, err)

	fmt.Println("\n--- STARTING SIMULATION: Pressing 'Start' ---")

	// 2. Action: Start Service
	fmt.Println("[Step 1] Calling setup.StartService()...")
	// Ensure we clean up the system-level service after the test
	defer func() {
		fmt.Println("[Cleanup] Stopping service and disconnecting...")
		_ = setup.StopService()
		a.conn.Disconnect()
	}()

	err = setup.StartService()
	if err != nil {
		fmt.Printf("! StartService failed: %v\n", err)
	} else {
		fmt.Println("✓ StartService returned success (or already running)")
	}

	// 3. Action: Connect to API
	fmt.Println("[Step 2] Calling a.conn.Connect()...")
	err = a.conn.Connect()
	if err != nil {
		fmt.Printf("! a.conn.Connect() failed: %v\n", err)
	} else {
		fmt.Println("✓ a.conn.Connect() initiated")
	}

	// 4. Verification: Wait for connection
	fmt.Println("[Step 3] Waiting for StatusConnected...")
	success := false
	for i := 0; i < 10; i++ {
		status := a.conn.status.Status()
		fmt.Printf("Current Status: %s\n", status)
		if status == StatusConnected {
			success = true
			break
		}
		time.Sleep(1 * time.Second)
	}

	if success {
		fmt.Println("✓ SUCCESS: Tray is connected to Sidecar API!")
	} else {
		fmt.Println("✗ FAILURE: Could not connect to Sidecar API within 10s.")
	}

	assert.True(t, success, "Should be connected to sidecar")
	fmt.Println("--- SIMULATION COMPLETE ---")
}
