package ui

// help lists the keys in groups, Vim and Telegram Desktop forms side by side. The first
// four groups make the left column on wide terminals.
var help = []struct {
	name string
	keys [][2]string
}{
	{"CHATS", [][2]string{{"j k  ↑ ↓", "move in the list"}, {"J K  alt+↑ ↓", "previous / next chat"}, {"enter", "open chat"}, {"esc", "close it (in the list)"},
		{"b", "back to the previous chat"}, {"m  ctrl+\\", "chat menu"}, {"R  ctrl+r", "mark as read"}}},
	{"FOLDERS", [][2]string{{"1–9  ctrl+1–8", "go to folder"}, {"[ ]  ctrl+⇧+↑ ↓", "previous / next folder"}, {"g a  ctrl+9", "archive"},
		{"g s  ctrl+0", "Saved Messages"}, {"g c", "contacts"}}},
	{"SEARCH", [][2]string{{"/", "find chats and contacts"}, {"?  ctrl+f", "search messages"}, {"g n  ctrl+n", "new chat: @user, +phone"},
		{":  :>", "find chats · settings, commands"}}},
	{"APP", [][2]string{{",  F2", "settings"}, {":help  F1", "this screen"}, {"L  :logout", "log out"}, {"U  :install", "install an update"}, {"ctrl+l", "redraw"},
		{":q  ctrl+q", "quit"}}},
	{"MESSAGES · NORMAL", [][2]string{{"j k  ctrl+↑ ↓", "select a message"}, {"r  alt+r", "reply"}, {"e  alt+e", "edit"},
		{"d  alt+d", "delete"}, {"f  alt+f", "forward"}, {"o  alt+o", "open media or link"}, {"s  alt+s", "save file"}, {"p  alt+p", "play / pause voice, audio"}, {"< >  alt+< >", "back / ahead 5 s"},
		{"space  alt+v", "show spoilers"}, {"i", "type a message"}}},
	{"COMPOSER · INSERT", [][2]string{{"enter", "send"}, {"alt+enter", "new line"}, {"↑", "edit last message (empty)"},
		{"a  ctrl+o", "attach files"}, {"ctrl+t", "record a voice message"}, {"ctrl+x", "remove attached files"}, {"@name  tab", "mention"},
		{"ctrl+b  ctrl+k", "bold · link"}, {"**b** __i__ `c`", "markup ~~s~~ ||spoiler||"}, {"esc", "back to NORMAL"}}},
}

// helpLines is one help column: group names, then their keys, a blank row between groups.
func (m *model) helpLines(groups int, from, w int) []string {
	kw := min(17, w/2)
	var out []string
	for _, g := range help[from : from+groups] {
		if len(out) > 0 {
			out = append(out, "")
		}
		out = append(out, dim.Render(g.name))
		for _, k := range g.keys {
			out = append(out, title.Render(fit(m.k(k[0]), kw-1))+" "+fit(k[1], w-kw))
		}
	}
	return out
}

// helpView is the Keys screen: one pane, two columns from 76 columns, scrolled by helpTop.
func (m *model) helpView(h int) []string {
	iw := m.w - 4
	var lines []string
	if m.w >= 76 {
		colw := (iw - 3) / 2
		left, right := m.helpLines(4, 0, colw), m.helpLines(2, 4, colw)
		for i := range max(len(left), len(right)) {
			l, r := "", ""
			if i < len(left) {
				l = left[i]
			}
			if i < len(right) {
				r = right[i]
			}
			lines = append(lines, fit(l, colw)+"   "+r)
		}
	} else {
		lines = m.helpLines(len(help), 0, iw)
	}
	m.helpTop = max(min(m.helpTop, len(lines)-(h-2)), 0)
	var inner []string
	for _, l := range lines[m.helpTop:] {
		inner = append(inner, pad(l, iw))
	}
	keymap := m.o.Config.Keys.Keymap + " · " + m.o.Config.Keys.Layout + " layout"
	return box(m.w, h, focusBorder, title.Render("Keys"), dim.Render(keymap), "", inner)
}

func (m *model) helpKey(key string) {
	switch key {
	case "j", "down":
		m.helpTop++
	case "k", "up":
		m.helpTop--
	case "pgdown", "ctrl+d", "space":
		m.helpTop += m.h / 2
	case "pgup", "ctrl+u":
		m.helpTop -= m.h / 2
	case "g", "home":
		m.helpTop = 0
	default: // esc, q, F1, any other key
		m.help = false
	}
	m.helpTop = max(m.helpTop, 0)
}
