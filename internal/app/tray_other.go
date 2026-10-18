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
	var box image.Rectangle // the drawn part
	b := img.Bounds()
	for y := b.Min.Y; y < b.Max.Y; y++ {
		for x := b.Min.X; x < b.Max.X; x++ {
			if _, _, _, alpha := img.At(x, y).RGBA(); alpha > 0 {
				box = box.Union(image.Rect(x, y, x+1, y+1))
			}
		}
	}
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

// mix is channel v a fraction a of the way to white.
func mix(v uint8, a float64) uint8 { return uint8(float64(v) + (0xff-float64(v))*a) }

// detailSep sits between a menu item's label and its detail, and is how a label is told from
// what decorate appended to it last time. On Windows a tab, which puts the rest of the label in the
// menu's right-hand column, where macOS draws its details.
var detailSep = map[bool]string{true: "\t", false: " · "}[runtime.GOOS == "windows"]

// decorate folds each detail into its item's label ("Proxy · 香港", "网络质量 · 86 ms"), since
// Linux and Windows menus show plain text: a pill keeps its text, a dot becomes ● or ○, the mode
// badge becomes the mode's name, and icons are skipped.
func sectionHeaders([]*application.MenuItem) {} // titles are separators here (build)

func decorate(details, _ map[*application.MenuItem]string) {
	for item, detail := range details {
		base, _, _ := strings.Cut(item.Label(), detailSep)
		if text := detailText(detail); text != "" {
			base += detailSep + text
		}
		if base != item.Label() {
			item.SetLabel(base)
		}
	}
}

func detailText(detail string) string {
	if len(detail) > 1 && detail[0] == 1 { // a pill: "\x01color|text"
		_, detail, _ = strings.Cut(detail[1:], "|")
	}
	if len(detail) > 1 && detail[0] == 2 { // a dot: "\x02color|text", a solid or (grey) hollow one before the text
		color, text, _ := strings.Cut(detail[1:], "|")
		mark := "●"
		if color == "gray" {
			mark = "○"
		}
		return mark + " " + text
	}
	for key, letter := range modeBadges {
		if letter == detail {
			return modeLabel(key)
		}
	}
	return detail
}
