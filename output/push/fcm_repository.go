// Copyright 2023 Blink Labs, LLC.
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
	"net/http"

	"github.com/gin-gonic/gin"
)

type TokenStore struct {
	FCMTokens map[string]string
}

// TokenRequest represents a request containing an FCM token.
type TokenRequest struct {
	FCMToken string `json:"fcmToken" binding:"required"`
}

// TODO add support for persistence
var fcmStore *TokenStore

func init() {
	fcmStore = newTokenStore()
}

func newTokenStore() *TokenStore {
	return &TokenStore{
		FCMTokens: make(map[string]string),
	}
}

func getTokenStore() *TokenStore {
	return fcmStore
}

func storeFCMToken(c *gin.Context) {
	var req TokenRequest

	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	store := getTokenStore()
	store.FCMTokens[req.FCMToken] = req.FCMToken
	c.Status(http.StatusCreated)
}

func readFCMToken(c *gin.Context) {
	token := c.Param("token")
	store := getTokenStore()
	storedToken, exists := store.FCMTokens[token]
	if !exists {
		c.Status(http.StatusNotFound)
		return
	}
	c.JSON(http.StatusOK, gin.H{"fcmToken": storedToken})
}

func deleteFCMToken(c *gin.Context) {
	token := c.Param("token")
	store := getTokenStore()
	_, exists := store.FCMTokens[token]
	if exists {
		delete(store.FCMTokens, token)
		c.Status(http.StatusNoContent)
	} else {
		c.Status(http.StatusNotFound)
	}
}

// GetFcmTokens returns the current in-memory FCM tokens
func GetFcmTokens() map[string]string {
	store := getTokenStore()
	return store.FCMTokens
}
