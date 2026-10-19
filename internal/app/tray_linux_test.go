package app

import (
	"bytes"
	"image/png"
	"os"
	"testing"

	"github.com/wailsapp/wails/v3/pkg/application"
)

func TestTrayPNGLinux(t *testing.T) {
	data, err := os.ReadFile("../../build/tray.png")
	if err != nil {
		t.Fatal(err)
	}
	img, err := png.Decode(bytes.NewReader(trayPNG(data, trayColors[""])))
	if err != nil {
		t.Fatal(err)
	}
	b := img.Bounds()
	if b.Dx() != b.Dy() {
		t.Fatalf("not square: %v", b)
	}
	// the tile is solid even with nothing captured, so the icon never fades into the panel
	if _, _, _, a := img.At(b.Dx()/2, 0).RGBA(); a != 0xffff {
		t.Fatalf("tile edge alpha %#x, want opaque", a)
	}
	// the white cat is centred: its drawn columns leave equal margins (within a pixel)
	left, right := b.Dx(), 0
	for y := 0; y < b.Dy(); y++ {
		for x := 0; x < b.Dx(); x++ {
			if r, _, _, _ := img.At(x, y).RGBA(); r>>8 > 0xe0 {
				left, right = min(left, x), max(right, x)
			}
		}
	}
	if d := left - (b.Dx() - 1 - right); d < -1 || d > 1 {
		t.Fatalf("off-centre: margins %d and %d", left, b.Dx()-1-right)
	}
}

func TestCheckLinux(t *testing.T) {
	item := application.NewMenuItem(tickOff + "GIA")
	check(item, true)
	if item.Label() != "✓\u2009GIA" {
		t.Fatalf("ticked: %q", item.Label())
	}
	check(item, false)
	if item.Label() != "\u2800\u2009GIA" {
		t.Fatalf("unticked: %q", item.Label())
	}
}

func TestDotPNGLinux(t *testing.T) {
	img, err := png.Decode(bytes.NewReader(dotPNG("good")))
	if err != nil || img.Bounds().Dx() != 16 {
		t.Fatalf("decode: %v %v", err, img.Bounds())
	}
	if r, g, _, a := img.At(8, 8).RGBA(); a == 0 || g < r {
		t.Errorf("centre should be the solid green: %v", img.At(8, 8))
	}
	if _, _, _, a := img.At(0, 0).RGBA(); a != 0 {
		t.Errorf("corner should be clear: %v", img.At(0, 0))
	}
	blank, _ := png.Decode(bytes.NewReader(dotPNG("")))
	if _, _, _, a := blank.At(8, 8).RGBA(); a != 0 {
		t.Errorf("the blank should be clear: %v", blank.At(8, 8))
	}
}
