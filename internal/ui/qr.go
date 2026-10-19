package ui

import (
	"strings"

	"charm.land/lipgloss/v2"
	"rsc.io/qr"
)

// qrBlock draws link as a QR code, two modules per cell with half blocks, in black on white
// whatever the terminal's colors (scanners don't read inverted codes).
func qrBlock(link string) string {
	code, err := qr.Encode(link, qr.L)
	if err != nil {
		return link
	}
	const quiet = 2 // the spec's 4 wastes rows; phone scanners are fine with 2
	n := code.Size + 2*quiet
	black := func(x, y int) bool { return code.Black(x-quiet, y-quiet) }
	st := lipgloss.NewStyle().Foreground(lipgloss.Color("#000000")).Background(lipgloss.Color("#ffffff"))
	var rows []string
	for y := 0; y < n; y += 2 {
		var b strings.Builder
		for x := 0; x < n; x++ {
			switch top, bot := black(x, y), black(x, y+1); {
			case top && bot:
				b.WriteString("█")
			case top:
				b.WriteString("▀")
			case bot:
				b.WriteString("▄")
			default:
				b.WriteString(" ")
			}
		}
		rows = append(rows, st.Render(b.String()))
	}
	return strings.Join(rows, "\n")
}
