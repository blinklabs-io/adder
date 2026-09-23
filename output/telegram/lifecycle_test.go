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

package telegram

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/blinklabs-io/adder/plugintest"
	"github.com/go-telegram/bot"
	"github.com/stretchr/testify/require"
)

func TestLifecycleContract(t *testing.T) {
	server := httptest.NewServer(
		http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			_, _ = io.Copy(io.Discard, r.Body)
			w.Header().Set("Content-Type", "application/json")
			switch {
			case strings.HasSuffix(r.URL.Path, "/getUpdates"):
				<-r.Context().Done()
			case strings.HasSuffix(r.URL.Path, "/getMe"):
				_, _ = w.Write(
					[]byte(
						`{"ok":true,"result":{"id":123,"is_bot":true,"username":"test"}}`,
					),
				)
			default:
				_, _ = w.Write([]byte(`{"ok":true,"result":true}`))
			}
		}),
	)
	t.Cleanup(server.Close)
	client, err := bot.New(
		"123:test",
		bot.WithSkipGetMe(),
		bot.WithServerURL(server.URL),
		bot.WithHTTPClient(time.Second, server.Client()),
	)
	require.NoError(t, err)
	plugintest.Lifecycle(t, &TelegramOutput{bot: client, chatID: 123})
}

func TestFailedStartContract(
	t *testing.T,
) {
	plugintest.FailedStart(t, &TelegramOutput{})
}

func TestStopCancelsAuthorization(t *testing.T) {
	entered, canceled := make(chan struct{}), make(chan struct{})
	server := httptest.NewServer(
		http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			_, _ = io.Copy(io.Discard, r.Body)
			close(entered)
			<-r.Context().Done()
			close(canceled)
		}),
	)
	defer server.Close()
	client, err := bot.New(
		"123:test",
		bot.WithSkipGetMe(),
		bot.WithServerURL(server.URL),
	)
	require.NoError(t, err)
	p := &TelegramOutput{bot: client, chatID: 123}
	started := make(chan error, 1)
	go func() { started <- p.Start() }()
	select {
	case <-entered:
	case <-time.After(2 * time.Second):
		t.Fatal("authorization request did not arrive")
	}
	stopped := make(chan error, 1)
	go func() { stopped <- p.Stop() }()
	select {
	case err := <-started:
		require.ErrorIs(t, err, context.Canceled)
	case <-time.After(2 * time.Second):
		t.Fatal("authorization was not canceled")
	}
	select {
	case err := <-stopped:
		require.NoError(t, err)
	case <-time.After(2 * time.Second):
		t.Fatal("Stop did not finish")
	}
	select {
	case <-canceled:
	case <-time.After(2 * time.Second):
		t.Fatal("authorization request survived Stop")
	}
	require.False(t, p.Running())
	require.Nil(t, p.InputChan())
}
