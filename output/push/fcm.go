package push

import (
	"net/http"

	"github.com/blinklabs-io/snek/api"
	"github.com/gin-gonic/gin"
)

// TODO implement FCM storage
var fcmTokens = make(map[string]string)

var tokenRequest struct {
	FCMToken string `json:"fcmToken" binding:"required"`
}

type Fcm struct {
}

func (f *Fcm) RegisterRoutes() {
	apiInstance := api.GetInstance()

	apiInstance.AddRoute("POST", "/fcm", storeFCMToken)
	apiInstance.AddRoute("POST", "/fcm/", storeFCMToken)

	apiInstance.AddRoute("GET", "/fcm/:token", readFCMToken)
	apiInstance.AddRoute("GET", "/fcm/:token/", readFCMToken)

	apiInstance.AddRoute("DELETE", "/fcm/:token", deleteFCMToken)
	apiInstance.AddRoute("DELETE", "/fcm/:token/", deleteFCMToken)
}

// TODO: update this with actual storage and implementation
func storeFCMToken(c *gin.Context) {
	// Handle the creation of an FCM token
	if err := c.ShouldBindJSON(&tokenRequest); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	// Use the FCM token as the map key
	fcmTokens[tokenRequest.FCMToken] = tokenRequest.FCMToken
	c.Status(http.StatusCreated)
}

// TODO: update this with actual storage and implementation
func readFCMToken(c *gin.Context) {
	token := c.Param("token")
	storedToken, exists := fcmTokens[token]
	if !exists {
		c.Status(http.StatusNotFound)
		return
	}
	c.JSON(http.StatusOK, gin.H{"fcmToken": storedToken})
}

// TODO: update this with actual storage and implementation
func deleteFCMToken(c *gin.Context) {
	token := c.Param("token")
	// Check if the token exists
	_, exists := fcmTokens[token]
	if !exists {
		c.Status(http.StatusNotFound)
		return
	}
	// Delete the FCM token
	delete(fcmTokens, token)
	c.Status(http.StatusNoContent)
}
