package ui

import (
	"strings"

	tea "charm.land/bubbletea/v2"

	"github.com/raccoon-overlord-dev/tgtui/internal/core"
)

// chatMenu is a list of actions in a centred box: the chat menu (Telegram's ctrl+\, m in
// normal mode), or the delete dialog (del) with the message quoted above.
type chatMenu struct {
	title string
	quote string // del: the message
	del   bool
	busy  string // shown while an action runs
	items []menuItem
	sel   int
}

type menuItem struct {
	label string
	key   string       // its shortcut outside the menu, shown on the right
	act   func() error // nil: just closes
	done  string       // shown after it ran
}

func (m *model) openMenu() {
	id := m.selID
	if m.focus == focusChat {
		id = m.open
	}
	ch := m.c.Chat(id)
	if ch == nil {
		return
	}
	list := m.tabs()[m.tab].ID
	if m.focus == focusChat && !ch.In(list) {
		list = core.Main
	}
	pinned, archived := ch.Pinned(list), ch.In(core.Archive)
	read := "R"
	if !m.vim() {
		read = "ctrl+r"
	}
	m.menu = &chatMenu{title: m.chatName(ch), items: []menuItem{
		{"Mark as read", read, func() error { return m.c.MarkRead(id) }, "Marked as read"},
		{map[bool]string{true: "Unmute", false: "Mute"}[ch.Muted], "", func() error { return m.c.SetMuted(id, !ch.Muted) },
			map[bool]string{true: "Unmuted", false: "Muted"}[ch.Muted]},
		{map[bool]string{true: "Unpin", false: "Pin"}[pinned], "", func() error { return m.c.SetPinned(list, id, !pinned) },
			map[bool]string{true: "Unpinned", false: "Pinned"}[pinned]},
		{map[bool]string{true: "Move out of the archive", false: "Archive"}[archived], "", func() error { return m.c.SetArchived(id, !archived) },
			map[bool]string{true: "Moved out of the archive", false: "Archived"}[archived]},
	}}
}

// openDelete asks how to delete a message, with the choices TDLib allows for it.
func (m *model) openDelete(p propsMsg) {
	ids, quote := []int64{p.id}, ""
	if i, ok := m.indexOf(p.id); ok {
		quote = oneLine(label(m.win.Messages[i]))
	}
	mn := &chatMenu{title: "Delete message?", quote: quote, del: true}
	if p.props.DeleteForAll {
		mn.items = append(mn.items, menuItem{"Delete for everyone", "", func() error { return m.c.Delete(ids, true) }, "Deleted"})
	}
	if p.props.DeleteForSelf {
		mn.items = append(mn.items, menuItem{"Delete only for me", "", func() error { return m.c.Delete(ids, false) }, "Deleted"})
	}
	mn.items = append(mn.items, menuItem{label: "Cancel"})
	m.menu = mn
}

func (m *model) menuKey(key string) tea.Cmd {
	mn := m.menu
	switch key {
	case "esc", "q", "m", "ctrl+\\", "f3", "n":
		m.menu = nil
	case "up", "k":
		mn.sel = max(mn.sel-1, 0)
	case "down", "j":
		mn.sel = min(mn.sel+1, len(mn.items)-1)
	case "enter", "space":
		it := mn.items[mn.sel]
		m.menu = nil
		if it.act == nil {
			return nil
		}
		if mn.busy != "" {
			m.flash = mn.busy
		}
		return func() tea.Msg {
			if err := it.act(); err != nil {
				return doneMsg{err}
			}
			return flashMsg("✓ " + it.done)
		}
	}
	return nil
}

func (m *model) menuBox() []string {
	mn := m.menu
	w := 34
	if mn.del {
		w = 44
	}
	w = min(w, m.w-4)
	iw := w - 4
	var inner []string
	if mn.quote != "" {
		inner = append(inner, pad(replyBar.Render("┃ ")+dim.Render(mn.quote), iw), "")
	}
	for i, it := range mn.items {
		t, _, s, fill := rowStyles(i == mn.sel)
		mark := "  "
		if i == mn.sel {
			mark = "› "
		}
		inner = append(inner, row(iw, fill, t.Render(mark+it.label), s.Render(it.key)))
	}
	bottom := "enter run · esc close"
	if mn.del {
		bottom = "enter choose · esc cancel"
	}
	return box(w, len(inner)+2, focusBorder, title.Render(mn.title), "", dim.Render(bottom), inner)
}

func (m *model) logoutBox() []string {
	w := min(44, m.w-4)
	name := "this account"
	if me := m.snap.Me; me != nil {
		name = strings.TrimSpace(me.FirstName + " " + me.LastName)
	}
	inner := []string{pad("You're signed in as "+title.Render(name)+".", w-4)}
	for _, l := range renderText("Signing in again needs a code on your phone or another Telegram app.", nil, w-4, true) {
		inner = append(inner, pad(dim.Render(l), w-4))
	}
	keys := title.Render("y") + dim.Render(" log out · ") + title.Render("n") + dim.Render(" cancel")
	return box(w, len(inner)+2, focusBorder, title.Render("Log out?"), "", keys, inner)
}
