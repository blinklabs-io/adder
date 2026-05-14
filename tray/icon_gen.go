package tray

import (
	"bytes"
	"image"
	"image/color"
	"image/png"
)

// generateIcon creates a simple 16x16 circle icon in the given color.
func generateIcon(c color.Color) []byte {
	const size = 16
	img := image.NewRGBA(image.Rect(0, 0, size, size))
	radius := size / 2

	for y := 0; y < size; y++ {
		for x := 0; x < size; x++ {
			dx := x - radius
			dy := y - radius
			if dx*dx+dy*dy <= radius*radius {
				img.Set(x, y, c)
			}
		}
	}

	var buf bytes.Buffer
	_ = png.Encode(&buf, img)
	return buf.Bytes()
}
