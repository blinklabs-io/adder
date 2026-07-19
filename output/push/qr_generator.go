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
	"html/template"
	"log/slog"
	"net/http"
)

// qrPageTemplate renders the QR connection page. Using html/template ensures
// the endpoint value (derived from the request Host header) is escaped for
// its context — HTML text in the span and a JS string literal in the script —
// preventing reflected XSS.
var qrPageTemplate = template.Must(template.New("qr").Parse(`
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
	<div class="bg-white p-8 rounded-lg shadow-md text-center">
		<p class="text-xl mb-4">Scan QR code with Adder Mobile to connect to the Adder Server on <span class="font-semibold">{{.ApiEndpoint}}</span></p>
		<canvas id="qrCanvas" class="mx-auto"></canvas>
	</div>

	<script>
		window.onload = function() {
			const canvas = document.getElementById('qrCanvas');
			const qrValue = { apiEndpoint: "{{.ApiEndpoint}}" };
			const qr = new QRious({
				element: canvas,
				value: JSON.stringify(qrValue),
				size: 250
			});
		}
	</script>
</body>
</html>
`))

// @Summary		Generate QR Setup Page
// @Description	Generates an interactive HTML page containing a QR code representing the local API FCM endpoint. Used by the Adder Tray desktop application during onboarding setup.
// @Produce		text/html
// @Success		200	{string}	string	"Interactive HTML Onboarding Page"
// @Router			/v1/qrcode [get]
func generateQRPage(apiEndpoint string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		fullApiEndpoint := r.Host + apiEndpoint
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		if err := qrPageTemplate.Execute(w, struct {
			ApiEndpoint string
		}{ApiEndpoint: fullApiEndpoint}); err != nil {
			// Response may be partially written; log and stop.
			slog.Debug(
				"failed to render QR page template",
				"error", err,
				"endpoint", fullApiEndpoint,
			)
			return
		}
	}
}
