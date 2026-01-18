// Copyright 2023 Blink Labs Software
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

package push

import (
	"encoding/json"
	"net/http"
	"os"
	"sync"

	_ "github.com/blinklabs-io/adder/docs"
	"github.com/blinklabs-io/adder/internal/logging"
	"github.com/gin-gonic/gin"
)

type TokenStore struct {
	FCMTokens    map[string]string `json:"fcm_tokens"`
	filePath     string
	mu           sync.RWMutex
	persistMutex sync.Mutex
}

// TokenRequest represents a request containing an FCM token.
type TokenRequest struct {
	FCMToken string `json:"fcmToken" binding:"required"`
}

// Token represents an FCM token object.
//
//	@Produce	json
//	@Success	200	{object}	TokenResponse
type TokenResponse struct {
	FCMToken string `json:"fcmToken"`
}

// ErrorResponse represents a generic error response.
type ErrorResponse struct {
	Error string `json:"error"`
}

var fcmStore *TokenStore

func init() {
	fcmStore = newTokenStore("")
}

func newTokenStore(filePath string) *TokenStore {
	store := &TokenStore{
		FCMTokens: make(map[string]string),
		filePath:  filePath,
	}
	// Load existing tokens if persistence is enabled
	if filePath != "" {
		store.loadTokens()
	}
	return store
}

// SetPersistenceFile configures the file path for token persistence
// If called with a non-empty path, tokens will be loaded from and saved to this file
func SetPersistenceFile(filePath string) {
	if fcmStore == nil {
		fcmStore = newTokenStore(filePath)
		return
	}
	fcmStore.persistMutex.Lock()
	fcmStore.filePath = filePath
	fcmStore.persistMutex.Unlock()
	if filePath != "" {
		fcmStore.loadTokens()
	}
}

func getTokenStore() *TokenStore {
	return fcmStore
}

// loadTokens loads tokens from the persistence file
func (s *TokenStore) loadTokens() {
	s.persistMutex.Lock()
	defer s.persistMutex.Unlock()

	if s.filePath == "" {
		return
	}

	logger := logging.GetLogger()

	data, err := os.ReadFile(s.filePath)
	if err != nil {
		if os.IsNotExist(err) {
			// File doesn't exist yet, that's fine for first run
			logger.Debug("FCM token persistence file does not exist yet", "path", s.filePath)
			return
		}
		logger.Error("failed to read FCM tokens from file", "error", err, "path", s.filePath)
		return
	}

	var loadedStore struct {
		FCMTokens map[string]string `json:"fcm_tokens"`
	}
	if err := json.Unmarshal(data, &loadedStore); err != nil {
		logger.Error("failed to parse FCM tokens from file", "error", err, "path", s.filePath)
		return
	}

	s.mu.Lock()
	if loadedStore.FCMTokens != nil {
		s.FCMTokens = loadedStore.FCMTokens
	}
	s.mu.Unlock()

	logger.Info("loaded FCM tokens from persistence file", "count", len(loadedStore.FCMTokens), "path", s.filePath)
}

// saveTokens saves tokens to the persistence file
func (s *TokenStore) saveTokens() {
	s.persistMutex.Lock()
	defer s.persistMutex.Unlock()

	if s.filePath == "" {
		return
	}

	logger := logging.GetLogger()

	s.mu.RLock()
	data, err := json.MarshalIndent(struct {
		FCMTokens map[string]string `json:"fcm_tokens"`
	}{
		FCMTokens: s.FCMTokens,
	}, "", "  ")
	s.mu.RUnlock()

	if err != nil {
		logger.Error("failed to marshal FCM tokens", "error", err)
		return
	}

	if err := os.WriteFile(s.filePath, data, 0o600); err != nil {
		logger.Error("failed to write FCM tokens to file", "error", err, "path", s.filePath)
		return
	}

	logger.Debug("saved FCM tokens to persistence file", "path", s.filePath)
}

// @Summary		Store FCM Token
// @Description	Store a new FCM token
// @Accept			json
// @Produce		json
// @Param			body	body		TokenRequest	true	"FCM Token Request"
// @Success		201		{string}	string			"Created"
// @Failure		400		{object}	ErrorResponse
// @Router			/fcm [post]
func storeFCMToken(c *gin.Context) {
	var req TokenRequest

	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	store := getTokenStore()
	if store == nil {
		c.JSON(
			http.StatusInternalServerError,
			gin.H{"error": "failed getting token store"},
		)
		return
	}
	store.mu.Lock()
	store.FCMTokens[req.FCMToken] = req.FCMToken
	store.mu.Unlock()
	store.saveTokens()
	c.Status(http.StatusCreated)
}

// @Summary		Get FCM Token
// @Description	Get an FCM token by its value
// @Accept			json
// @Produce		json
// @Param			token	path		string	true	"FCM Token"
// @Success		200		{object}	TokenResponse
// @Failure		404		{object}	ErrorResponse
// @Router			/fcm/{token} [get]
func readFCMToken(c *gin.Context) {
	token := c.Param("token")
	store := getTokenStore()
	if store == nil {
		c.JSON(
			http.StatusInternalServerError,
			gin.H{"error": "failed getting token store"},
		)
		return
	}
	store.mu.RLock()
	storedToken, exists := store.FCMTokens[token]
	store.mu.RUnlock()
	if !exists {
		c.Status(http.StatusNotFound)
		return
	}
	c.JSON(http.StatusOK, gin.H{"fcmToken": storedToken})
}

// @Summary		Delete FCM Token
// @Description	Delete an FCM token by its value
// @Accept			json
// @Produce		json
// @Param			token	path		string	true	"FCM Token"
// @Success		204		{string}	string	"No Content"
// @Failure		404		{object}	ErrorResponse
// @Router			/fcm/{token} [delete]
func deleteFCMToken(c *gin.Context) {
	token := c.Param("token")
	store := getTokenStore()
	if store == nil {
		c.JSON(
			http.StatusInternalServerError,
			gin.H{"error": "failed getting token store"},
		)
		return
	}
	store.mu.Lock()
	_, exists := store.FCMTokens[token]
	if exists {
		delete(store.FCMTokens, token)
	}
	store.mu.Unlock()
	if exists {
		store.saveTokens()
		c.Status(http.StatusNoContent)
	} else {
		c.Status(http.StatusNotFound)
	}
}

// GetFcmTokens returns a copy of the current in-memory FCM tokens
func GetFcmTokens() map[string]string {
	store := getTokenStore()
	if store == nil {
		return make(map[string]string)
	}
	store.mu.RLock()
	defer store.mu.RUnlock()
	// Return a copy to avoid race conditions
	tokens := make(map[string]string, len(store.FCMTokens))
	for k, v := range store.FCMTokens {
		tokens[k] = v
	}
	return tokens
}
