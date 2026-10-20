//go:build !darwin

package app

import (
	"bytes"
	"image"
	"image/color"
	"image/png"
	"math"
	"runtime"
	"strings"

	"github.com/wailsapp/wails/v3/pkg/application"
)

// trayColors are the tile behind the cat, one per state (drawing.state): grey while nothing is
// captured, still solid so it reads on any panel; pink through the system proxy; orange in partial
// enhancement; purple in enhanced mode.
var trayColors = map[string]color.NRGBA{"": {0x8e, 0x8e, 0x93, 0xff}, "proxy": {0xff, 0x6e, 0xb4, 0xff}, "partial": {0xff, 0x9f, 0x0a, 0xff}, "tun": {0x6a, 0x5a, 0xf9, 0xff}}

var trayState *string // the state the tray icon was last drawn for; nil before the first call

// setTrayIcon sets the cat on its state's tile, only when that changes: each icon goes over D-Bus
// on Linux.
func setTrayIcon(tray *application.SystemTray, icon []byte, state string) {
	if trayState != nil && *trayState == state {
		return
	}
	trayState = &state
	tray.SetIcon(trayPNG(icon, trayColors[state]))
}

// trayPNG redraws the macOS template cat (black, 50×48, off-centre) for a Linux or Windows tray: white,
// centred on a square tile of colour fill with rounded corners, since trays fit icons to a square.
func trayPNG(data []byte, fill color.NRGBA) []byte {
	img, err := png.Decode(bytes.NewReader(data))
	if err != nil {
		return data
	}
	box := drawnBox(img, 0)
	side := max(box.Dx(), box.Dy()) * 3 / 2 // the cat fills two thirds of the tile
	r := float64(side) / 4                  // corner radius
	out := image.NewNRGBA(image.Rect(0, 0, side, side))
	for y := range side {
		for x := range side {
			// distance from the pixel's centre to the tile's inner rectangle, antialiased over a pixel
			fx, fy := float64(x)+0.5, float64(y)+0.5
			d := math.Hypot(fx-min(max(fx, r), float64(side)-r), fy-min(max(fy, r), float64(side)-r))
			c := fill
			c.A = uint8(float64(fill.A) * min(max(r+0.5-d, 0), 1))
			out.SetNRGBA(x, y, c)
		}
	}
	dx, dy := (side-box.Dx())/2-box.Min.X, (side-box.Dy())/2-box.Min.Y
	for y := box.Min.Y; y < box.Max.Y; y++ {
		for x := box.Min.X; x < box.Max.X; x++ {
			_, _, _, alpha := img.At(x, y).RGBA()
			a := float64(alpha) / 0xffff
			c := out.NRGBAAt(x+dx, y+dy)
			c.R, c.G, c.B = mix(c.R, a), mix(c.G, a), mix(c.B, a)
			out.SetNRGBA(x+dx, y+dy, c)
		}
	}
	var buf bytes.Buffer
	if png.Encode(&buf, out) != nil {
		return data
	}
	return buf.Bytes()
}

// drawnBox is the bounding box of img's pixels with alpha (0–0xffff) above minAlpha: the drawn part.
func drawnBox(img image.Image, minAlpha uint32) image.Rectangle {
	var box image.Rectangle
	b := img.Bounds()
	for y := b.Min.Y; y < b.Max.Y; y++ {
		for x := b.Min.X; x < b.Max.X; x++ {
			if _, _, _, a := img.At(x, y).RGBA(); a > minAlpha {
				box = box.Union(image.Rect(x, y, x+1, y+1))
			}
		}
	}
	return box
}

// mix is channel v a fraction a of the way to white.
func mix(v uint8, a float64) uint8 { return uint8(float64(v) + (0xff-float64(v))*a) }

// detailSep sits between a menu item's label and its detail, and is how a label is told from
// what decorate appended to it last time. On Windows a tab, which puts the rest of the label in the
// menu's right-hand column, where macOS draws its details.
var detailSep = map[bool]string{true: "\t", false: " · "}[runtime.GOOS == "windows"]

// decorate folds each detail into its item's label ("Proxy · 香港", "网络质量 · 86 ms"), since
// Linux and Windows menus show plain text: a dot becomes ● or ○, and icons are skipped.
func sectionHeaders([]*application.MenuItem) {} // titles are separators here (build)

func onMenuThread(fn func()) { fn() }

// onTrayOpen does nothing here: on Linux every label change has the panel fetch the whole menu
// again, and the network quality keeps its minute.
func onTrayOpen(*application.Menu, func()) bool { return true }

func decorate(details, _ map[*application.MenuItem]string) {
	for item, detail := range details {
		markState(item, dotColor(detail))
		base, _, _ := strings.Cut(item.Label(), detailSep)
		if text := detailText(detail); text != "" {
			base += detailSep + text
		}
		if base != item.Label() {
			item.SetLabel(base)
		}
	}
}

// dotColor is a dot detail's colour (dot), or "".
func dotColor(detail string) string {
	if len(detail) > 1 && detail[0] == 2 {
		color, _, _ := strings.Cut(detail[1:], "|")
		return color
	}
	return ""
}

func detailText(detail string) string {
	if len(detail) > 1 && detail[0] == 2 { // a dot: "\x02color|text"; where the item can't show it in colour (markState), a solid or (grey) hollow one before the text
		color, text, _ := strings.Cut(detail[1:], "|")
		if !textDots {
			return text
		}
		mark := "●"
		if color == "gray" {
			mark = "○"
		}
		return mark + " " + text
	}
	return detail
}
