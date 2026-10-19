//go:build linux

package app

import (
	"bytes"
	"image"
	"image/color"
	"image/png"
	"math"

	"github.com/wailsapp/wails/v3/pkg/application"
)

// textDots: Linux draws a state's dot as the item's icon (markState), small and in colour, as the
// text's ● was large and the same grey whatever the state.
const textDots = false

// dotColors are dot's colours, as the macOS menu draws them.
var dotColors = map[string]color.NRGBA{
	"good": {0x34, 0xc7, 0x59, 0xff}, "warn": {0xff, 0x9f, 0x0a, 0xff}, "bad": {0xff, 0x3b, 0x30, 0xff},
	"gray": {0x8e, 0x8e, 0x93, 0xff}, "accent": {0x0a, 0x84, 0xff, 0xff},
}

var (
	dotPNGs = map[string][]byte{}                // colour ("" for the blank) -> its icon
	marked  = map[*application.MenuItem]string{} // the colour each item's icon was last set to
)

// markState gives item a small dot of color at its left, or a blank as wide for an item with none,
// so the labels stay in line; only when that changes, as each change has the panel fetch the whole
// menu again. Called from decorate, one goroutine at a time.
// ponytail: marked keeps the items of menus since rebuilt; clear it on rebuild if that ever grows
func markState(item *application.MenuItem, c string) {
	if was, ok := marked[item]; ok && was == c {
		return
	}
	marked[item] = c
	item.SetBitmap(dotPNG(c))
}

// dotPNG is a 16×16 icon: an antialiased dot 8 px across in colour c, or nothing for "".
func dotPNG(c string) []byte {
	if b, ok := dotPNGs[c]; ok {
		return b
	}
	img := image.NewNRGBA(image.Rect(0, 0, 16, 16))
	if fill, ok := dotColors[c]; ok {
		for y := range 16 {
			for x := range 16 {
				d := math.Hypot(float64(x)+0.5-8, float64(y)+0.5-8)
				a := fill
				a.A = uint8(float64(fill.A) * min(max(4.5-d, 0), 1))
				img.SetNRGBA(x, y, a)
			}
		}
	}
	var buf bytes.Buffer
	_ = png.Encode(&buf, img)
	dotPNGs[c] = buf.Bytes()
	return dotPNGs[c]
}
