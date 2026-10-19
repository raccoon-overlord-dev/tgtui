// Package termimg draws images in the terminal: kitty graphics with Unicode placeholders
// (kitty, Ghostty, also through tmux), or half blocks in truecolor anywhere else.
package termimg

import (
	"bytes"
	"encoding/base64"
	"fmt"
	"image"
	"image/png"
	"os"
	"strings"

	_ "image/jpeg"

	"golang.org/x/image/draw"
	_ "golang.org/x/image/webp"
)

type Mode string

const (
	Off    Mode = "off"
	Blocks Mode = "blocks"
	Kitty  Mode = "kitty"
)

// Detect picks the mode for this terminal; setting is the config value (auto | kitty | blocks | off).
// Kitty graphics only where the terminal is known to have them: guessing wrong prints garbage.
// ponytail: from the environment, no terminal query; the setting overrides it.
func Detect(setting string) Mode {
	if setting != "" && setting != "auto" {
		return Mode(setting)
	}
	term, prog := os.Getenv("TERM"), os.Getenv("TERM_PROGRAM")
	switch {
	case term == "xterm-kitty", term == "xterm-ghostty", prog == "ghostty":
		return Kitty
	case os.Getenv("TMUX") != "" && (os.Getenv("KITTY_WINDOW_ID") != "" || os.Getenv("GHOSTTY_RESOURCES_DIR") != ""):
		return Kitty // tmux in kitty or Ghostty: passthrough (needs `set -g allow-passthrough on`)
	}
	return Blocks
}

// Decode reads a JPEG, PNG or WebP file, or the bytes of one.
func Decode(path string, data []byte) (image.Image, error) {
	if path != "" {
		f, err := os.Open(path)
		if err != nil {
			return nil, err
		}
		defer f.Close()
		img, _, err := image.Decode(f)
		return img, err
	}
	img, _, err := image.Decode(bytes.NewReader(data))
	return img, err
}

// Size fits an image of w×h pixels in at most maxCols×maxRows cells, keeping its aspect
// (a cell is about twice as tall as wide).
func Size(w, h, maxCols, maxRows int) (cols, rows int) {
	if w <= 0 || h <= 0 {
		return 0, 0
	}
	rows = maxRows
	cols = rows * 2 * w / h
	if cols > maxCols {
		cols = maxCols
		rows = max(cols*h/(2*w), 1)
	}
	return max(cols, 1), rows
}

func scale(img image.Image, w, h int) *image.RGBA {
	dst := image.NewRGBA(image.Rect(0, 0, w, h))
	draw.ApproxBiLinear.Scale(dst, dst.Bounds(), img, img.Bounds(), draw.Src, nil)
	return dst
}

// DrawBlocks draws img in cols×rows cells, two pixels per cell with ▀ (foreground on top).
func DrawBlocks(img image.Image, cols, rows int) []string {
	px := scale(img, cols, rows*2)
	out := make([]string, rows)
	for y := range rows {
		var b strings.Builder
		for x := range cols {
			t, u := px.RGBAAt(x, 2*y), px.RGBAAt(x, 2*y+1)
			fmt.Fprintf(&b, "\x1b[38;2;%d;%d;%d;48;2;%d;%d;%dm▀", t.R, t.G, t.B, u.R, u.G, u.B)
		}
		b.WriteString("\x1b[m")
		out[y] = b.String()
	}
	return out
}

// Transmit is the escape sequence that sends img to the terminal as image id, with a
// virtual placement of cols×rows cells for Placeholders to show.
func Transmit(id uint32, img image.Image, cols, rows int) string {
	b := img.Bounds()
	if w := min(b.Dx(), cols*12); w < b.Dx() { // about a cell's pixels: no need to send more
		img = scale(img, w, max(b.Dy()*w/b.Dx(), 1))
	}
	var buf bytes.Buffer
	png.Encode(&buf, img)
	data := base64.StdEncoding.EncodeToString(buf.Bytes())
	var out strings.Builder
	for i := 0; i < len(data); i += 4096 {
		chunk, more := data[i:min(i+4096, len(data))], 0
		if i+4096 < len(data) {
			more = 1
		}
		keys := fmt.Sprintf("m=%d", more)
		if i == 0 {
			keys = fmt.Sprintf("a=T,U=1,f=100,q=2,i=%d,c=%d,r=%d,m=%d", id, cols, rows, more)
		}
		out.WriteString(Passthrough("\x1b_G" + keys + ";" + chunk + "\x1b\\"))
	}
	return out.String()
}

// DeleteAll frees every image tgtui sent (on leaving a chat).
func DeleteAll() string { return Passthrough("\x1b_Ga=d,d=A,q=2\x1b\\") }

// Passthrough wraps an escape sequence for tmux, which otherwise keeps it from the
// terminal (it also needs `set -g allow-passthrough on`).
func Passthrough(seq string) string {
	if os.Getenv("TMUX") == "" {
		return seq
	}
	return "\x1bPtmux;" + strings.ReplaceAll(seq, "\x1b", "\x1b\x1b") + "\x1b\\"
}

// Placeholders are the text cells that show image id: U+10EEEE plus row and column
// diacritics, in a foreground color that encodes the id. They are plain text to the
// renderer, so they scroll and redraw like the rest of the screen.
func Placeholders(id uint32, cols, rows int) []string {
	out := make([]string, rows)
	color := fmt.Sprintf("\x1b[38;2;%d;%d;%dm", id>>16&0xff, id>>8&0xff, id&0xff)
	for y := range min(rows, len(diacritics)) {
		var b strings.Builder
		b.WriteString(color)
		for x := range min(cols, len(diacritics)) {
			b.WriteRune(0x10EEEE)
			b.WriteRune(diacritics[y])
			b.WriteRune(diacritics[x])
		}
		b.WriteString("\x1b[39m")
		out[y] = b.String()
	}
	return out
}
