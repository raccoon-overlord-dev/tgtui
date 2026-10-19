package ui

import (
	"strconv"
	"strings"
	"unicode"

	tea "charm.land/bubbletea/v2"

	"github.com/raccoon-overlord-dev/tgtui/internal/core"
)

// mention is the @ autocomplete for the word being typed.
type mention struct {
	start int // where the @word starts in the composer
	query string
	items []core.Found
	sel   int
}

type mentionMsg struct {
	query string
	items []core.Found
}

// wordStart is where the word before the cursor begins.
func (t *textInput) wordStart() int {
	i := t.cur
	for i > 0 && !unicode.IsSpace(t.r[i-1]) {
		i--
	}
	return i
}

// replaceWord replaces r[from:cur] with s.
func (t *textInput) replaceWord(from int, s string) {
	t.save("")
	ins := []rune(s)
	t.r = append(append(t.r[:from:from], ins...), t.r[t.cur:]...)
	t.cur, t.anchor = from+len(ins), -1
}

// checkMention opens, updates or closes the autocomplete after the composer changed.
func (m *model) checkMention() tea.Cmd {
	t := &m.comp
	start := t.wordStart()
	if start >= t.cur || t.r[start] != '@' {
		m.mention = nil
		return nil
	}
	q := string(t.r[start+1 : t.cur])
	if mn := m.mention; mn != nil && mn.start == start && mn.query == q {
		return nil
	}
	m.mention = &mention{start: start, query: q}
	return func() tea.Msg {
		items, _ := m.c.Members(q)
		return mentionMsg{q, items}
	}
}

// mentionKey handles the keys of an open autocomplete; ok is false for the others.
func (m *model) mentionKey(key string) (cmd tea.Cmd, ok bool) {
	mn := m.mention
	if mn == nil || len(mn.items) == 0 {
		return nil, false
	}
	switch key {
	case "up", "ctrl+p":
		mn.sel = max(mn.sel-1, 0)
	case "down", "ctrl+n":
		mn.sel = min(mn.sel+1, len(mn.items)-1)
	case "tab", "enter":
		f := mn.items[mn.sel]
		ins := "@" + f.Username + " "
		if f.Username == "" { // no username: a mention by name, as markup core turns into a mention
			ins = "[" + strings.NewReplacer("[", "", "]", "").Replace(f.Title) + "](tg://user?id=" + strconv.FormatInt(f.UserID, 10) + ") "
		}
		m.comp.replaceWord(mn.start, ins)
		m.mention = nil
	case "esc":
		m.mention = nil
	default:
		return nil, false
	}
	return nil, true
}

// mentionBox is the @ autocomplete: up to 5 members in a box that rises from the
// composer, at most w cells wide.
func (m *model) mentionBox(w int) []string {
	mn := m.mention
	if mn == nil || len(mn.items) == 0 || m.focus != focusChat || !m.insert {
		return nil
	}
	w = min(34, w)
	var inner []string
	for i, f := range mn.items[:min(len(mn.items), 5)] {
		t, T, s, fill := rowStyles(i == mn.sel)
		if i != mn.sel {
			T = t
		}
		l := T.Render(f.Title)
		if f.Username != "" {
			l += fill.Render("  ") + s.Render("@"+f.Username)
		}
		inner = append(inner, row(w-4, fill, l, ""))
	}
	return box(w, len(inner)+2, focusBorder, "", "", "", inner)
}
