// Package ui is tgtui's Bubble Tea front end, the "Panels" design (tgtui-handoff/README.md):
// rounded panes, one bright frame on the focused pane, themes from themes/. It reads core
// copies and calls core commands, and never imports TDLib.
package ui

import (
	"os"
	"slices"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/colorprofile"
	"github.com/charmbracelet/x/ansi"

	uv "github.com/charmbracelet/ultraviolet"

	"github.com/raccoon-overlord-dev/tgtui/internal/config"
	"github.com/raccoon-overlord-dev/tgtui/internal/core"
	"github.com/raccoon-overlord-dev/tgtui/internal/ui/termimg"
)

type Options struct {
	Config  *config.Config // the settings; Settings changes and saves them
	DataDir string         // shown in Settings → About
	Version string
	Warn    string // shown in the status bar, e.g. no keychain
	TestDC  bool
	// ExitWhenListed quits as soon as the chat list has chats: tgtui --startup-time.
	ExitWhenListed bool
}

// Run starts the TUI and blocks until the user quits or the session closes.
func Run(c *core.Core, o Options) error {
	m := newModel(c, o)
	defer m.stopAudio()
	_, err := tea.NewProgram(m).Run()
	return err
}

type focus int

// Whether the terminal has focus: unknown until it reports (not all terminals do).
const (
	focusUnknown = iota
	focusHere
	focusAway
)

const (
	focusList focus = iota
	focusChat
)

type model struct {
	c       *core.Core
	o       Options
	snap    core.Snapshot
	w, h    int
	focused int // focusUnknown | focusHere | focusAway

	// from the settings, see applyConfig
	images    termimg.Mode
	notifyTo  string    // off | terminal | system
	clock     string    // time.Format layout of times of day
	kittyOK   bool      // the terminal answered the kitty graphics query
	play      *playback // the voice or audio message playing or paused
	playGen   int
	rec       *recording         // the voice note being recorded
	avatars   map[avKey][]string // profile pictures drawn, kitty images among them sent
	avatarDL  []int32            // pictures to download, found while drawing
	typedAt   time.Time          // when "typing" was last sent
	x256      bool               // a 256-colour terminal: themes use their x256 colours
	theme     string             // the theme loaded, with the background setting: "harvest/theme"
	themeX256 bool

	spin      int    // spinner frame
	ticking   bool   // a spinner tick is on its way
	flashSeen string // the flash the clear timer runs for
	flashN    int

	auth authState

	// main screen
	chats      []*core.Chat // of the current tab
	tab        int
	sel        int   // selected row in the chat list
	selID      int64 // its chat, to keep the selection when chats reorder
	listTop    int   // first chat row shown
	focus      focus
	insert     bool // the composer gets the keys (Vim insert mode)
	open       int64
	prevOpen   int64 // for "back to the previous chat"
	win        core.Window
	anchor     int64 // message at the bottom of the pane, 0: follow the newest
	off        int   // lines of the anchor message hidden below the pane
	cache      map[lineKey]rendered
	viewedMax  int64
	cur        int64 // selected message (normal mode)
	revealed   map[int64]bool
	comp       textInput
	replyTo    int64 // the composer's message replies to this one
	editID     int64 // the composer edits this message instead of sending a new one
	pick       *picker
	jumpTo     int64 // a search result: select it once the window holds it
	mention    *mention
	attach     []string         // files to send with the composer's text as caption
	pathIn     *textInput       // the "attach file" prompt, nil when closed
	pending    map[int32]string // file → "open" / "save:<name>", to do once downloaded
	requested  map[int32]bool   // previews already asked to download
	nextImg    uint32           // last kitty image id
	sendQ      []string         // kitty image transmissions to write
	help       bool
	helpTop    int
	settings   bool
	setSel     int
	menu       *chatMenu
	pathItems  []string // the attach prompt's completions
	pathSel    int      // the selected one, -1 for none
	newVersion string   // a newer release, "" when none
	askLogout  bool
	flash      string
}

func newModel(c *core.Core, o Options) *model {
	m := &model{c: c, o: o, cache: map[lineKey]rendered{}, revealed: map[int64]bool{}, comp: newInput(""),
		pending: map[int32]string{}, requested: map[int32]bool{}, avatars: map[avKey][]string{}}
	m.applyConfig()
	return m
}

// applyConfig takes the settings into the model, after start and after each change.
func (m *model) applyConfig() {
	cfg := m.o.Config
	images := termimg.Detect(cfg.Media.Images)
	// A terminal that says it has kitty graphics gets them, except herdr (0.8), which says
	// so but shows none. No answer proves the opposite: it can come late or not at all.
	if cfg.Media.Images == "auto" && m.kittyOK && os.Getenv("HERDR_ENV") == "" {
		images = termimg.Kitty
	}
	if images != m.images {
		m.images, m.cache, m.avatars = images, map[lineKey]rendered{}, map[avKey][]string{}
	}
	m.notifyTo = notifyMode(cfg.Notifications.Mode)
	clock := "15:04"
	if cfg.Appearance.TimeFormat == "12h" {
		clock = "3:04 PM"
	}
	if clock != m.clock {
		m.clock, m.cache = clock, map[lineKey]rendered{}
	}
	if th := cfg.Appearance.Theme + "/" + cfg.Appearance.Background; th != m.theme || m.x256 != m.themeX256 {
		setTheme(cfg.Appearance.Theme, m.x256, cfg.Appearance.Background != "terminal")
		m.theme, m.themeX256, m.cache, m.avatars = th, m.x256, map[lineKey]rendered{}, map[avKey][]string{}
	}
}

// kittyQuery asks whether the terminal has kitty graphics.
// ponytail: in tmux the query only reaches the terminal with allow-passthrough on, and its
// answer may not come back; there the environment guess stays.
const kittyQuery = "\x1b_Gi=31,s=1,v=1,a=q,t=d,f=24;AAAA\x1b\\"

type (
	tickMsg       struct{}
	clearFlashMsg int
	changedMsg    struct{}
	doneMsg       struct{ err error }
	flashMsg      string
	propsMsg      struct {
		id    int64
		props core.Props
		err   error
	}
	editMsg struct {
		id   int64
		text string
		err  error
	}
)

func (m *model) wait() tea.Cmd {
	return func() tea.Msg { <-m.c.Changed(); return changedMsg{} }
}

// run calls a core command off the UI goroutine; its error lands in the status bar.
func run(f func() error) tea.Cmd {
	return func() tea.Msg { return doneMsg{f()} }
}

func (m *model) Init() tea.Cmd {
	cmds := []tea.Cmd{m.wait(), m.checkUpdate(false), tea.Raw("\x1b[16t")} // cell size in pixels
	if m.o.Config.Media.Images == "auto" && os.Getenv("TMUX") == "" {
		cmds = append(cmds, tea.Raw(kittyQuery))
	}
	return tea.Batch(cmds...)
}

func (m *model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	var cmd tea.Cmd
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.w, m.h = msg.Width, msg.Height
	case tea.ColorProfileMsg:
		m.x256 = msg.Profile < colorprofile.TrueColor
		m.applyConfig()
	case tickMsg:
		m.ticking = false
		m.spin++
		cmd = m.signalRecording()
	case playEndMsg:
		m.playEnded(msg)
	case recordedMsg:
		cmd = m.recorded(msg)
	case clearFlashMsg:
		if int(msg) == m.flashN {
			m.flash = ""
		}
	case uv.KittyGraphicsEvent:
		if msg.Options.ID == 31 && string(msg.Payload) == "OK" {
			m.kittyOK = true
			m.applyConfig()
		}
	case uv.CellSizeEvent:
		if msg.Width > 0 && msg.Height > 0 {
			termimg.CellW, termimg.CellH = msg.Width, msg.Height
			m.cache, m.avatars = map[lineKey]rendered{}, map[avKey][]string{}
		}
	case updateMsg:
		m.updated(msg)
	case tea.FocusMsg:
		m.focused = focusHere
	case tea.BlurMsg:
		m.focused = focusAway
	case changedMsg:
		old := m.snap.Auth.Kind
		m.snap = m.c.Snapshot()
		if m.snap.Auth.Kind == core.AuthClosed {
			return m, tea.Quit
		}
		if m.snap.Auth.Kind != old {
			m.auth = authState{}
		}
		m.refresh()
		if m.o.ExitWhenListed && len(m.chats) > 0 {
			return m, tea.Quit
		}
		cmds := []tea.Cmd{m.wait()}
		for _, n := range m.c.TakeNotifications() {
			cmds = append(cmds, m.notify(n))
		}
		cmd = tea.Batch(cmds...)
	case doneMsg:
		m.auth.busy = false
		if msg.err != nil && m.snap.Auth.Kind == core.AuthReady {
			m.flash = "✕ " + errText(msg.err)
		} else if msg.err != nil {
			m.auth.err = errText(msg.err)
		} else {
			m.auth.input = nil
		}
	case flashMsg:
		m.flash = string(msg)
	case chosenMsg:
		m.flash = ""
		if msg.chat == m.open && len(msg.paths) > 0 {
			m.attach, m.insert = append(m.attach, msg.paths...), true
		}
	case resultsMsg:
		if p := m.pick; p == msg.p && msg.query == strings.TrimSpace(p.in.value()) {
			p.items, p.query, p.busy, p.sel = msg.items, msg.query, false, 0
			if msg.err != nil {
				m.flash = "✕ " + errText(msg.err)
			}
		}
	case mentionMsg:
		if mn := m.mention; mn != nil && mn.query == msg.query {
			mn.items, mn.sel = msg.items, 0
		}
	case propsMsg:
		cmd = m.askDeleteFor(msg)
	case editMsg:
		if msg.err != nil {
			m.flash = "✕ " + errText(msg.err)
		} else {
			m.comp, m.editID, m.replyTo, m.insert = newInput(msg.text), msg.id, 0, true
		}
	case tea.PasteMsg:
		if m.snap.Auth.Kind != core.AuthReady {
			m.auth.input = append(m.auth.input, []rune(strings.TrimSpace(msg.Content))...)
		} else if m.pathIn != nil {
			m.pathIn.insert(strings.TrimSpace(msg.Content))
			m.listPaths()
		} else if ps := pastedPaths(msg.Content); ps != nil && m.focus == focusChat && m.canWrite() {
			m.attach, m.insert = append(m.attach, ps...), true
		} else if m.insert {
			m.comp.insert(msg.Content)
		}
	case tea.KeyPressMsg:
		cmd = m.key(msg)
	}
	return m, tea.Batch(cmd, m.after(), m.timers())
}

// timers runs the spinner while something is in flight, and clears a status-line message
// after 4 s.
func (m *model) timers() tea.Cmd {
	var cmds []tea.Cmd
	if m.busy() && !m.ticking {
		m.ticking = true
		cmds = append(cmds, tea.Tick(100*time.Millisecond, func(time.Time) tea.Msg { return tickMsg{} }))
	}
	if m.flash != m.flashSeen {
		m.flashSeen = m.flash
		m.flashN++
		n := m.flashN
		cmds = append(cmds, tea.Tick(4*time.Second, func(time.Time) tea.Msg { return clearFlashMsg(n) }))
	}
	return tea.Batch(cmds...)
}

// busy: a spinner is on screen.
func (m *model) busy() bool {
	switch {
	case m.snap.Auth.Kind == core.AuthStarting, m.snap.Auth.Kind == core.AuthClosing, m.auth.busy,
		m.snap.Auth.Kind == core.AuthQR && !m.auth.phoneEntry:
		return true
	case m.snap.Auth.Kind != core.AuthReady:
		return false
	}
	return m.win.Loading && m.open != 0 || m.pick != nil && m.pick.busy || m.rec != nil || m.play != nil && m.play.cmd != nil ||
		m.snap.Conn == "Connecting" || m.snap.Conn == "ConnectingToProxy" || m.snap.Conn == "Updating"
}

func (m *model) spinner() string { return string([]rune("⠋⠙⠹⠸⠼⠴⠦⠧⠇⠏")[m.spin%10]) }

// refresh takes the current chats and messages from core.
func (m *model) refresh() {
	if m.snap.Auth.Kind != core.AuthReady {
		return
	}
	m.chats = m.c.Chats(m.tabs()[min(m.tab, len(m.tabs())-1)].ID)
	m.win = m.c.Window()
	if i, ok := m.indexOf(m.jumpTo); ok && m.jumpTo != 0 && !m.win.Loading && m.win.ChatID == m.open {
		m.jumpTo, m.insert = 0, false
		m.anchor, m.off = 0, 0
		m.selectMsg(i)
	}
	m.sel = 0
	for i, ch := range m.chats {
		if ch.ID == m.selID {
			m.sel = i
		}
	}
	m.selectRow(m.sel)
}

func (m *model) selectRow(i int) {
	m.sel = max(min(i, len(m.chats)-1), 0)
	m.selID = 0
	if len(m.chats) > 0 {
		m.selID = m.chats[m.sel].ID
	}
}

// tabs are the folder tabs: Main and the user's folders, then the archive.
func (m *model) tabs() []core.List {
	return append(append([]core.List{}, m.snap.Lists...), core.List{ID: core.Archive, Name: "Archive"})
}

func (m *model) setTab(i int) tea.Cmd {
	tabs := m.tabs()
	m.tab = (i + len(tabs)) % len(tabs)
	m.selID, m.listTop = 0, 0
	m.refresh()
	l := tabs[m.tab].ID
	return func() tea.Msg { m.c.LoadList(l); return nil }
}

func (m *model) openChat(id int64) tea.Cmd {
	return m.enterChat(id, func() { m.c.OpenChat(id) })
}

// enterChat makes id the open chat on screen and runs load, the core call that opens it.
func (m *model) enterChat(id int64, load func()) tea.Cmd {
	if id == 0 {
		return nil
	}
	if id != m.open {
		if m.open != 0 {
			m.prevOpen = m.open
		}
		m.open = id
		m.anchor, m.off, m.viewedMax, m.cur, m.replyTo, m.editID = 0, 0, 0, 0, 0, 0
		m.cache = map[lineKey]rendered{}
		m.win = core.Window{ChatID: id, Loading: true}
		m.attach, m.pathIn, m.mention = nil, nil, nil
		if m.images == termimg.Kitty {
			m.sendQ, m.avatars = append(m.sendQ, termimg.DeleteAll()), map[avKey][]string{}
		}
	}
	m.focus = focusChat
	m.insert = m.canWrite() && m.jumpTo == 0 && (m.o.Config.Keys.OpenIn != "normal" || !m.vim())
	return func() tea.Msg { load(); return nil }
}

// closeChat leaves the open chat: the pane shows "No chat open" and its messages are no
// longer marked read.
func (m *model) closeChat() tea.Cmd {
	if m.open == 0 {
		return nil
	}
	m.prevOpen, m.open = m.open, 0
	m.win, m.cache, m.focus = core.Window{}, map[lineKey]rendered{}, focusList
	m.cur, m.replyTo, m.editID, m.attach, m.pathIn, m.mention = 0, 0, 0, nil, nil, nil
	if m.images == termimg.Kitty {
		m.sendQ, m.avatars = append(m.sendQ, termimg.DeleteAll()), map[avKey][]string{}
	}
	return func() tea.Msg { m.c.CloseChat(); return nil }
}

func (m *model) openChatOf(ch *core.Chat) tea.Cmd {
	if ch == nil {
		return nil
	}
	return m.openChat(ch.ID)
}

func (m *model) canWrite() bool {
	ch := m.c.Chat(m.open)
	return ch == nil || ch.Kind != core.ChatChannel
}

// step opens the previous (-1) or next (+1) chat of the list, as Alt+↑/↓ in Telegram.
func (m *model) step(d int) tea.Cmd {
	i := m.sel
	for j, ch := range m.chats {
		if ch.ID == m.open {
			i = j + d
		}
	}
	if i < 0 || i >= len(m.chats) {
		return nil
	}
	m.selectRow(i)
	insert := m.insert
	cmd := m.openChat(m.chats[i].ID)
	m.insert = insert && m.canWrite()
	return cmd
}

// after runs after every update: marks what's on screen as read, and loads more history
// when the pane gets near the end of what is loaded.
func (m *model) after() tea.Cmd {
	var cmds []tea.Cmd
	if m.snap.Auth.Kind == core.AuthReady && m.w > 0 {
		cmds = m.warmAvatars()
	}
	if m.snap.Auth.Kind != core.AuthReady || m.open == 0 || m.win.ChatID != m.open || m.w == 0 || !m.chatShown() {
		if len(m.sendQ) > 0 { // e.g. the kitty clean-up of the chat just left
			q := strings.Join(m.sendQ, "")
			m.sendQ = nil
			cmds = append(cmds, tea.Raw(q))
		}
		return tea.Batch(cmds...)
	}
	// Rendering here, before View, also queues the kitty images that View will place.
	_, ids, top, bottom := m.messageRows()
	cmds = append(cmds, m.mediaWork(ids)...)
	if n := len(ids); n > 0 && ids[n-1] > m.viewedMax {
		m.viewedMax = ids[n-1]
		cmds = append(cmds, func() tea.Msg { m.c.Viewed(ids); return nil })
	}
	if !m.win.Loading && !m.win.AtOldest && top < 10 {
		cmds = append(cmds, func() tea.Msg { m.c.LoadOlder(); return nil })
	}
	if !m.win.Loading && !m.win.AtNewest && bottom > len(m.win.Messages)-10 {
		cmds = append(cmds, func() tea.Msg { m.c.LoadNewer(); return nil })
	}
	return tea.Batch(cmds...)
}

func (m *model) View() tea.View {
	v := tea.NewView("")
	v.AltScreen, v.ReportFocus = true, true
	if m.w == 0 || m.h == 0 {
		return v
	}
	h := m.h - 1
	var screen []string
	switch {
	case m.snap.Auth.Kind != core.AuthReady:
		screen = m.authView(h)
	case m.help:
		screen = m.helpView(h)
	case m.settings:
		screen = m.settingsView(h)
	default:
		screen = m.mainView(h)
		switch {
		case m.menu != nil:
			overlay(screen, m.menuBox(), m.w)
		case m.askLogout:
			overlay(screen, m.logoutBox(), m.w)
		case m.pick != nil:
			overlay(screen, m.pickerBox(h), m.w)
		}
	}
	screen = append(screen, m.statusBar())
	for i := range m.h {
		l := ""
		if i < len(screen) {
			l = screen[i]
		}
		if ansi.StringWidth(l) != m.w {
			l = cut(l, m.w)
		}
		screen[i] = paint(l)
	}
	v.SetContent(strings.Join(screen[:m.h], "\n"))
	return v
}

// Layout (handoff "Layout rules"): the chat list at 31 % of the width (27 to 44 columns),
// a one-column gap, the chat pane with the composer under it, the status line last.
// Below 70 columns one pane at a time: the list, or the open chat.
func (m *model) listWidth() int {
	if m.w < 70 {
		return m.w
	}
	return min(max(int(float64(m.w)*0.31+0.5), 27), 44)
}

func (m *model) chatWidth() int {
	if m.w < 70 {
		return m.w
	}
	return m.w - m.listWidth() - 1
}

func (m *model) chatShown() bool { return m.w >= 70 || m.focus == focusChat }

func (m *model) mainView(h int) []string {
	if m.w < 70 {
		if m.focus == focusList {
			return m.listPane(m.w, h)
		}
		return m.chatPane(m.w, h)
	}
	left, right := m.listPane(m.listWidth(), h), m.chatPane(m.chatWidth(), h)
	out := make([]string, h)
	for i := range out {
		out[i] = left[i] + " " + right[i]
	}
	return out
}

// k writes a key the way the keyboard layout types it (Settings › Keys › Keyboard layout).
func (m *model) k(key string) string {
	if m.o.Config.Keys.Layout != "it" {
		return key
	}
	return strings.NewReplacer("ctrl+\\", "ctrl+ù", "[", "è", "]", "+", "/", "-", "?", "_").Replace(key)
}

// hints are the keys of what has the focus, most useful first.
func (m *model) hints() []string {
	vim := m.vim()
	switch {
	case m.snap.Auth.Kind != core.AuthReady:
		return []string{"ctrl+q quit"}
	case m.help:
		return []string{"j k scroll", "esc close"}
	case m.settings:
		return []string{"↑ ↓ move", "← → change", "enter run", "esc close"}
	case m.menu != nil && m.menu.del:
		return []string{"↑ ↓ move", "enter choose", "esc cancel"}
	case m.menu != nil:
		return []string{"↑ ↓ move", "enter run", "esc close"}
	case m.askLogout:
		return []string{"y log out", "n cancel"}
	case m.pick != nil:
		act := map[pickKind]string{pickForward: "enter send", pickContacts: "enter open chat", pickNew: "enter open chat",
			pickChats: "enter open", pickPalette: "enter open", pickMessages: "enter jump", pickInChat: "enter jump", pickJump: "enter go"}[m.pick.kind]
		return []string{"type to search", act, "esc close"}
	case m.rec != nil:
		return []string{"enter send", "esc cancel"}
	case m.pathIn != nil:
		return []string{"tab complete", "enter attach", "esc cancel"}
	case m.focus == focusList:
		read := ""
		if m.sel < len(m.chats) && (m.chats[m.sel].Unread > 0 || m.chats[m.sel].MarkedUnread) {
			read = map[bool]string{true: "R mark read", false: "ctrl+r mark read"}[vim]
		}
		if vim {
			return slices.DeleteFunc([]string{"j k move", "enter open", read, "1–9 folders", m.k("/") + " search", "n new chat", "m chat menu", m.k("?") + " keys", ", settings"}, isEmpty)
		}
		return slices.DeleteFunc([]string{"↑ ↓ move", "enter open", read, "← → folders", "type to find", m.k("ctrl+\\") + " chat menu", "ctrl+n new chat", "> settings"}, isEmpty)
	case m.cur != 0 && m.selected() != nil:
		return m.msgHints(*m.selected(), m.insert || !vim)
	case m.insert:
		return []string{"enter send", "alt+enter new line", "ctrl+↑ select", m.k("ctrl+\\") + " chat menu", "ctrl+o attach", "ctrl+t record"}
	case !vim:
		return []string{"↑ ↓ move", "alt+o open", "alt+s save", "alt+f forward", "esc chats"}
	case !m.canWrite():
		return []string{"j k move", "o open", "s save", "f forward", "esc chats"}
	}
	return []string{"j k move", "r reply", "f forward", "o open", "s save", "D delete", "i type", "esc chats"}
}

func isEmpty(s string) bool { return s == "" }

// msgHints are the keys for the selected message, what it offers first: play a voice or
// audio message, open a file or a link, show a spoiler; then reply, edit (own text),
// forward, delete. alt: the alt+letter forms (while typing, or the telegram keymap).
func (m *model) msgHints(sel core.Message, alt bool) []string {
	key := func(k string) string {
		if !alt {
			return k
		}
		if a, ok := map[string]string{"D": "alt+d", "space": "alt+v"}[k]; ok {
			return a
		}
		return "alt+" + k
	}
	var h []string
	add := func(k, what string) { h = append(h, key(k)+" "+what) }
	md := sel.Media
	switch {
	case md != nil && md.File != 0 && (sel.Kind == "VoiceNote" || sel.Kind == "Audio"):
		p := m.playing(sel.ID)
		add("p", map[bool]string{true: "pause", false: "play"}[p != nil && p.cmd != nil])
		if p != nil {
			h = append(h, key("<")+" "+key(">")+" seek")
		}
		add("s", "save")
	case md != nil && md.File != 0:
		add("o", "open")
		add("s", "save")
	case msgLink(sel) != "":
		add("o", "open link")
	}
	if !m.revealed[sel.ID] && slices.ContainsFunc(sel.Spans, func(s core.Span) bool { return s.Format&core.Spoiler != 0 }) {
		add("space", "show spoiler")
	}
	if m.canWrite() {
		add("r", "reply")
	}
	if sel.Outgoing && sel.Kind == "Text" {
		add("e", "edit")
	}
	add("f", "forward")
	if m.canWrite() {
		add("D", "delete")
	}
	if alt {
		return append(h, "esc cancel")
	}
	if m.canWrite() {
		h = append(h, "i type")
	}
	return append(h, "esc chats")
}

// state is the one system state the status line shows on the right, by priority.
func (m *model) state() string {
	switch m.snap.Conn {
	case "WaitingForNetwork":
		return connSt.Render("waiting for network")
	case "Connecting", "ConnectingToProxy":
		return connSt.Render(m.spinner() + " connecting")
	case "Updating":
		return connSt.Render(m.spinner() + " updating")
	}
	switch {
	case m.newVersion != "":
		return updateSt.Render("↑ tgtui " + m.newVersion + " available")
	case m.o.Warn != "":
		return warnSt.Render(m.o.Warn)
	}
	return ""
}

// statusBar: the hints of the focused pane or a message (4 s) left; the system state and
// F1 help right. The : command line takes it over.
func (m *model) statusBar() string {
	var right []string
	if s := m.state(); s != "" && m.snap.Auth.Kind == core.AuthReady {
		right = append(right, s)
	}
	switch {
	case m.settings:
		right = append(right, dim.Render("saved to config.toml"))
	case m.help && m.vim():
		right = append(right, dim.Render("keymap and layout: , settings › Keys"))
	case m.help:
		right = append(right, dim.Render("keymap and layout: f2 settings › Keys"))
	}
	rs := strings.Join(append(right, dim.Render("F1 help")), "  ")
	rx := max(m.w-1-ansi.StringWidth(rs), 1)
	room := rx - 4
	left := ""
	if m.o.TestDC {
		left = selFill.Render(" TEST SERVERS ") + " "
		room -= 15
	}
	switch {
	case m.flash != "":
		st := dim
		if strings.HasPrefix(m.flash, "✓") {
			st = okSt
		} else if strings.HasPrefix(m.flash, "✕") {
			st = bad
		}
		left += st.Render(ansi.Truncate(m.flash, max(room, 0), "…"))
	case m.snap.Err != "":
		left += bad.Render(ansi.Truncate("✕ "+m.snap.Err, max(room, 0), "…"))
	default:
		left += dim.Render(fitHints(m.hints(), room))
	}
	return " " + cut(left, rx-1) + rs + " "
}

// fitHints joins as many hints as fit in room cells.
func fitHints(hs []string, room int) string {
	out := ""
	for i, h := range hs {
		cand := h
		if i > 0 {
			cand = out + " · " + h
		}
		if ansi.StringWidth(cand) > room {
			if i == 0 {
				return ansi.Truncate(h, max(room, 0), "…")
			}
			break
		}
		out = cand
	}
	return out
}

// errText turns the TDLib errors a user can act on into advice.
func errText(err error) string {
	s := err.Error()
	for code, msg := range map[string]string{
		"API_ID_INVALID":         "Telegram rejected the api_id / api_hash: check them at my.telegram.org",
		"API_ID_PUBLISHED_FLOOD": "This api_id is blocked: set your own with TGTUI_API_ID / TGTUI_API_HASH",
		"PHONE_NUMBER_INVALID":   "Invalid phone number: include the country code (+…)",
		"PHONE_CODE_INVALID":     "That code is wrong. Check it and try again.",
		"PHONE_CODE_EXPIRED":     "The code expired: press esc and request a new one",
		"PASSWORD_HASH_INVALID":  "That password is wrong. Check it and try again.",
	} {
		if strings.Contains(s, code) {
			return msg
		}
	}
	return s
}
