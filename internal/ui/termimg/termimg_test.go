package termimg

import (
	"image"
	"image/color"
	"strings"
	"testing"

	"github.com/charmbracelet/x/ansi"
)

// Both modes must take exactly cols cells per row, or they break the layout.
func TestWidths(t *testing.T) {
	img := image.NewRGBA(image.Rect(0, 0, 64, 48))
	img.Set(1, 1, color.RGBA{255, 0, 0, 255})
	cols, rows := Size(64, 48, 40, 10)
	if cols != 26 || rows != 10 {
		t.Fatalf("size %dx%d", cols, rows)
	}
	for name, lines := range map[string][]string{"blocks": DrawBlocks(img, cols, rows), "kitty": Placeholders(7, cols, rows)} {
		if len(lines) != rows {
			t.Fatalf("%s: %d rows", name, len(lines))
		}
		for _, l := range lines {
			if w := ansi.StringWidth(l); w != cols {
				t.Fatalf("%s: row is %d cells, want %d", name, w, cols)
			}
		}
	}
	if s := Transmit(7, img, cols, rows); !strings.HasPrefix(s, "\x1b_Ga=T,U=1,f=100,q=2,i=7,c=26,r=10,m=0;") {
		t.Fatalf("transmit: %.60q", s)
	}
}
