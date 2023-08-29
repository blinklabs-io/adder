package push_test

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/blinklabs-io/snek/api"
	"github.com/blinklabs-io/snek/output/push"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
)

func setupRouter() *gin.Engine {
	apiInstance := api.New(false)
	p := &push.PushOutput{}
	p.RegisterRoutes()
	return apiInstance.Engine()
}

func TestStoreFCMToken(t *testing.T) {
	router := setupRouter()

	t.Run("Valid JSON input", func(t *testing.T) {
		jsonStr := `{"FCMToken": "abcd1234"}`
		req, _ := http.NewRequest("POST", "/fcm", strings.NewReader(jsonStr))
		req.Header.Set("Content-Type", "application/json")
		w := httptest.NewRecorder()
		router.ServeHTTP(w, req)

		assert.Equal(t, http.StatusCreated, w.Code)

		tokens := push.GetFcmTokens()
		assert.Contains(t, tokens, "abcd1234")
	})

	t.Run("Store 2 tokens JSON input", func(t *testing.T) {
		jsonStr := `{"FCMToken": "abcd1234"}`
		req, _ := http.NewRequest("POST", "/fcm", strings.NewReader(jsonStr))
		req.Header.Set("Content-Type", "application/json")
		w := httptest.NewRecorder()
		router.ServeHTTP(w, req)

		assert.Equal(t, http.StatusCreated, w.Code)

		tokens := push.GetFcmTokens()
		assert.Contains(t, tokens, "abcd1234")

		jsonStr = `{"FCMToken": "abcd0000"}`
		req, _ = http.NewRequest("POST", "/fcm", strings.NewReader(jsonStr))
		req.Header.Set("Content-Type", "application/json")
		w = httptest.NewRecorder()
		router.ServeHTTP(w, req)

		assert.Equal(t, http.StatusCreated, w.Code)

		tokens = push.GetFcmTokens()
		assert.Contains(t, tokens, "abcd0000")
	})

	t.Run("Invalid JSON input", func(t *testing.T) {
		jsonStr := `{"invalid_field": "abcd1234"}`
		req, _ := http.NewRequest("POST", "/fcm", strings.NewReader(jsonStr))
		req.Header.Set("Content-Type", "application/json")
		w := httptest.NewRecorder()
		router.ServeHTTP(w, req)

		assert.Equal(t, http.StatusBadRequest, w.Code)
	})
}

func TestReadFCMToken(t *testing.T) {
	router := setupRouter()

	// Prepopulate the FCMTokens map for the read test
	push.GetFcmTokens()["abcd1234"] = "abcd1234"

	t.Run("Token exists", func(t *testing.T) {
		req, _ := http.NewRequest("GET", "/fcm/abcd1234", nil)
		w := httptest.NewRecorder()
		router.ServeHTTP(w, req)

		assert.Equal(t, http.StatusOK, w.Code)
		assert.Contains(t, w.Body.String(), "abcd1234")
	})

	t.Run("Token does not exist", func(t *testing.T) {
		req, _ := http.NewRequest("GET", "/fcm/nonexistenttoken", nil)
		w := httptest.NewRecorder()
		router.ServeHTTP(w, req)

		assert.Equal(t, http.StatusNotFound, w.Code)
	})
}

func TestDeleteFCMToken(t *testing.T) {
	router := setupRouter()

	// Prepopulate the FCMTokens map for the delete test
	push.GetFcmTokens()["abcd1234"] = "abcd1234"

	t.Run("Token exists and is deleted", func(t *testing.T) {
		req, _ := http.NewRequest("DELETE", "/fcm/abcd1234", nil)
		w := httptest.NewRecorder()
		router.ServeHTTP(w, req)

		assert.Equal(t, http.StatusNoContent, w.Code)

		tokens := push.GetFcmTokens()
		_, exists := tokens["abcd1234"]
		assert.False(t, exists, "Token should be deleted")
	})

	t.Run("Token does not exist", func(t *testing.T) {
		req, _ := http.NewRequest("DELETE", "/fcm/nonexistenttoken", nil)
		w := httptest.NewRecorder()
		router.ServeHTTP(w, req)

		assert.Equal(t, http.StatusNotFound, w.Code)
	})

	t.Run("Deleting already deleted token", func(t *testing.T) {
		req, _ := http.NewRequest("DELETE", "/fcm/abcd1234", nil)
		w := httptest.NewRecorder()
		router.ServeHTTP(w, req)

		assert.Equal(t, http.StatusNotFound, w.Code)
	})
}
