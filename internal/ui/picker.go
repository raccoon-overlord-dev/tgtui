package ui

import (
	"fmt"
	"strings"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"

	"github.com/raccoon-overlord-dev/tgtui/internal/core"
)

type pickKind int

const (
	pickForward  pickKind = iota // the chat to forward to
	pickChats                    // chats and contacts, by name (/)
	pickContacts                 // contacts (g c)
	pickMessages                 // messages in all chats (?, ctrl+f in the list)
	pickInChat                   // messages in the open chat (ctrl+f in a chat)
	pickPalette                  // the : palette: chats as pickChats, settings and commands after >
	pickNew                      // a new chat: contacts, or someone by @username or +phone
)

// chats: the kind searches chats and contacts by name; contacts: it lists contacts.
func (k pickKind) chats() bool    { return k == pickChats || k == pickPalette }
func (k pickKind) contacts() bool { return k == pickContacts || k == pickNew }

type item struct {
	cmd             string // a : command to run
	set             int    // a Settings row to go to, its index + 1
	title, right    string // the name; its kind, @username or time on the right
	who, text       string // a message result's second line: "Sender: " and the text
	section         string // Chats, Contacts, Recent: a label above the first item of each
	chat, user, msg int64  // chat 0 with user: a contact without a chat yet
}

// picker is a filtered list: type to search, enter to pick. Searches run in core and come
// back as resultsMsg; query is the search the items answer.
type picker struct {
	kind  pickKind
	ids   []int64 // pickForward: the messages
	in    textInput
	sel   int
	items []item
	query string
	busy  bool
}

type resultsMsg struct {
	p     *picker
	query string
	items []item
	err   error
}

func (m *model) openPicker(kind pickKind, ids []int64) tea.Cmd {
	if kind == pickInChat && m.open == 0 {
		kind = pickMessages
	}
	m.pick = &picker{kind: kind, ids: ids, in: newInput(""), query: "\x00"}
	return m.search()
}

func (m *model) chatName(ch *core.Chat) string {
	if m.snap.Me != nil && ch.ID == m.snap.Me.ID {
		return "Saved Messages"
	}
	return ch.Title
}

// kindOf is a chat's kind for the right of a picker row: private, group, channel.
func (m *model) kindOf(id int64) string {
	ch := m.c.Chat(id)
	if ch == nil || m.snap.Me != nil && id == m.snap.Me.ID {
		return ""
	}
	return []string{"private", "group", "channel"}[ch.Kind]
}

func (m *model) foundItem(f core.Found) item {
	if f.UserID == 0 {
		return item{title: f.Title, right: m.kindOf(f.ChatID), section: "Chats", chat: f.ChatID}
	}
	it := item{title: f.Title, section: "Contacts", chat: f.ChatID, user: f.UserID}
	if f.Username != "" {
		it.right = "@" + f.Username
	}
	return it
}

// search starts the search for the picker's current text, unless the items answer it.
func (m *model) search() tea.Cmd {
	p, q := m.pick, strings.TrimSpace(m.pick.in.value())
	if q == p.query {
		return nil
	}
	if rest, ok := strings.CutPrefix(q, ">"); ok && p.kind.chats() { // > in any chat search: settings and commands
		p.query, p.items = q, m.settingItems(strings.TrimSpace(rest))
		return nil
	}
	if p.kind == pickForward || p.kind.chats() && q == "" { // filter the chat list, no request
		p.query, p.items = q, nil
		section := ""
		if p.kind.chats() {
			section = "Recent"
		}
		for _, ch := range m.c.Chats(core.Main) {
			if strings.Contains(strings.ToLower(m.chatName(ch)), strings.ToLower(q)) {
				p.items = append(p.items, item{title: m.chatName(ch), right: m.kindOf(ch.ID), section: section, chat: ch.ID})
			}
		}
		return nil
	}
	if q == "" && !p.kind.contacts() {
		p.query, p.items = q, nil
		return nil
	}
	p.busy = true
	open := m.open
	return func() tea.Msg {
		var items []item
		var err error
		switch {
		case p.kind.chats() || p.kind.contacts():
			var fs []core.Found
			if p.kind.chats() {
				fs, err = m.c.SearchChats(q)
			} else {
				fs, err = m.c.Contacts()
			}
			for _, f := range fs {
				if p.kind.chats() {
					items = append(items, m.foundItem(f))
				} else if strings.Contains(strings.ToLower(f.Title+" "+f.Username), strings.ToLower(q)) {
					it := m.foundItem(f)
					it.section = ""
					items = append(items, it)
				}
			}
		default:
			chat := int64(0)
			if p.kind == pickInChat {
				chat = open
			}
			var ms []core.Message
			ms, err = m.c.SearchMessages(q, chat)
			for _, msg := range ms {
				it := item{title: senderName(msg), right: m.shortTime(msg.Date), text: oneLine(label(msg)), chat: msg.ChatID, msg: msg.ID}
				if ch := m.c.Chat(msg.ChatID); ch != nil && p.kind == pickMessages {
					it.title = m.chatName(ch)
					switch {
					case msg.Outgoing && ch.Kind != core.ChatChannel:
						it.who = "You: "
					case ch.Kind == core.ChatGroup:
						it.who = senderName(msg) + ": "
					}
				}
				items = append(items, it)
			}
		}
		if p.kind == pickNew && (strings.HasPrefix(q, "@") && len(q) > 1 || strings.HasPrefix(q, "+")) {
			if f, err := m.c.Lookup(q); err == nil && (f.ChatID != 0 || f.UserID != 0) {
				it := m.foundItem(f)
				it.section = ""
				if it.right == "" && f.Username != "" {
					it.right = "@" + f.Username
				}
				items = append([]item{it}, items...)
			}
		}
		if c, ok := commandNamed(q); ok && p.kind == pickPalette {
			items = append([]item{c}, items...)
		}
		if me := m.snap.Me; me != nil && p.kind.chats() && strings.Contains("saved messages", strings.ToLower(q)) {
			items = append([]item{{title: "Saved Messages", section: "Chats", chat: me.ID}}, items...)
		}
		return resultsMsg{p, q, items, err}
	}
}

func (m *model) pickerKey(k tea.KeyPressMsg) tea.Cmd {
	p := m.pick
	switch k.String() {
	case "esc":
		m.pick = nil
	case "up", "ctrl+p", "ctrl+k":
		p.sel = max(p.sel-1, 0)
	case "down", "ctrl+n", "ctrl+j", "tab":
		p.sel = min(p.sel+1, max(len(p.items)-1, 0))
	case "enter":
		if len(p.items) == 0 {
			return nil
		}
		it := p.items[min(p.sel, len(p.items)-1)]
		m.pick = nil
		return m.picked(p, it)
	default:
		p.in.key(k, false)
		p.sel = 0
		return m.search()
	}
	return nil
}

func (m *model) picked(p *picker, it item) tea.Cmd {
	switch {
	case it.cmd != "":
		return m.runCommand(it.cmd)
	case it.set != 0:
		m.openSettings()
		m.setSel = it.set - 1
		return nil
	case p.kind == pickForward:
		ids := p.ids
		return func() tea.Msg {
			if err := m.c.Forward(it.chat, ids); err != nil {
				return doneMsg{err}
			}
			return flashMsg("✓ Forwarded to " + it.title)
		}
	case it.msg != 0:
		m.jumpTo = it.msg
		return m.enterChat(it.chat, func() { m.c.OpenChatAt(it.chat, it.msg) })
	case it.chat != 0:
		return m.openChat(it.chat)
	default:
		return m.enterChat(it.user, func() { m.c.OpenUser(it.user) })
	}
}

// hl draws s with the first match of q in bold.
func hl(s, q string, base, bold lipgloss.Style) string {
	i := strings.Index(strings.ToLower(s), strings.ToLower(q))
	if q == "" || i < 0 || len(strings.ToLower(s)) != len(s) {
		return base.Render(s)
	}
	return base.Render(s[:i]) + bold.Render(s[i:i+len(q)]) + base.Render(s[i+len(q):])
}

// pickerBox is the picker as a centred box of at most h lines: the query, a rule, the
// results (scrolled to the selection).
func (m *model) pickerBox(h int) []string {
	p := m.pick
	w := map[pickKind]int{pickForward: 50, pickContacts: 50, pickNew: 50, pickChats: 60, pickPalette: 60, pickMessages: 84, pickInChat: 84}[p.kind]
	w = min(w, m.w-4)
	iw := w - 4
	q := strings.TrimSpace(p.in.value())
	settings := p.kind.chats() && strings.HasPrefix(q, ">")
	hq := strings.TrimSpace(strings.TrimPrefix(q, ">")) // what results highlight
	hint := map[pickKind]string{pickForward: "type to filter", pickContacts: "type to filter", pickChats: "type a name or @username",
		pickPalette: "a chat, or > for settings", pickNew: "a contact, @username or +phone"}[p.kind]
	if hint == "" {
		hint = "type to search"
	}
	input := title.Render("› ")
	if p.in.value() == "" {
		input += text.Reverse(true).Render(" ") + dim.Render(hint)
	} else {
		input += p.in.view(text, max(iw-2, 1))
	}
	inner := []string{pad(input, iw), pad(borderSt.Render(strings.Repeat("─", iw)), iw)}
	var rows []string
	selTo, section := 0, ""
	for i, it := range p.items {
		t, T, s, fill := rowStyles(i == p.sel)
		if it.section != section {
			if section = it.section; len(rows) > 0 {
				rows = append(rows, "")
			}
			rows = append(rows, pad(dim.Render(section), iw))
		}
		if it.text == "" {
			rows = append(rows, row(iw, fill, hl(it.title, hq, t, T), s.Render(it.right)))
		} else {
			if i > 0 {
				rows = append(rows, "")
			}
			rows = append(rows, row(iw, fill, T.Render(it.title), s.Render(it.right)), row(iw, fill, s.Render(it.who)+hl(it.text, hq, t, T), ""))
		}
		if i == p.sel {
			selTo = len(rows)
		}
	}
	if len(p.items) == 0 && !p.busy && q != "" && p.query == q {
		rows = append(rows, "", center(title.Render(`Nothing found for "`+q+`"`), iw+2))
		if p.kind.chats() && !settings {
			rows = append(rows, center(dim.Render(m.k("?")+" searches inside messages instead"), iw+2))
		}
	}
	room := max(h-2-len(inner), 1)
	top := max(selTo-room, 0)
	inner = append(inner, rows[top:min(top+room, len(rows))]...)

	head := map[pickKind]string{pickForward: "Forward to…", pickContacts: "Contacts", pickChats: "Find chats and contacts",
		pickPalette: "Find chats and contacts", pickNew: "New chat"}[p.kind]
	if settings {
		head = "Settings and commands"
	}
	head = title.Render(head)
	switch p.kind {
	case pickMessages:
		head = title.Render("Search messages") + "  " + dim.Render("all chats")
	case pickInChat:
		if ch := m.openChatInfo(); ch != nil {
			head = title.Render("Search in " + m.chatName(ch))
		}
	}
	right := ""
	switch {
	case p.busy:
		right = title.Render(m.spinner() + " searching")
	case p.kind == pickForward:
		right = dim.Render(map[bool]string{true: "1 message", false: fmt.Sprint(len(p.ids), " messages")}[len(p.ids) == 1])
	case p.kind.contacts() && q == "":
		right = dim.Render(fmt.Sprint(len(p.items)))
	case q != "" && p.query == q || settings:
		right = dim.Render(map[bool]string{true: "1 result", false: fmt.Sprint(len(p.items), " results")}[len(p.items) == 1])
	}
	bottom := map[pickKind]string{pickForward: "enter send · esc close", pickContacts: "enter open chat · esc close",
		pickNew: "enter open chat · esc close", pickChats: "↑ ↓ move · enter open · esc close", pickPalette: "↑ ↓ move · enter open · esc close", pickMessages: "↑ ↓ move · enter jump to message · esc close",
		pickInChat: "enter jump · esc close"}[p.kind]
	if len(p.items) == 0 {
		bottom = "esc close"
	}
	return box(w, len(inner)+2, focusBorder, head, right, dim.Render(bottom), inner)
}
