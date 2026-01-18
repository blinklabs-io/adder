package api_test

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/blinklabs-io/adder/api"
	"github.com/blinklabs-io/adder/output/push"
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

	// Create a test request to one of the registered routes
	req, err := http.NewRequest(http.MethodGet, "/v1/fcm/someToken", nil)
	if err != nil {
		t.Fatal(err)
	}

	// Record the response
	rr := httptest.NewRecorder()
	apiInstance.Engine().ServeHTTP(rr, req)

	// Check the status code - token doesn't exist so we expect 404
	assert.Equal(t, http.StatusNotFound, rr.Code, "Expected status not found")
}

func TestHealthcheckEndpoint(t *testing.T) {
	// Initialize the API and set it to debug mode for testing
	apiInstance := api.New(true)

	// Create a test request to the healthcheck endpoint
	req, err := http.NewRequest(http.MethodGet, "/healthcheck", nil)
	require.NoError(t, err)

	// Record the response
	rr := httptest.NewRecorder()
	apiInstance.Engine().ServeHTTP(rr, req)

	// Check the status code
	assert.Equal(t, http.StatusOK, rr.Code, "Expected status OK")

	// Parse and validate JSON response
	var response map[string]interface{}
	err = json.Unmarshal(rr.Body.Bytes(), &response)
	require.NoError(t, err, "Response should be valid JSON")

	// Check that the "failed" field exists and is false
	failed, exists := response["failed"]
	assert.True(t, exists, "Response should contain 'failed' field")
	assert.Equal(t, false, failed, "Expected 'failed' to be false")
}

func TestPingEndpoint(t *testing.T) {
	// Initialize the API and set it to debug mode for testing
	apiInstance := api.New(true)

	// Create a test request to the ping endpoint
	req, err := http.NewRequest(http.MethodGet, "/ping", nil)
	require.NoError(t, err)

	// Record the response
	rr := httptest.NewRecorder()
	apiInstance.Engine().ServeHTTP(rr, req)

	// Check the status code
	assert.Equal(t, http.StatusOK, rr.Code, "Expected status OK")

	// Check response body
	assert.Equal(t, "pong", rr.Body.String(), "Expected 'pong' response")
}

// mockHealthChecker implements api.HealthChecker for testing
type mockHealthChecker struct {
	running bool
}

func (m *mockHealthChecker) IsRunning() bool {
	return m.running
}

func TestHealthcheckWithUnhealthyPipeline(t *testing.T) {
	// Initialize the API and set it to debug mode for testing
	apiInstance := api.New(true)

	// Clean up health checkers after test to prevent state leakage
	t.Cleanup(api.ResetHealthCheckers)

	// Register a mock health checker that reports as not running
	mock := &mockHealthChecker{running: false}
	api.RegisterHealthChecker(mock)

	// Create a test request to the healthcheck endpoint
	req, err := http.NewRequest(http.MethodGet, "/healthcheck", nil)
	require.NoError(t, err)

	// Record the response
	rr := httptest.NewRecorder()
	apiInstance.Engine().ServeHTTP(rr, req)

	// Check the status code - should be 503 Service Unavailable
	assert.Equal(t, http.StatusServiceUnavailable, rr.Code, "Expected status Service Unavailable")

	// Parse and validate JSON response
	var response map[string]interface{}
	err = json.Unmarshal(rr.Body.Bytes(), &response)
	require.NoError(t, err, "Response should be valid JSON")

	// Check that the "failed" field exists and is true
	failed, exists := response["failed"]
	assert.True(t, exists, "Response should contain 'failed' field")
	assert.Equal(t, true, failed, "Expected 'failed' to be true")

	// Check that the "reason" field exists
	reason, exists := response["reason"]
	assert.True(t, exists, "Response should contain 'reason' field")
	assert.Equal(t, "pipeline is not running", reason, "Expected reason to explain why unhealthy")
}
