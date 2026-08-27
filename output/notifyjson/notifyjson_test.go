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
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/blinklabs-io/adder/event"
	"github.com/blinklabs-io/adder/tray/setup"
	"github.com/stretchr/testify/require"
)

type lockedBuffer struct {
	mu sync.Mutex
	b  bytes.Buffer
}

func (b *lockedBuffer) Write(data []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.b.Write(data)
}

func (b *lockedBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.b.String()
}

func writeTestConfig(
	t *testing.T,
	mutate ...func(*setup.NotificationConfig),
) string {
	t.Helper()
	cfg := setup.DefaultNotificationConfig()
	cfg.Monitor.Everything = true
	cfg.RateLimit.Max = -1
	for _, fn := range mutate {
		fn(&cfg)
	}
	data, err := json.Marshal(cfg)
	require.NoError(t, err)
	path := filepath.Join(t.TempDir(), "notifications.json")
	require.NoError(t, os.WriteFile(path, data, 0o600))
	return path
}

func testTransaction() event.Event {
	return event.Event{
		Type:      event.TypeTransaction,
		Timestamp: time.Now(),
		Context: map[string]any{
			"transactionHash": "0123456789abcdef",
		},
		Payload: map[string]any{},
	}
}

func testNativeBlock() event.Event {
	return event.Event{
		Type:      event.TypeBlock,
		Timestamp: time.Now(),
		Context: event.BlockContext{
			BlockNumber: 13_335_000,
		},
		Payload: event.BlockEvent{
			BlockHash: "84ee913d2d3aaaaabbbb255af401",
		},
	}
}

type nativeTestOutput struct {
	Address string `json:"address"`
	Amount  uint64 `json:"amount"`
}

type nativeTestTransaction struct {
	Outputs []nativeTestOutput `json:"outputs"`
}

func TestOutputEmitsStatusAndMatchedNotification(t *testing.T) {
	var output lockedBuffer
	o := New(
		WithConfigPath(writeTestConfig(t)),
		WithWriter(&output),
		WithStaleAfter(time.Hour),
	)
	require.NoError(t, o.Start())
	o.InputChan() <- testTransaction()
	require.Eventually(t, func() bool {
		text := output.String()
		return strings.Contains(text, `"status":"connected"`) &&
			strings.Contains(text, `"kind":"notification"`) &&
			strings.Contains(text, `"ruleId":"everything-tx"`)
	}, 2*time.Second, 10*time.Millisecond)
	require.NoError(t, o.Stop())

	for _, line := range strings.Split(strings.TrimSpace(output.String()), "\n") {
		var record map[string]any
		require.NoError(t, json.Unmarshal([]byte(line), &record), line)
		require.Equal(t, float64(schemaVersion), record["schemaVersion"])
	}
}

func TestOutputReportsStaleAndRecovery(t *testing.T) {
	var output lockedBuffer
	o := New(
		WithConfigPath(writeTestConfig(t)),
		WithWriter(&output),
		WithStaleAfter(25*time.Millisecond),
	)
	require.NoError(t, o.Start())
	o.InputChan() <- testTransaction()
	require.Eventually(t, func() bool {
		return strings.Contains(output.String(), `"status":"stale"`)
	}, 2*time.Second, 10*time.Millisecond)
	o.InputChan() <- testTransaction()
	require.Eventually(t, func() bool {
		return strings.Count(output.String(), `"status":"connected"`) == 2 &&
			strings.Contains(output.String(), `Reconnected to mainnet`)
	}, 2*time.Second, 10*time.Millisecond)
	require.NoError(t, o.Stop())
}

func TestOutputNormalizesNativeEventBeforeRendering(t *testing.T) {
	var output lockedBuffer
	o := New(
		WithConfigPath(writeTestConfig(t)),
		WithWriter(&output),
		WithStaleAfter(time.Hour),
	)
	require.NoError(t, o.Start())
	o.InputChan() <- testNativeBlock()
	require.Eventually(t, func() bool {
		return strings.Contains(
			output.String(),
			`"body":"Block #13335000 (84ee913d...255af401) minted."`,
		)
	}, 2*time.Second, 10*time.Millisecond)
	require.NotContains(t, output.String(), "{{")
	require.NoError(t, o.Stop())
}

func TestNormalizeEventUsesJSONFieldNames(t *testing.T) {
	normalized, err := normalizeEvent(testNativeBlock())
	require.NoError(t, err)
	context, ok := normalized.Context.(map[string]any)
	require.True(t, ok)
	require.Equal(t, float64(13_335_000), context["blockNumber"])
	payload, ok := normalized.Payload.(map[string]any)
	require.True(t, ok)
	require.Equal(t,
		"84ee913d2d3aaaaabbbb255af401",
		payload["blockHash"],
	)
}

func TestNormalizeEventPreservesJSONShapedEvents(t *testing.T) {
	evt := testTransaction()
	normalized, err := normalizeEvent(evt)
	require.NoError(t, err)
	require.Equal(t, evt, normalized)
}

func BenchmarkNormalizeEvent(b *testing.B) {
	for _, test := range []struct {
		name  string
		event event.Event
	}{
		{name: "native", event: testNativeBlock()},
		{name: "json-shaped", event: testTransaction()},
	} {
		b.Run(test.name, func(b *testing.B) {
			for b.Loop() {
				if _, err := normalizeEvent(test.event); err != nil {
					b.Fatal(err)
				}
			}
		})
	}
}

func TestOutputNormalizesNativeEventBeforeTargetMatching(t *testing.T) {
	var output lockedBuffer
	configPath := writeTestConfig(t, func(cfg *setup.NotificationConfig) {
		cfg.Monitor.Everything = false
		cfg.Monitor.Wallets = []string{"addr1watched"}
	})
	o := New(
		WithConfigPath(configPath),
		WithWriter(&output),
		WithStaleAfter(time.Hour),
	)
	require.NoError(t, o.Start())
	o.InputChan() <- event.Event{
		Type: event.TypeTransaction,
		Context: event.TransactionContext{
			TransactionHash: "0123456789abcdef",
		},
		Payload: nativeTestTransaction{Outputs: []nativeTestOutput{{
			Address: "addr1watched",
			Amount:  5_000_000,
		}}},
	}
	require.Eventually(t, func() bool {
		return strings.Contains(
			output.String(),
			`"ruleId":"wallet-in"`,
		) && strings.Contains(
			output.String(),
			`"body":"Received 5 ADA at addr1watched."`,
		)
	}, 2*time.Second, 10*time.Millisecond)
	require.NoError(t, o.Stop())
}

func TestOutputRequiresConfig(t *testing.T) {
	o := New()
	require.ErrorContains(t, o.Start(), "config path")
	require.NoError(t, o.Stop())
}
