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
	"os"
	"os/exec"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestStartAndReap(t *testing.T) {
	if os.Getenv("ADDER_REAP_HELPER") == "1" {
		return
	}

	cmd := exec.Command(os.Args[0], "-test.run=^TestStartAndReap$")
	cmd.Env = append(os.Environ(), "ADDER_REAP_HELPER=1")
	done := make(chan error, 1)
	err := startAndReap(cmd, func(err error) {
		done <- err
	})
	require.NoError(t, err)

	select {
	case err := <-done:
		require.NoError(t, err)
	case <-time.After(5 * time.Second):
		t.Fatal("child process was not waited")
	}
}

func TestStartAndReapReturnsStartError(t *testing.T) {
	cmd := exec.Command("/path/that/does/not/exist")
	err := startAndReap(cmd, nil)
	require.Error(t, err)
}
