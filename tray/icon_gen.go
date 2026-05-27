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

package tray

import (
	"image/color"

	"fyne.io/fyne/v2"
	"github.com/blinklabs-io/adder/internal/ui/assets"
)

// GetStatusIcon returns a fyne.Resource with the branded logo filtered by status.
func GetStatusIcon(s Status) fyne.Resource {
	switch s {
	case StatusConnected:
		return assets.GetIcon(64, nil) // Full color
	case StatusStarting, StatusReconnecting:
		// Tint Yellow/Amber
		return assets.GetIcon(64, color.RGBA{R: 255, G: 215, B: 0, A: 255})
	case StatusError:
		// Tint Red
		return assets.GetIcon(64, color.RGBA{R: 255, G: 0, B: 0, A: 255})
	case StatusStopped:
		return assets.GetGrayscaleIcon(64)
	default: // StatusStopped, etc.
		return assets.GetGrayscaleIcon(64)
	}
}

// DefaultIconBytes returns the raw bytes for the default gray icon.
func DefaultIconBytes() []byte {
	return GetStatusIcon(StatusStopped).Content()
}
