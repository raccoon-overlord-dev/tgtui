package ui

import (
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/raccoon-overlord-dev/tgtui/internal/core"
)

// Two keymaps (Settings → Keys). vim+telegram: Telegram Desktop's shortcuts, plus a Vim-like
// normal mode with letter keys in the chat list and in chats (esc). telegram: no modes; a
// chat's composer always has the keys, and messages are picked with ctrl+↑/↓ and acted on
// with alt+letter, which also works while typing in vim+telegram. Every action has a key
// that legacy terminals deliver (ANALYSIS.md §5).

func (m *model) vim() bool { return m.o.Config.Keys.Keymap != "telegram" }

// itKeys are the shortcuts of the Italian ISO layout (Settings → Keys → Keyboard layout)
// for those that need US punctuation: the same physical keys where US has [ ] / ? (è + - _),
// and ctrl+ù for ctrl+\, which Italian keyboards can't type. Typing is never affected.
var itKeys = map[string]string{"è": "[", "+": "]", "-": "/", "_": "?", "ctrl+ù": "ctrl+\\"}

func (m *model) layoutKey(key string) string {
	if us, ok := itKeys[key]; ok && m.o.Config.Keys.Layout == "it" {
		return us
	}
	return key
}

func (m *model) key(k tea.KeyPressMsg) tea.Cmd {
	key := m.layoutKey(k.String()) // the composer and other text inputs get k itself
	if key == "ctrl+q" || m.snap.Auth.Kind != core.AuthReady && key == "ctrl+c" {
		return tea.Quit
	}
	if m.snap.Auth.Kind != core.AuthReady {
		return m.authKey(k)
	}
	m.flash = ""
	switch {
	case m.help:
		m.helpKey(key)
		return nil
	case m.settings:
		return m.settingsKey(key)
	case m.menu != nil:
		return m.menuKey(key)
	case m.askLogout:
		m.askLogout = false
		if key == "y" {
			return run(m.c.LogOut)
		}
		return nil
	case m.pick != nil:
		return m.pickerKey(k)
	}
	if m.rec != nil {
		switch key {
		case "ctrl+t", "enter":
			return m.recordKey()
		case "esc":
			return m.stopRecording(false)
		}
		m.flash = "Recording: enter sends · esc cancels"
		return nil
	}
	if cmd, ok := m.globalKey(key); ok {
		return cmd
	}
	if m.pathIn != nil {
		return m.pathKey(k)
	}
	if m.focus == focusChat && m.insert {
		return m.composerKey(k)
	}
	if !m.vim() {
		if m.focus == focusList {
			return m.plainListKey(k)
		}
		return m.chatKey(key) // a channel: scroll, esc
	}
	if m.pendingG {
		m.pendingG = false
		switch key {
		case "g":
			if m.focus == focusList {
				m.selectRow(0)
			} else {
				m.scroll(1 << 20)
			}
		case "s":
			return m.saved()
		case "a":
			return m.setTab(len(m.tabs()) - 1)
		case "c":
			return m.openPicker(pickContacts, nil)
		case "n":
			return m.openPicker(pickNew, nil)
		}
		return nil
	}
	switch key {
	case "g":
		m.pendingG = true
		return nil
	case "1", "2", "3", "4", "5", "6", "7", "8", "9":
		return m.setTab(int(key[0] - '1'))
	case "]":
		return m.setTab(m.tab + 1)
	case "[":
		return m.setTab(m.tab - 1)
	case "/":
		return m.openPicker(pickChats, nil)
	case "?":
		return m.openPicker(pickMessages, nil)
	case "J":
		return m.step(1)
	case "K":
		return m.step(-1)
	case "R":
		return m.markRead()
	case "b":
		return m.openChat(m.prevOpen)
	case "m":
		m.openMenu()
		return nil
	case ",":
		m.openSettings()
		return nil
	case ":":
		return m.openPicker(pickPalette, nil)
	case "L":
		m.askLogout = true
		return nil
	case "U":
		m.askUpdate()
		return nil
	}
	if m.focus == focusList {
		return m.listKey(key)
	}
	if cmd, ok := m.msgAction(key); ok {
		return cmd
	}
	return m.chatKey(key)
}

// globalKey handles the Telegram Desktop shortcuts and the other keys of every mode.
func (m *model) globalKey(key string) (tea.Cmd, bool) {
	switch key {
	case "f1":
		m.help, m.helpTop = true, 0
	case "f2", "ctrl+,":
		m.openSettings()
	case "ctrl+l":
		return tea.ClearScreen, true
	case "ctrl+\\", "f3":
		m.openMenu()
	case "alt+up", "ctrl+pgup":
		return m.step(-1), true
	case "alt+down", "ctrl+pgdown":
		return m.step(1), true
	case "ctrl+alt+home", "ctrl+alt+end":
		if len(m.chats) == 0 {
			return nil, true
		}
		m.selectRow(map[bool]int{true: 0, false: len(m.chats) - 1}[key == "ctrl+alt+home"])
		return m.openChat(m.chats[m.sel].ID), true
	case "ctrl+shift+down":
		return m.setTab(m.tab + 1), true
	case "ctrl+shift+up":
		return m.setTab(m.tab - 1), true
	case "ctrl+1", "ctrl+2", "ctrl+3", "ctrl+4", "ctrl+5", "ctrl+6", "ctrl+7", "ctrl+8":
		return m.setTab(int(key[5] - '1')), true
	case "ctrl+9":
		return m.setTab(len(m.tabs()) - 1), true
	case "ctrl+0":
		return m.saved(), true
	case "ctrl+n":
		if m.mention != nil { // there it moves down the list
			return nil, false
		}
		return m.openPicker(pickNew, nil), true
	case "ctrl+r":
		return m.markRead(), true
	case "ctrl+f":
		if m.focus == focusChat {
			return m.openPicker(pickInChat, nil), true
		}
		return m.openPicker(pickMessages, nil), true
	case "ctrl+o":
		if m.focus != focusChat {
			return nil, false
		}
		return m.attachPrompt(), true
	case "ctrl+t":
		if m.focus != focusChat {
			return nil, false
		}
		return m.recordKey(), true
	default:
		if letter, ok := strings.CutPrefix(key, "alt+"); ok && m.focus == focusChat && len(letter) == 1 {
			if letter == "v" {
				letter = "space"
			}
			return m.msgAction(letter)
		}
		return nil, false
	}
	return nil, true
}

func (m *model) saved() tea.Cmd {
	if m.snap.Me == nil {
		return nil
	}
	return m.enterChat(m.snap.Me.ID, m.c.OpenSaved)
}

func (m *model) markRead() tea.Cmd {
	id := m.selID
	if m.focus == focusChat {
		id = m.open
	}
	return run(func() error { return m.c.MarkRead(id) })
}

func (m *model) listKey(key string) tea.Cmd {
	switch key {
	case "j", "down":
		m.selectRow(m.sel + 1)
	case "k", "up":
		m.selectRow(m.sel - 1)
	case "G", "end":
		m.selectRow(len(m.chats) - 1)
	case "home":
		m.selectRow(0)
	case "pgdown", "ctrl+d":
		m.selectRow(m.sel + m.listRows()/2)
	case "pgup", "ctrl+u":
		m.selectRow(m.sel - m.listRows()/2)
	case "enter", "l", "right":
		if len(m.chats) > 0 {
			return m.openChatOf(m.chats[m.sel])
		}
	case "tab":
		if m.open != 0 {
			m.focus = focusChat
		}
	case "esc": // as in Telegram: esc in the list closes the open chat
		return m.closeChat()
	}
	return nil
}

// plainListKey is the chat list without letter keys (telegram keymap): ←/→ change folder,
// and typing searches, as in Telegram Desktop.
func (m *model) plainListKey(k tea.KeyPressMsg) tea.Cmd {
	switch key := k.String(); key {
	case "left":
		return m.setTab(m.tab - 1)
	case "right":
		return m.setTab(m.tab + 1)
	case "j", "k", "l", "G", "ctrl+d", "ctrl+u":
	case "esc":
		return m.listKey(key)
	default:
		if k.Text != "" && !strings.Contains(key, "+") && key != "space" {
			kind := pickChats
			if k.Text == ">" { // settings and commands, as after : in the vim keymap
				kind = pickPalette
			}
			cmd := m.openPicker(kind, nil)
			m.pick.in.insert(k.Text)
			return tea.Batch(cmd, m.search())
		}
		if key == "space" {
			return nil
		}
		if key == "tab" && m.open != 0 {
			m.focus = focusChat
			m.insert = m.canWrite()
			return nil
		}
		return m.listKey(key)
	}
	if k.Text != "" { // letters of the vim list keys: search as well
		cmd := m.openPicker(pickChats, nil)
		m.pick.in.insert(k.Text)
		return tea.Batch(cmd, m.search())
	}
	return nil
}

// msgAction is an action on the selected message: r reply, e edit, d delete, f forward,
// o open, s save, space spoilers. Normal mode takes the letter, every mode alt+letter.
func (m *model) msgAction(key string) (tea.Cmd, bool) {
	sel := m.selected()
	switch key {
	case "<":
		return m.seek(-5 * time.Second), true
	case ">":
		return m.seek(5 * time.Second), true
	case "r", "e", "d", "f", "o", "s", "p", "space":
	default:
		return nil, false
	}
	if sel == nil {
		m.flash = "Select a message first (ctrl+↑)"
		return nil, true
	}
	switch key {
	case "r":
		m.write()
		m.replyTo, m.editID = sel.ID, 0
	case "e":
		return m.edit(sel), true
	case "d":
		id := sel.ID
		m.replyTo = 0
		return func() tea.Msg { p, err := m.c.Properties(id); return propsMsg{id, p, err} }, true
	case "f":
		m.replyTo = 0
		return m.openPicker(pickForward, []int64{sel.ID}), true
	case "o":
		return m.fileKey("open"), true
	case "s":
		return m.fileKey("save"), true
	case "p":
		return m.playKey(), true
	case "space":
		m.revealed[sel.ID] = !m.revealed[sel.ID]
	}
	return nil, true
}

func (m *model) chatKey(key string) tea.Cmd {
	half := max(m.paneHeight()/2, 1)
	i, ok := m.indexOf(m.cur)
	if !ok && (key == "j" || key == "k" || key == "up" || key == "down") { // nothing selected: start at the bottom
		_, _, _, bottom := m.messageRows()
		m.selectMsg(bottom)
		return nil
	}
	switch key {
	case "j", "down":
		m.selectMsg(i + 1)
	case "k", "up":
		m.selectMsg(i - 1)
	case "ctrl+d", "pgdown":
		m.scroll(-half)
	case "ctrl+u", "pgup":
		m.scroll(half)
	case "G", "end":
		if !m.win.AtNewest {
			return m.reopen()
		}
		m.anchor, m.off = 0, 0
		m.selectMsg(len(m.win.Messages) - 1)
	case "i", "enter":
		m.write()
	case "a":
		return m.attachPrompt()
	case "esc", "q", "h", "left", "tab":
		m.focus, m.cur = focusList, 0
	}
	return nil
}

// attachPrompt picks files to send: in the system's file picker, or by typing the path.
func (m *model) attachPrompt() tea.Cmd {
	if !m.canWrite() {
		return nil
	}
	if cmd := fileDialog(); cmd != nil && m.o.Config.Media.Attach != "path" {
		m.flash = "Choosing files…"
		return chooseFiles(m.open, cmd)
	}
	in := newInput("~/")
	m.pathIn, m.insert = &in, true
	m.listPaths()
	return nil
}

// write switches to insert mode, unless the chat is a channel.
func (m *model) write() {
	m.insert, m.cur = m.canWrite(), 0
	if !m.insert {
		m.flash = "✕ Channels are read-only"
	}
}

// edit loads an own text message into the composer, with its formatting as markup.
func (m *model) edit(msg *core.Message) tea.Cmd {
	switch {
	case msg == nil:
		return nil
	case !msg.Outgoing:
		m.flash = "✕ Only your own messages can be edited"
		return nil
	case msg.Kind != "Text":
		m.flash = "✕ Only text messages can be edited for now"
		return nil
	}
	cp := *msg
	return func() tea.Msg { t, err := m.c.Markdown(cp); return editMsg{cp.ID, t, err} }
}

func (m *model) askDeleteFor(p propsMsg) tea.Cmd {
	switch {
	case p.err != nil:
		m.flash = "✕ " + errText(p.err)
	case !p.props.DeleteForAll && !p.props.DeleteForSelf:
		m.flash = "✕ This message can't be deleted"
	default:
		m.openDelete(p)
	}
	return nil
}

// reopen jumps back to the newest messages by loading them again.
func (m *model) reopen() tea.Cmd {
	id := m.open
	m.anchor, m.off = 0, 0
	return func() tea.Msg { m.c.OpenChat(id); return nil }
}

func (m *model) composerKey(k tea.KeyPressMsg) tea.Cmd {
	if cmd, ok := m.mentionKey(k.String()); ok {
		return cmd
	}
	cmd := m.editKey(k)
	if m.insert {
		return tea.Batch(cmd, m.checkMention())
	}
	return cmd
}

// markup are the formatting shortcuts of Telegram Desktop, as the markup they type.
var markup = map[string][2]string{
	"ctrl+b": {"**", "**"}, "ctrl+i": {"__", "__"}, "ctrl+shift+x": {"~~", "~~"},
	"ctrl+shift+m": {"`", "`"}, "ctrl+shift+p": {"||", "||"}, "ctrl+k": {"[", "]()"},
}

func (m *model) editKey(k tea.KeyPressMsg) tea.Cmd {
	if mk, ok := markup[k.String()]; ok {
		m.comp.wrap(mk[0], mk[1])
		if k.String() == "ctrl+k" {
			m.comp.cur++ // into the (), for the link
		}
		return nil
	}
	switch k.String() {
	case "esc":
		switch {
		case m.editID != 0:
			m.editID, m.comp = 0, newInput("")
		case m.replyTo != 0 || m.cur != 0 && !m.vim():
			m.replyTo, m.cur = 0, 0
		case !m.vim():
			m.focus = focusList
		default:
			m.insert = false
			_, _, _, bottom := m.messageRows()
			m.cur = 0
			if len(m.win.Messages) > 0 {
				m.cur = m.win.Messages[bottom].ID
			}
		}
	case "ctrl+x":
		m.attach = nil
	case "enter":
		text := strings.TrimSpace(m.comp.value())
		if text == "" && len(m.attach) == 0 {
			return nil
		}
		m.comp = newInput("")
		if files := m.attach; len(files) > 0 && m.editID == 0 {
			reply := m.replyTo
			m.attach, m.replyTo, m.anchor, m.off = nil, 0, 0, 0
			return run(func() error { return m.c.SendFiles(files, text, reply) })
		}
		if id := m.editID; id != 0 {
			m.editID = 0
			return run(func() error { return m.c.EditText(id, text) })
		}
		reply := m.replyTo
		m.replyTo, m.cur, m.anchor, m.off = 0, 0, 0, 0
		send := run(func() error { return m.c.SendText(text, reply) })
		if !m.win.AtNewest {
			return tea.Batch(m.reopen(), send)
		}
		return send
	case "shift+enter", "alt+enter", "ctrl+j":
		m.comp.insert("\n")
	case "up":
		if m.comp.value() == "" && m.editID == 0 { // Telegram: ↑ in an empty box edits the last own message
			for i := len(m.win.Messages) - 1; i >= 0; i-- {
				if msg := m.win.Messages[i]; msg.Outgoing && msg.Kind == "Text" {
					return m.edit(&msg)
				}
			}
			return nil
		}
		m.comp.key(k, true)
	case "ctrl+up", "ctrl+down": // Telegram: reply to the previous / next message
		ms := m.win.Messages
		i, ok := m.indexOf(m.replyTo)
		if !ok {
			i = len(ms)
		}
		if k.String() == "ctrl+up" {
			i--
		} else {
			i++
		}
		m.replyTo, m.cur = 0, 0
		if i >= 0 && i < len(ms) {
			m.replyTo = ms[i].ID
			m.selectMsg(i)
		}
	case "pgup":
		m.scroll(max(m.paneHeight()/2, 1))
	case "pgdown":
		m.scroll(-max(m.paneHeight()/2, 1))
	default:
		before := m.comp.value()
		m.comp.key(k, true)
		// Telegram apps repeat "typing" every few seconds while it lasts; not for edits.
		if v := m.comp.value(); v != before && v != "" && m.editID == 0 && time.Since(m.typedAt) > 5*time.Second {
			m.typedAt = time.Now()
			return run(func() error { return m.c.SendAction("chatActionTyping") })
		}
	}
	return nil
}

// wrap puts open and close around the selection, or around the cursor.
func (t *textInput) wrap(open, close string) {
	lo, hi := t.selection()
	if lo == hi {
		lo, hi = t.cur, t.cur
	}
	t.save("")
	sel := string(t.r[lo:hi])
	rest := string(t.r[hi:])
	t.r = []rune(string(t.r[:lo]) + open + sel + close + rest)
	t.cur, t.anchor = lo+len([]rune(open+sel)), -1
}

// commands of the : palette, run when one is typed exactly, listed after >.
var commands = [][2]string{
	{"q", "quit"}, {"help", "keys"}, {"settings", "settings"}, {"update", "check for updates"}, {"install", "install the update"}, {"logout", "log out"},
	{"new", "start a new chat"}, {"saved", "Saved Messages"}, {"contacts", "contacts"}, {"archive", "the archive"},
}

var commandAliases = map[string]string{"quit": "q", "qa": "q", "h": "help", "set": "settings"}

// commandNamed is the palette item of command q, typed exactly.
func commandNamed(q string) (item, bool) {
	if a, ok := commandAliases[q]; ok {
		q = a
	}
	for _, c := range commands {
		if c[0] == q {
			return item{title: ":" + c[0], right: c[1], section: "Command", cmd: c[0]}, true
		}
	}
	return item{}, false
}

// settingItems are the Settings rows and the commands that match q, for the palette after >.
func (m *model) settingItems(q string) []item {
	q = strings.ToLower(q)
	var out []item
	for i, r := range settingRows {
		if !strings.Contains(strings.ToLower(r.section+" "+r.label), q) {
			continue
		}
		it := item{title: r.label, section: "Settings", set: i + 1, right: r.section}
		if r.opts != nil {
			it.right = r.get(m.o.Config)
		}
		out = append(out, it)
	}
	for _, c := range commands {
		if strings.Contains(c[0]+" "+c[1], q) {
			out = append(out, item{title: ":" + c[0], right: c[1], section: "Commands", cmd: c[0]})
		}
	}
	return out
}

func (m *model) runCommand(c string) tea.Cmd {
	switch c {
	case "q":
		return tea.Quit
	case "help":
		m.help, m.helpTop = true, 0
	case "settings":
		m.openSettings()
	case "update":
		m.flash = "Checking…"
		return m.checkUpdate(true)
	case "install":
		m.askUpdate()
	case "logout":
		m.askLogout = true
	case "new":
		return m.openPicker(pickNew, nil)
	case "saved":
		return m.saved()
	case "contacts":
		return m.openPicker(pickContacts, nil)
	case "archive":
		return m.setTab(len(m.tabs()) - 1)
	}
	return nil
}
