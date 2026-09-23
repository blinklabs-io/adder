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

package notifyjson

import (
	"errors"
	"io"
	"testing"

	"github.com/blinklabs-io/adder/plugintest"
	"github.com/stretchr/testify/require"
)

func TestLifecycleContract(t *testing.T) {
	plugintest.Lifecycle(
		t,
		New(WithConfigPath(writeTestConfig(t)), WithWriter(io.Discard)),
	)
}

func TestFailedStartContract(
	t *testing.T,
) {
	plugintest.FailedStart(t, New(WithConfigPath("")))
}

type failedWriter struct{}

func (failedWriter) Write(
	[]byte,
) (int, error) {
	return 0, errors.New("writer unavailable")
}

func TestFailedInitialWriteUnwindsEngine(t *testing.T) {
	o := New(WithConfigPath(writeTestConfig(t)), WithWriter(failedWriter{}))
	plugintest.FailedStart(t, o)
	o.writer = io.Discard
	require.NoError(t, o.Start())
	require.NoError(t, o.Stop())
}
