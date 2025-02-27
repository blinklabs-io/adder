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
	if registrar, ok := interface{}(pushPlugin).(api.APIRouteRegistrar); ok {
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

	// Check the status code
	assert.Equal(t, http.StatusNotFound, rr.Code, "Expected status not found")
	// You can also check the response body, headers, etc.
	// TODO check for JSON response (#338)
	// assert.Equal(t, `{"fcmToken":"someToken"}`, rr.Body.String())
}
