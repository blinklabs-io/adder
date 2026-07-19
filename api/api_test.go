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

package api_test

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/blinklabs-io/adder/api"
	"github.com/blinklabs-io/adder/output/push"
	"github.com/stretchr/testify/assert"
)

func TestRouteRegistration(t *testing.T) {
	// Initialize the API and set it to debug mode for testing
	apiInstance := api.New(true)

	// Check if Fcm implements APIRouteRegistrar and register its routes
	pushPlugin := &push.PushOutput{}
	if registrar, ok := any(pushPlugin).(api.APIRouteRegistrar); ok {
		registrar.RegisterRoutes()
	} else {
		t.Fatal("pushPlugin does NOT implement APIRouteRegistrar")
	}

	// Build the path from the configured base so the test is independent of
	// whether WithGroup was applied to the process-wide singleton.
	//
	// Both a registered handler (missing token) and an unregistered path
	// return 404, but only ServeMux's default writes a body. An empty body
	// proves readFCMToken was reached, i.e. the route is registered.
	req, err := http.NewRequest(
		http.MethodGet,
		apiInstance.BasePath()+"/fcm/someToken",
		nil,
	)
	if err != nil {
		t.Fatal(err)
	}
	rr := httptest.NewRecorder()
	apiInstance.Engine().ServeHTTP(rr, req)
	assert.Equal(t, http.StatusNotFound, rr.Code, "expected 404 for missing token")
	assert.Empty(
		t,
		rr.Body.String(),
		"empty body proves the handler ran, not the ServeMux 404",
	)
}
