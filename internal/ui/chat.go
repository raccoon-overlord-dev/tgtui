package ui

import (
	"cmp"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/raccoon-overlord-dev/tgtui/internal/core"
	"github.com/raccoon-overlord-dev/tgtui/internal/ui/termimg"
)

// lineKey is everything a message's rendered lines depend on.
type lineKey struct {
	id, edit             int64
	text, reply          string
	width                int
	head, day, gap, read bool
	sending, failed      bool
	revealed             bool
	img, file, prev      string // image source, file state, link preview
}

// rendered is a message's lines; lines[start:] are the message itself, after the day
// separator and spacing, and get the selection mark.
type rendered struct {
	lines []string
	start int
}

// kinds names what a non-text message is; service lists the events drawn as a centred line.
var kinds = map[string]string{
	"Photo": "photo", "Video": "video", "Document": "file", "Sticker": "sticker", "Animation": "GIF",
	"Audio": "audio", "VoiceNote": "voice", "VideoNote": "video message", "Poll": "poll",
	"Location": "location", "Venue": "venue", "Contact": "contact", "Call": "call", "Dice": "dice",
	"Story": "story", "Unsupported": "unsupported message", "Deleted": "deleted message",
}

var service = map[string]string{
	"PinMessage": "pinned a message", "ChatAddMembers": "added members", "ChatJoinByLink": "joined via link",
	"ChatDeleteMember": "left or was removed", "ChatChangeTitle": "changed the title",
	"ChatChangePhoto": "changed the photo", "BasicGroupChatCreate": "created the group",
	"SupergroupChatCreate": "created the group", "ContactRegistered": "joined Telegram",
}

// fileKinds are drawn as a file block: ┃ file  name  size.
var fileKinds = map[string]bool{"Document": true, "Audio": true, "VoiceNote": true, "Video": true, "VideoNote": true, "Animation": true}

// kindWord is what a non-text message is, "" for text and service events.
func kindWord(msg core.Message) string {
	if msg.Kind == "Text" || service[msg.Kind] != "" {
		return ""
	}
	if w, ok := kinds[msg.Kind]; ok {
		return w
	}
	return strings.ToLower(msg.Kind)
}

func duration(s int) string { return fmt.Sprintf("%d:%02d", s/60, s%60) }

func joinNonEmpty(sep string, parts ...string) string {
	return strings.Join(slices.DeleteFunc(parts, func(s string) bool { return s == "" }), sep)
}

// label is a message as one line of plain text: its text, "photo · caption", "file · name",
// "voice · 0:42", "Marco pinned a message".
func label(msg core.Message) string {
	if v := service[msg.Kind]; v != "" {
		return senderName(msg) + " " + v
	}
	w := kindWord(msg)
	if w == "" {
		return msg.Text
	}
	detail := msg.Detail // file name, sticker emoji
	if detail == "" && msg.Text == "" && msg.Media != nil && msg.Media.Duration > 0 {
		detail = duration(msg.Media.Duration)
	}
	return joinNonEmpty(" · ", w, detail, msg.Text)
}

func oneLine(s string) string { return strings.ReplaceAll(s, "\n", " ") }

func (m *model) openChatInfo() *core.Chat { return m.c.Chat(m.open) }

func senderName(msg core.Message) string {
	if msg.Sender == "" {
		return "Deleted account"
	}
	return msg.Sender
}

func nameStyle(id int64) lipgloss.Style { return names[uint64(id)%uint64(len(names))] }

// replyBlock is the quote above a reply: ┃ Sender  quoted text. key is it as plain text.
func (m *model) replyBlock(msg core.Message, w int) (key, line string) {
	r := m.c.Replied(msg)
	if r == nil {
		if msg.ReplyTo != 0 {
			return "…", replyBar.Render("┃ ") + dim.Render("…") // being fetched
		}
		return "", ""
	}
	text := label(*r)
	if msg.Quote != "" {
		text = msg.Quote
	}
	line = replyBar.Render("┃ ") + nameStyle(r.SenderID).Render(senderName(*r)) + "  " + dim.Render(oneLine(text))
	return senderName(*r) + "\x00" + text, ansi.Truncate(line, w, "…")
}

// fileBlock is a message's file: ┃ file  name  size, and the download progress.
func (m *model) fileBlock(msg core.Message) string {
	md := msg.Media
	if md == nil || md.File == 0 || !fileKinds[msg.Kind] {
		return ""
	}
	f := m.c.File(md.File)
	var meta []string
	if md.Duration > 0 {
		meta = append(meta, duration(md.Duration))
	}
	if md.Size > 0 {
		meta = append(meta, size(md.Size))
	}
	switch {
	case f.Downloading && f.Size > 0:
		meta = append(meta, fmt.Sprintf("%d%%", f.Downloaded*100/f.Size))
	case f.Downloading:
		meta = append(meta, "↓")
	}
	name := ""
	if md.Name != "" {
		name = text.Render(md.Name)
	}
	return replyBar.Render("┃ ") + joinNonEmpty("  ", dim.Render(kinds[msg.Kind]), name, dim.Render(strings.Join(meta, " · ")))
}

// previewBlock is a link preview: site, title, description, one line each.
func previewBlock(p *core.LinkPreview, w int) []string {
	if p == nil || p.Title == "" && p.Description == "" {
		return nil
	}
	var out []string
	for _, l := range []string{dim.Render(p.Site), title.Render(oneLine(p.Title)), dim.Render(oneLine(p.Description))} {
		if ansi.StringWidth(l) > 0 {
			out = append(out, ansi.Truncate(previewBar.Render("┃ ")+l, w, "…"))
		}
	}
	return out
}

func dayLabel(t time.Time) string {
	if t.Year() != time.Now().Year() {
		return t.Format("Mon 2 Jan 2006")
	}
	return t.Format("Mon 2 Jan")
}

// msgLines renders message i of the window in w cells (the pane's inner width, padding
// columns included): the day separator, a blank row before a new group, the sender line
// (when the sender changes or 5 minutes pass), then the blocks. Own messages are right
// aligned, without a name line, with the time and state after the last line.
func (m *model) msgLines(i, w int, ch *core.Chat) rendered {
	ms := m.win.Messages
	msg := ms[i]
	t := time.Unix(msg.Date, 0)
	svc := service[msg.Kind] != ""
	day, head, gap := true, true, false
	if i > 0 {
		p := ms[i-1]
		day = !sameDay(time.Unix(p.Date, 0), t)
		head = day || p.SenderID != msg.SenderID || p.Outgoing != msg.Outgoing || msg.Date-p.Date > 300 || service[p.Kind] != ""
		gap = head && !day && !svc && service[p.Kind] == ""
	}
	cw := w - 2
	own := msg.Outgoing && !svc
	bw := cw
	if own {
		bw = min(max(20, cw*3/4), cw)
	}
	replyKey, reply := m.replyBlock(msg, min(bw, 44))
	var img, path string
	var data []byte
	if cw >= 40 { // a pane wide enough for pictures
		img, path, data = m.imageSource(msg)
	}
	file, prev := m.fileBlock(msg), ""
	if msg.Preview != nil {
		prev = msg.Preview.Site + msg.Preview.Title + msg.Preview.Description
	}
	key := lineKey{msg.ID, msg.EditDate, msg.Text, replyKey, w, head, day, gap, ch != nil && msg.ID <= ch.ReadOutbox,
		msg.Sending, msg.Failed, m.revealed[msg.ID], img, file, prev}
	if r, ok := m.cache[key]; ok {
		return r
	}
	blank := strings.Repeat(" ", w)
	var out []string
	if day {
		out = append(out, " "+center(dim.Render(dayLabel(t)), cw)+" ")
	}
	if gap {
		out = append(out, blank)
	}
	start := len(out)
	if svc {
		out = append(out, " "+center(dim.Render(label(msg)), cw)+" ")
		return m.store(key, rendered{out, start})
	}
	if head && !own {
		out = append(out, pad(nameStyle(msg.SenderID).Render(senderName(msg))+"  "+dim.Render(t.Format(m.clock)), cw))
	}
	var body []string
	if reply != "" {
		body = append(body, reply)
	}
	if img != "" {
		maxRows := 10
		if msg.Kind == "Sticker" {
			maxRows = 6
		}
		cols, rows := termimg.Size(msg.Media.W, msg.Media.H, min(bw, 60), maxRows)
		body = append(body, m.drawImage(path, data, cols, rows)...)
	}
	if file != "" {
		body = append(body, ansi.Truncate(file, bw, "…"))
	}
	text, spans := msg.Text, msg.Spans
	if w := kindWord(msg); w != "" && img == "" && file == "" { // the kind, dimmed, before the caption
		text = strings.TrimSpace(w + "  " + msg.Text)
		spans = []core.Span{{From: 0, To: len(w), Format: fDim}}
		for _, s := range msg.Spans {
			s.From, s.To = s.From+len(w)+2, s.To+len(w)+2
			spans = append(spans, s)
		}
	}
	if text != "" {
		body = append(body, renderText(text, spans, bw, key.revealed)...)
	}
	body = append(body, previewBlock(msg.Preview, bw)...)
	if own {
		mark := dim.Render("✓")
		switch {
		case msg.Failed:
			mark = bad.Render("✕ failed")
		case msg.Sending:
			mark = dim.Render("…")
		case key.read:
			mark = readMark.Render("✓✓")
		}
		meta := dim.Render(t.Format(m.clock)) + " " + mark
		if msg.EditDate != 0 {
			meta = dim.Render("edited ") + meta
		}
		if n := len(body); n > 0 && ansi.StringWidth(body[n-1])+2+ansi.StringWidth(meta) <= cw {
			body[n-1] += "  " + meta
		} else {
			body = append(body, meta)
		}
		for _, l := range body {
			out = append(out, " "+strings.Repeat(" ", max(cw-ansi.StringWidth(l), 0))+ansi.Truncate(l, cw, "…")+" ")
		}
	} else {
		for _, l := range body {
			out = append(out, pad(l, cw))
		}
	}
	return m.store(key, rendered{out, start})
}

func (m *model) store(key lineKey, r rendered) rendered {
	if len(m.cache) > 5000 {
		m.cache = map[lineKey]rendered{}
	}
	m.cache[key] = r
	return r
}

func (m *model) indexOf(id int64) (int, bool) {
	return slices.BinarySearchFunc(m.win.Messages, id, func(x core.Message, id int64) int { return cmp.Compare(x.ID, id) })
}

func (m *model) anchorIndex() int {
	if m.anchor == 0 {
		return len(m.win.Messages) - 1
	}
	i, _ := m.indexOf(m.anchor)
	return min(i, len(m.win.Messages)-1)
}

// msgWidth is the width of message lines: the chat pane inside its border.
func (m *model) msgWidth() int { return m.chatWidth() - 2 }

// messageRows is what the message pane shows, bottom-aligned on the anchor message: the
// rows, the ids of the messages they show (oldest first) and their window indexes.
func (m *model) messageRows() (rows []string, ids []int64, top, bottom int) {
	w, h := m.msgWidth(), m.paneHeight()
	if len(m.win.Messages) == 0 || h <= 0 {
		return nil, nil, 0, 0
	}
	ch := m.openChatInfo()
	i := m.anchorIndex()
	bottom, top = i, i
	skip := m.off
	for ; i >= 0 && len(rows) < h; i-- {
		r := m.msgLines(i, w, ch)
		ls := r.lines
		if m.win.Messages[i].ID == m.cur && m.focus == focusChat {
			ls = slices.Clone(ls)
			for j := r.start; j < len(ls); j++ {
				ls[j] = accent.Render("▌") + ls[j][1:] // lines[start:] begin with a plain space
			}
		}
		ls = ls[:max(len(ls)-skip, 0)]
		skip = 0
		if len(ls) == 0 {
			continue
		}
		take := min(len(ls), h-len(rows))
		rows = slices.Concat(ls[len(ls)-take:], rows) // a new slice: ls is the cache's
		ids, top = append(ids, m.win.Messages[i].ID), i
	}
	slices.Reverse(ids)
	return rows, ids, top, bottom
}

// scroll moves the pane n lines up (n > 0, older) or down.
func (m *model) scroll(n int) {
	ms := m.win.Messages
	if len(ms) == 0 {
		return
	}
	w, ch := m.msgWidth(), m.openChatInfo()
	lines := func(i int) int { return len(m.msgLines(i, w, ch).lines) }
	i, off := m.anchorIndex(), m.off+n
	for off < 0 && i < len(ms)-1 {
		i++
		off += lines(i)
	}
	off = max(off, 0)
	for i > 0 && off >= lines(i) {
		off -= lines(i)
		i--
	}
	if i == 0 {
		off = min(off, lines(0)-1)
	}
	m.anchor, m.off = ms[i].ID, off
	if i == len(ms)-1 && off == 0 && m.win.AtNewest {
		m.anchor = 0
	}
	// At the chat's start, don't leave the pane half empty.
	if rows, _, _, _ := m.messageRows(); n > 0 && m.win.AtOldest && len(rows) < m.paneHeight() {
		m.scroll(len(rows) - m.paneHeight())
	}
}

// selectMsg selects message i of the window and scrolls it into view.
func (m *model) selectMsg(i int) {
	ms := m.win.Messages
	if len(ms) == 0 {
		return
	}
	i = max(min(i, len(ms)-1), 0)
	m.cur = ms[i].ID
	_, _, top, bottom := m.messageRows()
	switch {
	case i > bottom || i == bottom && m.off > 0:
		m.anchor, m.off = m.cur, 0
		if i == len(ms)-1 && m.win.AtNewest {
			m.anchor = 0
		}
	case i < top || i == top && i > 0:
		m.anchor, m.off = m.cur, 0 // bottom-aligned, then up so it's at the top
		m.scroll(-(m.paneHeight() - len(m.msgLines(i, m.msgWidth(), m.openChatInfo()).lines)))
	}
}

// selected is the selected message, or nil.
func (m *model) selected() *core.Message {
	if i, ok := m.indexOf(m.cur); ok {
		return &m.win.Messages[i]
	}
	return nil
}

func fileKind(p string) string {
	switch strings.ToLower(filepath.Ext(p)) {
	case ".jpg", ".jpeg", ".png", ".webp", ".gif", ".heic":
		return "photo"
	}
	return "file"
}

func fileSize(p string) string {
	st, err := os.Stat(p)
	if err != nil {
		return ""
	}
	if st.IsDir() {
		return "folder"
	}
	return size(st.Size())
}

// composerPane is the composer box: 1 to 5 lines of text, then it scrolls. Its borders
// say what it does (reply, edit, files) and which keys apply. nil for channels.
func (m *model) composerPane(w int) []string {
	if !m.canWrite() {
		return nil
	}
	if m.pathIn != nil {
		return m.attachBox(w)
	}
	iw := w - 4
	focused := m.focus == focusChat && m.insert
	var head, bottom string
	var inner []string
	switch {
	case m.editID != 0:
		head, bottom = dim.Render("✎")+" "+title.Render("Editing"), "enter save · esc cancel"
	case m.replyTo != 0:
		who, what := "a message", ""
		if i, ok := m.indexOf(m.replyTo); ok {
			who, what = senderName(m.win.Messages[i]), oneLine(label(m.win.Messages[i]))
		}
		head, bottom = dim.Render("↳")+" "+title.Render(who)+"  "+dim.Render(what), "esc cancel reply"
	case len(m.attach) == 1:
		head, bottom = title.Render("1 file"), "ctrl+o add more · enter send"
	case len(m.attach) > 1:
		head, bottom = title.Render(fmt.Sprint(len(m.attach), " files"))+"  "+dim.Render("sent as an album"), "ctrl+o add more · enter send"
	}
	for _, p := range m.attach {
		inner = append(inner, row(iw, lipgloss.NewStyle(), replyBar.Render("┃ ")+dim.Render(fileKind(p))+"  "+filepath.Base(p), dim.Render(fileSize(p))))
	}
	if len(m.attach) > 0 {
		inner = append(inner, "")
	}
	switch {
	case m.comp.value() == "" && focused:
		inner = append(inner, pad(text.Reverse(true).Render(" ")+dim.Render("Message"), iw))
	case m.comp.value() == "":
		inner = append(inner, pad(dim.Render("Message"), iw))
	default:
		m.comp.hidden = !focused
		ls, cur := m.comp.lines(text, max(iw, 1))
		if len(ls) > 1 && bottom == "" && focused {
			bottom = "alt+enter new line"
		}
		from := max(min(cur-2, len(ls)-5), 0)
		for _, l := range ls[from:min(from+5, len(ls))] {
			inner = append(inner, pad(l, iw))
		}
	}
	if len(inner) > 7 { // many files: keep the text in view
		inner = inner[len(inner)-7:]
	}
	if mn := m.mention; mn != nil && len(mn.items) > 0 {
		bottom = "tab pick · ↑ ↓ move"
	}
	right := ""
	if focused && m.vim() {
		right = accent.Render("INSERT")
	}
	if bottom != "" {
		bottom = dim.Render(bottom)
	}
	return box(w, len(inner)+2, borderOf(focused), head, right, bottom, inner)
}

// attachBox replaces the composer while a path is typed: the path, then what it completes to.
func (m *model) attachBox(w int) []string {
	iw := w - 4
	inner := []string{pad(title.Render("› ")+m.pathIn.view(text, max(iw-2, 1)), iw)}
	for i, p := range m.pathItems {
		t, _, s, fill := rowStyles(i == m.pathSel)
		name := filepath.Base(p)
		info := fileSize(p)
		if info == "folder" {
			name += "/"
		}
		inner = append(inner, row(iw, fill, t.Render("  "+name), s.Render(info)))
	}
	return box(w, len(inner)+2, focusBorder, title.Render("Attach file"), "", dim.Render("tab complete · enter attach · esc cancel"), inner)
}

// paneHeight is the message area: the chat pane inside its border, above the composer.
func (m *model) paneHeight() int {
	h := m.h - 1 - len(m.composerPane(m.chatWidth())) - 2
	if !m.canWrite() {
		h-- // the read-only line
	}
	return h
}

// chatPane is the open chat and its composer, h lines of width w.
func (m *model) chatPane(w, h int) []string {
	if m.open == 0 {
		inner := make([]string, h-2)
		y := max(h/2-3, 0)
		find := m.k("/") + " find a chat · g s Saved Messages · g c contacts"
		if !m.vim() {
			find = "type to find a chat · ctrl+0 Saved Messages"
		}
		for i, l := range []string{title.Render("No chat open"), "", dim.Render("enter opens the highlighted chat"), dim.Render(find)} {
			if y+i < len(inner) {
				inner[y+i] = center(l, w-2)
			}
		}
		return box(w, h, borderSt, "", "", "", inner)
	}
	comp := m.composerPane(w)
	chH := h - len(comp)
	focused := m.focus == focusChat && (!m.insert || !m.canWrite()) && m.pathIn == nil
	ch := m.openChatInfo()
	head := ""
	if ch != nil {
		head = title.Render(m.chatName(ch)) + "  " + dim.Render([]string{"private", "group", "channel"}[ch.Kind])
		if m.w < 70 {
			head = dim.Render("‹ Chats") + "  " + title.Render(m.chatName(ch))
		}
	}
	right := ""
	switch {
	case m.win.Loading:
		right = dim.Render(m.spinner())
	case focused && m.vim():
		right = accent.Render("NORMAL")
	}
	bottom := ""
	if (m.anchor != 0 || !m.win.AtNewest) && len(m.win.Messages) > 0 {
		var parts []string
		if ch != nil && ch.Unread > 0 {
			parts = append(parts, fmt.Sprint("↓ ", ch.Unread, " new"))
		}
		if m.vim() {
			parts = append(parts, "G newest")
		}
		bottom = title.Render(strings.Join(parts, " · "))
	}
	ih := chH - 2
	ph := m.paneHeight()
	inner := make([]string, 0, ih)
	rows, _, _, _ := m.messageRows()
	switch {
	case len(rows) == 0 && (m.win.Loading || m.win.ChatID != m.open):
		inner = append(make([]string, ph/2), center(dim.Render(m.spinner()+" Loading messages"), w-2))
	case len(rows) == 0:
		inner = append(make([]string, max(ph/2-1, 0)), center(title.Render("No messages here yet"), w-2))
		if m.canWrite() {
			inner = append(inner, center(dim.Render("Write the first one below"), w-2))
		}
	default:
		inner = append(make([]string, max(ph-len(rows), 0)), rows...)
	}
	if !m.canWrite() {
		menu := "m"
		if !m.vim() {
			menu = m.k("ctrl+\\")
		}
		for len(inner) < ih-1 {
			inner = append(inner, "")
		}
		inner = append(inner[:ih-1], center(dim.Render("read-only channel · "+menu+" chat menu"), w-2))
	}
	out := append(box(w, chH, borderOf(focused), head, right, bottom, inner), comp...)
	if b := m.mentionBox(w - 2); b != nil && len(comp) > 0 {
		place(out, b, 1, chH-len(b), w)
	}
	return out
}
