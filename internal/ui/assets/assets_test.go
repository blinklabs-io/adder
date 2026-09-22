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
	"image"
	"image/color"
	_ "image/png"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func nonTransparentBounds(img image.Image) image.Rectangle {
	b := img.Bounds()
	minX, minY, maxX, maxY := b.Max.X, b.Max.Y, b.Min.X, b.Min.Y
	found := false
	for y := b.Min.Y; y < b.Max.Y; y++ {
		for x := b.Min.X; x < b.Max.X; x++ {
			_, _, _, a := img.At(x, y).RGBA()
			if a > 0 {
				found = true
				if x < minX {
					minX = x
				}
				if x > maxX {
					maxX = x
				}
				if y < minY {
					minY = y
				}
				if y > maxY {
					maxY = y
				}
			}
		}
	}
	if !found {
		return image.Rectangle{}
	}
	return image.Rect(minX, minY, maxX+1, maxY+1)
}

func TestGetIcon_PreservesAspect(t *testing.T) {
	res := GetIcon(64, nil)
	require.NotNil(t, res)

	img, _, err := image.Decode(bytes.NewReader(res.Content()))
	require.NoError(t, err)
	assert.Equal(t, image.Rect(0, 0, 64, 64), img.Bounds())

	nb := nonTransparentBounds(img)
	// Logo illustration is 285x373. Within 64x64, height should be 64 and
	// width should be approximately 48, centered horizontally with margins.
	assert.Equal(t, 64, nb.Dy())
	assert.InDelta(t, 48, nb.Dx(), 2)
	assert.InDelta(t, 8, nb.Min.X, 2)
}

func TestGetGrayscaleIcon_PreservesAspect(t *testing.T) {
	res := GetGrayscaleIcon(64)
	require.NotNil(t, res)

	img, _, err := image.Decode(bytes.NewReader(res.Content()))
	require.NoError(t, err)
	assert.Equal(t, image.Rect(0, 0, 64, 64), img.Bounds())

	nb := nonTransparentBounds(img)
	assert.Equal(t, 64, nb.Dy())
	assert.InDelta(t, 48, nb.Dx(), 2)
}

func TestEventIcons(t *testing.T) {
	block := GetBlockIcon(32)
	assert.NotNil(t, block)

	gov := GetGovernanceIcon(32)
	assert.NotNil(t, gov)

	tx := GetTransactionIcon(32)
	assert.NotNil(t, tx)
}

func TestFitRect(t *testing.T) {
	src := image.Rect(0, 0, 285, 373)
	fitted := fitRect(src, 64)
	assert.Equal(t, 64, fitted.Dy())
	assert.Equal(t, 48, fitted.Dx())
	assert.Equal(t, 8, fitted.Min.X)
	assert.Equal(t, 0, fitted.Min.Y)

	wide := image.Rect(0, 0, 400, 200)
	fittedWide := fitRect(wide, 100)
	assert.Equal(t, 100, fittedWide.Dx())
	assert.Equal(t, 50, fittedWide.Dy())
	assert.Equal(t, 0, fittedWide.Min.X)
	assert.Equal(t, 25, fittedWide.Min.Y)

	empty := fitRect(image.Rectangle{}, 50)
	assert.Equal(t, image.Rect(0, 0, 50, 50), empty)
}

func TestApplyTint(t *testing.T) {
	c := color.RGBA{R: 200, G: 100, B: 50, A: 255}
	tint := color.RGBA{R: 255, G: 0, B: 0, A: 255}
	tinted := applyTint(c, tint)
	r, g, b, a := tinted.RGBA()
	assert.Equal(t, uint32(0xffff), a)
	assert.True(t, r > 0)
	assert.Equal(t, uint32(0), g)
	assert.Equal(t, uint32(0), b)
}
