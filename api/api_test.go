package api_test

import (
	"net/http"
	"net/http/httptest"
	"os"
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
		t.Error("pushPlugin does NOT implement APIRouteRegistrar")
		os.Exit(1)
	}

	// Create a test request to one of the registered routes
	req, err := http.NewRequest(http.MethodGet, "/v1/fcm/someToken", nil)
	if err != nil {
		t.Error(err)
		os.Exit(1)
	}

	// Record the response
	rr := httptest.NewRecorder()
	apiInstance.Engine().ServeHTTP(rr, req)

	// Check the status code
	assert.Equal(t, http.StatusNotFound, rr.Code, "Expected status not found")

	// You can also check the response body, headers, etc.
	// TODO check for JSON response
	// assert.Equal(t, `{"fcmToken":"someToken"}`, rr.Body.String())
}
