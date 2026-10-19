package ui

import (
	"strings"

	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"
	"github.com/rivo/uniseg"
)

// fit truncates or pads s to exactly w cells. All width math goes through ansi.StringWidth.
func fit(s string, w int) string {
	s = ansi.Truncate(s, max(w, 0), "…")
	return s + strings.Repeat(" ", max(w-ansi.StringWidth(s), 0))
}

// cut is fit without the ellipsis, for splicing.
func cut(s string, w int) string {
	s = ansi.Truncate(s, max(w, 0), "")
	return s + strings.Repeat(" ", max(w-ansi.StringWidth(s), 0))
}

func center(s string, w int) string {
	return fit(strings.Repeat(" ", max((w-ansi.StringWidth(s))/2, 0))+s, w)
}

// row is an inner line of a pane: its padding columns, left, and right aligned in the iw
// cells between them, the gaps in fill (the selection, or plain). Left is cut first.
func row(iw int, fill lipgloss.Style, left, right string) string {
	rw := ansi.StringWidth(right)
	if rw > 0 {
		left = ansi.Truncate(left, max(iw-rw-1, 0), "…")
	} else {
		left = ansi.Truncate(left, iw, "…")
	}
	gap := max(iw-ansi.StringWidth(left)-rw, 0)
	return fill.Render(" ") + left + fill.Render(strings.Repeat(" ", gap)) + right + fill.Render(" ")
}

// rowStyles are the text, title and secondary styles of a row, and its fill.
func rowStyles(sel bool) (t, T, s, fill lipgloss.Style) {
	if sel {
		return selFill, selTitle, selDim, selFill
	}
	return text, title, dim, lipgloss.NewStyle()
}

func borderOf(focused bool) lipgloss.Style {
	if focused {
		return focusBorder
	}
	return borderSt
}

// box is a rounded pane w×h around inner lines (each w-2 cells; missing ones are blank):
// the title from column 2, a right label (mode, spinner, count) and a bottom label ending
// two cells before the corners. The title is cut before the right label.
func box(w, h int, bst lipgloss.Style, title, right, bottom string, inner []string) []string {
	if w < 6 || h < 2 {
		return nil
	}
	label := func(s string) string {
		if s == "" {
			return ""
		}
		return " " + s + " "
	}
	room := w - 6
	if right != "" {
		right = ansi.Truncate(right, room-1, "…")
		room -= ansi.StringWidth(right) + 3
	}
	t, r := label(ansi.Truncate(title, max(room, 1), "…")), label(right)
	dashes := w - 4 - ansi.StringWidth(t) - ansi.StringWidth(r)
	out := []string{bst.Render("╭─") + t + bst.Render(strings.Repeat("─", max(dashes, 0))) + r + bst.Render("─╮")}
	side := bst.Render("│")
	for i := range h - 2 {
		s := ""
		if i < len(inner) {
			s = inner[i]
		}
		if ansi.StringWidth(s) != w-2 {
			s = fit(s, w-2)
		}
		out = append(out, side+s+side)
	}
	b := label(ansi.Truncate(bottom, w-6, "…"))
	return append(out, bst.Render("╰"+strings.Repeat("─", max(w-3-ansi.StringWidth(b), 0)))+b+bst.Render("─╯"))
}

// pad is a plain inner line: the padding columns around s, cut or padded to iw.
func pad(s string, iw int) string { return " " + fit(s, iw) + " " }

// place draws lines over screen at column x, row y.
func place(screen, lines []string, x, y, w int) {
	for i, l := range lines {
		if y+i < 0 || y+i >= len(screen) {
			continue
		}
		s := screen[y+i]
		lw := ansi.StringWidth(l)
		screen[y+i] = cut(cut(s, x)+l+ansi.TruncateLeft(s, x+lw, ""), w)
	}
}

// dimAll fades the screen behind an overlay to the border colour. Kitty image placeholders
// become blanks: in another colour they would name another image.
func dimAll(screen []string) {
	for i, l := range screen {
		var b strings.Builder
		state := -1
		for rest := ansi.Strip(l); rest != ""; {
			var g string
			g, rest, _, state = uniseg.FirstGraphemeClusterInString(rest, state)
			if strings.HasPrefix(g, "\U0010EEEE") {
				g = " "
			}
			b.WriteString(g)
		}
		screen[i] = dimmed.Render(b.String())
	}
}

// overlay dims the screen and centres lines over it.
func overlay(screen, lines []string, w int) {
	dimAll(screen)
	if len(lines) == 0 {
		return
	}
	place(screen, lines, (w-ansi.StringWidth(lines[0]))/2, max((len(screen)-len(lines))/2, 0), w)
}
