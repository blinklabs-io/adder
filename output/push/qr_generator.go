package push

import (
	"encoding/json"
	"fmt"
	"net/http"
	"text/template"

	"github.com/gin-gonic/gin"
)

type QRValue struct {
	ApiEndpoint string `json:"apiEndpoint"`
}

func generateQRPage(apiEndpoint string) gin.HandlerFunc {
	return func(c *gin.Context) {
		qrValue, err := json.Marshal(QRValue{
			ApiEndpoint: apiEndpoint,
		})
		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{
				"error": err.Error(),
			})
			return
		}

		qrValueEscaped := template.JSEscapeString(string(qrValue))

		htmlContent := fmt.Sprintf(`
	<!DOCTYPE html>
	<html lang="en">
	<head>
			<meta charset="UTF-8">
			<meta name="viewport" content="width=device-width, initial-scale=1.0">
			<title>QR Code</title>
			<link href="https://cdn.jsdelivr.net/npm/tailwindcss@2.2.19/dist/tailwind.min.css" rel="stylesheet">
			<script src="https://cdn.jsdelivr.net/npm/qrious@latest/dist/qrious.min.js"></script>
	</head>
	<body class="bg-gray-100 h-screen flex items-center justify-center">
		<!-- QR Code Container -->
		<div class="bg-white p-8 rounded-lg shadow-md text-center">
				<p class="text-xl mb-4">Scan QR code with Snek Mobile to connect to the Snek Server on <span class="font-semibold">%s</span></p>
				<canvas id="qrCanvas" class="mx-auto"></canvas>
		</div>
	
		<!-- Generate QR Code using JavaScript -->
		<script>
				window.onload = function() {
						const canvas = document.getElementById('qrCanvas');
						const qrValue = "%s";
						const qr = new QRious({
								element: canvas,
								value: qrValue,
								size: 250 
						});
				}
		</script>
	</body>
	</html>
	`, apiEndpoint, qrValueEscaped)

		c.Data(http.StatusOK, "text/html; charset=utf-8", []byte(htmlContent))
	}
}
