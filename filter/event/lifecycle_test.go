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

package event

import (
	"context"
	"testing"
	"time"

	"github.com/blinklabs-io/adder/plugintest"
	"github.com/stretchr/testify/require"
)

func TestLifecycleContract(t *testing.T) {
	plugintest.Lifecycle(t, New())
}

func TestWorkerHonorsParentCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	p := New()
	require.NoError(t, p.StartContext(ctx))
	cancel()
	exited := make(chan struct{})
	go func() { p.Wait(); close(exited) }()
	select {
	case <-exited:
	case <-time.After(time.Second):
		t.Fatal("filter worker ignored parent cancellation")
	}
	require.NoError(t, p.Stop())
}
