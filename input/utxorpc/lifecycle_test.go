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

package utxorpc

import (
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/blinklabs-io/adder/plugintest"
	"golang.org/x/net/http2"
	"golang.org/x/net/http2/h2c"
)

func TestLifecycleContract(t *testing.T) {
	server := httptest.NewServer(
		h2c.NewHandler(
			http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				_, _ = io.Copy(io.Discard, r.Body)
				<-r.Context().Done()
			}),
			&http2.Server{},
		),
	)
	t.Cleanup(server.Close)
	plugintest.Lifecycle(t, New(WithURL(server.URL)))
}

func TestFailedStartContract(t *testing.T) { plugintest.FailedStart(t, New()) }
