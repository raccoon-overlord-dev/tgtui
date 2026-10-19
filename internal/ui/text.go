package ui

import (
	"strings"

	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"
	"github.com/rivo/uniseg"

	"github.com/raccoon-overlord-dev/tgtui/internal/core"
)

const fDim core.Format = 1 << 15 // UI only: the [Photo] tag of media messages

type cell struct {
	s   string // one grapheme
	w   int
	f   core.Format
	url string
}

func styleOf(f core.Format) lipgloss.Style {
	st := lipgloss.NewStyle()
	switch {
	case f&core.Code != 0:
		st = codeSt
	case f&core.Link != 0:
		st = linkSt
	case f&core.Mention != 0:
		st = mentionSt
	case f&core.Quote != 0:
		st = quoteSt
	case f&fDim != 0:
		st = dim
	}
	if f&core.Bold != 0 {
		st = st.Bold(true)
	}
	if f&core.Italic != 0 {
		st = st.Italic(true)
	}
	if f&core.Underline != 0 {
		st = st.Underline(true)
	}
	if f&core.Strike != 0 {
		st = st.Strikethrough(true)
	}
	return st
}

// renderText wraps text to width cells, styled by spans. Spoilers show as ▒ unless reveal.
// Wrapping happens on graphemes before styling, so a style never runs across a line break.
func renderText(text string, spans []core.Span, width int, reveal bool) []string {
	width = max(width, 1)
	var paras [][]cell
	var cur []cell
	state, b := -1, 0
	for rest := text; rest != ""; {
		var g string
		g, rest, _, state = uniseg.FirstGraphemeClusterInString(rest, state)
		c := cell{s: g, w: ansi.StringWidth(g)}
		for _, sp := range spans {
			if b >= sp.From && b < sp.To {
				c.f |= sp.Format
				if sp.URL != "" {
					c.url = sp.URL
				}
			}
		}
		b += len(g)
		if g == "\n" || g == "\r\n" {
			paras, cur = append(paras, cur), nil
			continue
		}
		if g == "\t" {
			c.s, c.w = "    ", 4
		}
		cur = append(cur, c)
	}
	paras = append(paras, cur)

	var out []string
	for _, p := range paras {
		quote := len(p) > 0 && p[0].f&core.Quote != 0
		w := width
		if quote {
			w -= 2
		}
		for _, l := range wrapCells(p, max(w, 1)) {
			s := drawCells(l, reveal)
			if quote {
				s = replyBar.Render("┃ ") + s
			}
			out = append(out, s)
		}
	}
	return out
}

// wrapCells breaks a paragraph into lines of at most w cells, at spaces when it can.
func wrapCells(p []cell, w int) [][]cell {
	var lines [][]cell
	var line []cell
	lw := 0
	for i := 0; i < len(p); {
		j := i // the word p[i:j] and its trailing spaces up to k
		for j < len(p) && p[j].s != " " {
			j++
		}
		k := j
		for k < len(p) && p[k].s == " " {
			k++
		}
		ww := 0
		for _, c := range p[i:j] {
			ww += c.w
		}
		if lw > 0 && lw+ww > w {
			lines, line, lw = append(lines, line), nil, 0
		}
		for _, c := range p[i:k] {
			if lw+c.w > w && c.s != " " { // a word longer than the line: hard break
				lines, line, lw = append(lines, line), nil, 0
			}
			if lw+c.w <= w {
				line, lw = append(line, c), lw+c.w
			}
		}
		i = k
	}
	return append(lines, line)
}

func drawCells(l []cell, reveal bool) string {
	var b strings.Builder
	for i := 0; i < len(l); {
		j := i
		var s strings.Builder
		for j < len(l) && l[j].f == l[i].f && l[j].url == l[i].url {
			if l[j].f&core.Spoiler != 0 && !reveal {
				s.WriteString(strings.Repeat("▒", l[j].w))
			} else {
				s.WriteString(l[j].s)
			}
			j++
		}
		out := s.String()
		switch {
		case l[i].f&core.Spoiler != 0 && !reveal:
			out = spoilerSt.Render(out)
		case l[i].f&core.Spoiler != 0:
			out = revealSt.Inherit(styleOf(l[i].f)).Render(out)
		case l[i].f != 0:
			out = styleOf(l[i].f).Render(out)
		}
		if l[i].url != "" {
			out = ansi.SetHyperlink(l[i].url) + out + ansi.ResetHyperlink()
		}
		b.WriteString(out)
		i = j
	}
	return b.String()
}
