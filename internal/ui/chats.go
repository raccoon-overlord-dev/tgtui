package ui

import (
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/charmbracelet/x/ansi"

	"github.com/raccoon-overlord-dev/tgtui/internal/core"
)

// rowLines is how many lines a chat row takes: 2, and a blank one unless the terminal is short.
func (m *model) rowLines() int {
	if m.h < 26 {
		return 2
	}
	return 3
}

// listRows is how many chats the list shows.
func (m *model) listRows() int {
	per := m.rowLines()
	return max((m.h-3+per-2)/per, 1) // the status line and the borders; no blank after the last
}

// tabsTitle is the folder tabs in the list's top border: the current one in the accent,
// and › when some don't fit in w cells.
func (m *model) tabsTitle(w int) string {
	var b strings.Builder
	n := 0
	for i, t := range m.tabs() {
		name := t.Name
		if t.ID == core.Main {
			name = "All"
		}
		add := ansi.StringWidth(name)
		if i > 0 {
			add += 2
		}
		if n+add > w-2 {
			b.WriteString(dim.Render(" ›"))
			break
		}
		if i > 0 {
			b.WriteString("  ")
		}
		if i == m.tab {
			b.WriteString(accent.Render(name))
		} else {
			b.WriteString(dim.Render(name))
		}
		n += add
	}
	return b.String()
}

// listPane is the chat list as h lines of width w.
func (m *model) listPane(w, h int) []string {
	focused := m.focus == focusList
	m.listTop = min(m.listTop, m.sel)
	m.listTop = max(m.listTop, m.sel-m.listRows()+1)
	var inner []string
	list := m.tabs()[min(m.tab, len(m.tabs())-1)].ID
	for i := m.listTop; i < len(m.chats); i++ {
		blank := len(inner) > 0 && m.rowLines() == 3
		if len(inner)+2+map[bool]int{true: 1}[blank] > h-2 {
			break
		}
		if blank {
			inner = append(inner, "")
		}
		ch := m.chats[i]
		sel := i == m.sel && focused || ch.ID == m.open && !focused
		l1, l2 := m.chatRow(ch, w-4, sel, focused && ch.ID == m.open && i != m.sel, list)
		inner = append(inner, l1, l2)
	}
	if len(m.chats) == 0 {
		inner = append(inner, pad(dim.Render("No chats"), w-4))
	}
	right := ""
	if focused && m.vim() {
		right = accent.Render("LIST")
	}
	return box(w, h, borderOf(focused), m.tabsTitle(w-6), right, "", inner)
}

// chatRow is a chat's two lines in iw cells plus the padding columns: title and time, then
// the last message and the badges. sel fills them; bar marks the open chat with ▌.
func (m *model) chatRow(ch *core.Chat, iw int, sel, bar bool, list core.ChatList) (string, string) {
	t, T, s, fill := rowStyles(sel)
	when := ""
	if ch.Last != nil {
		when = m.shortTime(ch.Last.Date)
	}
	if ch.Pinned(list) {
		when = strings.TrimSpace("↑ " + when)
	}
	var badges []string
	if ch.ID != m.open { // the open chat is being read
		if ch.Mentions > 0 {
			badges = append(badges, mentionBadge.Render(" @ "))
		}
		switch {
		case ch.Unread > 0 && ch.Muted:
			badges = append(badges, mutedBadge.Render(" "+strconv.Itoa(ch.Unread)+" "))
		case ch.Unread > 0:
			badges = append(badges, badge.Render(" "+strconv.Itoa(ch.Unread)+" "))
		case ch.MarkedUnread:
			badges = append(badges, mentionBadge.Render(" • "))
		}
	}
	name := t
	if ch.Unread > 0 || ch.MarkedUnread || ch.ID == m.open {
		name = T
	}
	preview := ""
	if ch.Last != nil {
		preview = s.Render(m.preview(ch))
	}
	if typ := typingText(ch); typ != "" {
		preview = accent.Render(typ)
		if sel {
			preview = s.Render(typ)
		}
	}
	var av []string
	if iw >= 20 {
		av = m.chatAvatar(ch, 4, 2)
	}
	if av != nil { // the picture, then the row's own padding column as the gap
		iw -= 5
	}
	l1 := row(iw, fill, name.Render(ansi.Truncate(m.chatName(ch), max(iw-ansi.StringWidth(when)-1, 1), "…")), s.Render(when))
	l2 := row(iw, fill, preview, strings.Join(badges, fill.Render(" ")))
	if av != nil {
		l1, l2 = fill.Render(" ")+av[0]+l1, fill.Render(" ")+av[1]+l2
	}
	if bar {
		l1, l2 = accent.Render("▌")+l1[1:], accent.Render("▌")+l2[1:] // rows start with a plain space
	}
	return l1, l2
}

// typingText says who is typing in ch: "typing…" in a private chat, "Marco typing…" or
// "Marco +2 typing…" in a group; "" when nobody is.
func typingText(ch *core.Chat) string {
	if len(ch.Typing) == 0 {
		return ""
	}
	t := ch.Typing[0]
	if ch.Kind == core.ChatPrivate {
		return t.Action + "…"
	}
	who := t.Name
	if n := len(ch.Typing) - 1; n > 0 {
		who += fmt.Sprintf(" +%d", n)
	}
	return strings.TrimSpace(who + " " + t.Action + "…")
}

func (m *model) preview(ch *core.Chat) string {
	l := *ch.Last
	s := strings.ReplaceAll(label(l), "\n", " ")
	switch {
	case service[l.Kind] != "":
	case l.Outgoing && ch.Kind != core.ChatChannel:
		return "You: " + s
	case ch.Kind == core.ChatGroup && l.Sender != "":
		return l.Sender + ": " + s
	}
	return s
}

// shortTime is hh:mm today, the weekday this week, dd/mm before.
func (m *model) shortTime(unix int64) string {
	t, now := time.Unix(unix, 0), time.Now()
	switch {
	case sameDay(t, now):
		return t.Format(m.clock)
	case now.Sub(t) < 6*24*time.Hour:
		return t.Format("Mon")
	}
	return t.Format("02/01")
}

func sameDay(a, b time.Time) bool {
	ay, am, ad := a.Date()
	by, bm, bd := b.Date()
	return ay == by && am == bm && ad == bd
}
