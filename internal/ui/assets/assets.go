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

package assets

import (
	"bytes"
	_ "embed"
	"fmt"
	"image"
	"image/color"
	"image/draw"
	"image/png"
	"log/slog"
	"time"

	"fyne.io/fyne/v2"
	xdraw "golang.org/x/image/draw"
)

//go:embed adder-illustration.png
var adderIllustrationData []byte

var baseLogo image.Image

func init() {
	img, _, err := image.Decode(bytes.NewReader(adderIllustrationData))
	if err != nil {
		slog.Error("failed to decode embedded logo", "error", err)
		return
	}
	baseLogo = img
}

// GetIcon returns a fyne.Resource with the branded logo filtered by a tint
// color.
func GetIcon(size uint, tint color.Color) fyne.Resource {
	// Use a coarse timestamp (10-second window) for the resource name.
	// This provides enough cache-busting to fix macOS tray rendering quirks
	// without causing unbounded memory growth in Fyne's resource registry
	// during long-running sessions or high-frequency watchdog updates.
	v := time.Now().Unix() / 10

	name := fmt.Sprintf("icon_%d_color_%d.png", size, v)
	if tint != nil {
		r, g, b, a := tint.RGBA()
		name = fmt.Sprintf("icon_%d_%d_%d_%d_%d_%d.png", size, r, g, b, a, v)
	}

	if baseLogo == nil {
		data, _ := encodePNG(generateFallbackImage(size, tint))
		return fyne.NewStaticResource("fallback_"+name, data)
	}

	// Resize using golang.org/x/image/draw (CatmullRom for high quality)
	rect := image.Rect(0, 0, int(size), int(size))
	img := image.NewRGBA(rect)
	xdraw.CatmullRom.Scale(
		img,
		rect,
		baseLogo,
		baseLogo.Bounds(),
		xdraw.Over,
		nil,
	)

	if tint == nil {
		r, g, b, _ := img.At(img.Bounds().Dx()/2, img.Bounds().Dy()/2).RGBA()
		slog.Debug("generated color icon",
			"name", name,
			"center_r", r,
			"center_g", g,
			"center_b", b)
		data, _ := encodePNG(img)
		return fyne.NewStaticResource(name, data)
	}

	bounds := img.Bounds()
	dst := image.NewRGBA(bounds)
	for y := bounds.Min.Y; y < bounds.Max.Y; y++ {
		for x := bounds.Min.X; x < bounds.Max.X; x++ {
			c := img.At(x, y)
			dst.Set(x, y, applyTint(c, tint))
		}
	}

	r, g, b, _ := dst.At(bounds.Dx()/2, bounds.Dy()/2).RGBA()
	slog.Debug("generated tinted icon",
		"name", name,
		"center_r", r,
		"center_g", g,
		"center_b", b)
	data, _ := encodePNG(dst)
	return fyne.NewStaticResource(name, data)
}

// GetGrayscaleIcon returns a grayscale version of the icon.
func GetGrayscaleIcon(size uint) fyne.Resource {
	name := fmt.Sprintf("icon_%d_gray.png", size)
	if baseLogo == nil {
		return GetIcon(size, color.Gray{Y: 128})
	}

	// Resize using golang.org/x/image/draw
	rect := image.Rect(0, 0, int(size), int(size))
	img := image.NewRGBA(rect)
	xdraw.CatmullRom.Scale(
		img,
		rect,
		baseLogo,
		baseLogo.Bounds(),
		xdraw.Over,
		nil,
	)

	bounds := img.Bounds()
	dst := image.NewRGBA(bounds)
	for y := bounds.Min.Y; y < bounds.Max.Y; y++ {
		for x := bounds.Min.X; x < bounds.Max.X; x++ {
			r, g, b, a := img.At(x, y).RGBA()
			// RGBA returns values in range [0, 0xffff], so uint16 cast is safe.
			// gosec G115 is a false positive here as values never overflow
			// uint16.
			gray := uint32(0.299*float64(r) + 0.587*float64(g) + 0.114*float64(b))
			dst.Set(x, y, color.RGBA64{
				R: uint16(gray), //nolint:gosec // range checked
				G: uint16(gray), //nolint:gosec // range checked
				B: uint16(gray), //nolint:gosec // range checked
				A: uint16(a),    //nolint:gosec // range checked
			})
		}
	}
	r, g, b, _ := dst.At(bounds.Dx()/2, bounds.Dy()/2).RGBA()
	slog.Debug("generated grayscale icon",
		"name", name,
		"center_r", r,
		"center_g", g,
		"center_b", b)
	data, _ := encodePNG(dst)
	return fyne.NewStaticResource(name, data)
}

// GetBlockIcon returns a blue-tinted icon for block events.
func GetBlockIcon(size uint) fyne.Resource {
	return GetIcon(size, color.RGBA{R: 52, G: 152, B: 219, A: 255}) // Peter River Blue
}

// GetGovernanceIcon returns a gold-tinted icon for governance events.
func GetGovernanceIcon(size uint) fyne.Resource {
	return GetIcon(size, color.RGBA{R: 241, G: 196, B: 15, A: 255}) // Sun Flower Yellow
}

// GetTransactionIcon returns a green-tinted icon for transaction events.
func GetTransactionIcon(size uint) fyne.Resource {
	return GetIcon(size, color.RGBA{R: 46, G: 204, B: 113, A: 255}) // Emerald Green
}

// GetFullResource returns the original illustration as a fyne.Resource.
func GetFullResource() fyne.Resource {
	if baseLogo == nil {
		data, _ := encodePNG(generateFallbackImage(256, nil))
		return fyne.NewStaticResource("fallback.png", data)
	}
	return fyne.NewStaticResource("adder-illustration.png", adderIllustrationData)
}

func applyTint(c color.Color, tint color.Color) color.Color {
	r, g, b, a := c.RGBA()
	tr, tg, tb, _ := tint.RGBA()

	// Simple multiplicative tint
	// Each component is in range [0, 0xffff], so the shifted result
	// is guaranteed to fit in uint16.
	return color.RGBA64{
		R: uint16((r * tr) >> 16), //nolint:gosec // range checked
		G: uint16((g * tg) >> 16), //nolint:gosec // range checked
		B: uint16((b * tb) >> 16), //nolint:gosec // range checked
		A: uint16(a),              //nolint:gosec // range checked
	}
}

func encodePNG(img image.Image) ([]byte, error) {
	var buf bytes.Buffer
	if err := png.Encode(&buf, img); err != nil {
		return nil, fmt.Errorf("failed to encode PNG: %w", err)
	}
	return buf.Bytes(), nil
}

func generateFallbackImage(size uint, c color.Color) image.Image {
	if c == nil {
		c = color.RGBA{128, 128, 128, 255}
	}
	img := image.NewRGBA(image.Rect(0, 0, int(size), int(size)))
	draw.Draw(img, img.Bounds(), &image.Uniform{c}, image.Point{}, draw.Src)
	return img
}
