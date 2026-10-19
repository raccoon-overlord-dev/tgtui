package core

import (
	"cmp"
	"encoding/json"
	"slices"
	"strings"
)

type Message struct {
	ID       int64
	ChatID   int64
	SenderID int64  // user or chat
	Sender   string // its name when the message arrived
	Outgoing bool
	Date     int64 // unix seconds
	EditDate int64
	Kind     string // TDLib's content type without "message": Text, Photo, Sticker, ChatAddMembers…
	Text     string // text, caption or poll question
	Detail   string // sticker emoji, document file name
	Spans    []Span // formatting of Text
	Media    *Media
	Preview  *LinkPreview
	ReplyTo  int64  // the message this one replies to, in the same chat (see Replied)
	Quote    string // the part of it quoted, if any
	Sending  bool
	Failed   bool

	entities json.RawMessage // TDLib's, for getMarkdownText
	reply    *Message        // a replied message from another chat, as TDLib sends it
}

// Window is the part of the open chat's history that is loaded, oldest message first.
// The UI asks for more at either end (LoadOlder, LoadNewer) and core drops the far end, so
// a chat of any length costs at most windowCap messages.
type Window struct {
	ChatID   int64
	Messages []Message
	AtOldest bool // the chat's first message is loaded
	AtNewest bool // its last message is loaded (new messages are then appended live)
	Loading  bool
}

const (
	pageSize  = 50
	windowCap = 1000 // ponytail: fixed, generous for a terminal; tune with the scroll benchmark
)

type tdMessage struct {
	ID     int64 `json:"id"`
	ChatID int64 `json:"chat_id"`
	Sender struct {
		Type   string `json:"@type"`
		UserID int64  `json:"user_id"`
		ChatID int64  `json:"chat_id"`
	} `json:"sender_id"`
	SendingState *struct {
		Type string `json:"@type"`
	} `json:"sending_state"`
	Outgoing bool            `json:"is_outgoing"`
	Date     int64           `json:"date"`
	EditDate int64           `json:"edit_date"`
	Content  json.RawMessage `json:"content"`
	ReplyTo  *struct {
		Type      string `json:"@type"`
		ChatID    int64  `json:"chat_id"`
		MessageID int64  `json:"message_id"`
		Quote     *struct {
			Text formattedText `json:"text"`
		} `json:"quote"`
		Content json.RawMessage `json:"content"`
	} `json:"reply_to"`
}

// message converts a TDLib message. Called with mu held (it reads sender names).
func (c *Core) message(t *tdMessage) *Message {
	m := &Message{ID: t.ID, ChatID: t.ChatID, Outgoing: t.Outgoing, Date: t.Date, EditDate: t.EditDate}
	if t.Sender.Type == "messageSenderChat" {
		m.SenderID = t.Sender.ChatID
		if ch := c.chats[m.SenderID]; ch != nil {
			m.Sender = ch.Title
		}
	} else {
		m.SenderID, m.Sender = t.Sender.UserID, c.users[t.Sender.UserID]
	}
	if t.SendingState != nil {
		m.Sending = t.SendingState.Type == "messageSendingStatePending"
		m.Failed = t.SendingState.Type == "messageSendingStateFailed"
	}
	c.setContent(m, t.Content)
	if r := t.ReplyTo; r != nil && r.Type == "messageReplyToMessage" {
		if r.Quote != nil {
			m.Quote = r.Quote.Text.Text
		}
		if r.ChatID == t.ChatID || r.ChatID == 0 {
			m.ReplyTo = r.MessageID
		} else {
			m.reply = &Message{ID: r.MessageID, ChatID: r.ChatID}
			if ch := c.chats[r.ChatID]; ch != nil {
				m.reply.Sender = ch.Title
			}
			c.setContent(m.reply, r.Content)
		}
	}
	return m
}

func (c *Core) setContent(m *Message, raw json.RawMessage) {
	c.setMedia(m, raw)
	var ct struct {
		Type    string        `json:"@type"`
		Text    formattedText `json:"text"`
		Caption formattedText `json:"caption"`
		Sticker struct {
			Emoji string `json:"emoji"`
		} `json:"sticker"`
		Document struct {
			FileName string `json:"file_name"`
		} `json:"document"`
		Poll struct {
			Question formattedText `json:"question"`
		} `json:"poll"`
	}
	json.Unmarshal(raw, &ct) // a field of another shape in some content type only leaves it empty
	m.Kind, m.Detail = strings.TrimPrefix(ct.Type, "message"), ct.Sticker.Emoji+ct.Document.FileName
	t := ct.Text
	if ct.Caption.Text != "" {
		t = ct.Caption
	} else if ct.Poll.Question.Text != "" {
		t = ct.Poll.Question
	}
	m.Text, m.entities, m.Spans = t.Text, t.Entities, spans(t.Text, t.Entities)
}

// applyMessage handles updates about messages of the open chat. Called with mu held.
func (c *Core) applyMessage(u *update) {
	w := &c.win
	switch u.Type {
	case "updateNewMessage":
		if u.Message != nil && u.Message.ChatID == w.ChatID && w.AtNewest {
			c.merge([]Message{*c.message(u.Message)}, false)
		}
	case "updateMessageSendSucceeded", "updateMessageSendFailed":
		if u.Message != nil && u.Message.ChatID == w.ChatID {
			w.Messages = slices.DeleteFunc(slices.Clone(w.Messages), func(m Message) bool { return m.ID == u.OldMessageID })
			c.merge([]Message{*c.message(u.Message)}, false)
		}
	case "updateMessageContent":
		c.edit(u.ChatID, u.MessageID, func(m *Message) { c.setContent(m, u.NewContent) })
	case "updateMessageEdited":
		c.edit(u.ChatID, u.MessageID, func(m *Message) { m.EditDate = u.EditDate })
	case "updateDeleteMessages":
		if u.ChatID == w.ChatID && u.IsPermanent { // not permanent: only dropped from TDLib's cache
			w.Messages = slices.DeleteFunc(slices.Clone(w.Messages), func(m Message) bool { return slices.Contains(u.MessageIDs, m.ID) })
		}
	}
}

func (c *Core) edit(chatID, id int64, f func(*Message)) {
	if chatID != c.win.ChatID {
		return
	}
	if i, ok := c.find(id); ok {
		c.win.Messages = slices.Clone(c.win.Messages)
		f(&c.win.Messages[i])
	}
}

// merge adds ms to the window and keeps it within windowCap, dropping the newest messages
// when older ones were loaded (older), the oldest otherwise. Called with mu held.
func (c *Core) merge(ms []Message, older bool) {
	w := &c.win
	all := append(slices.Clone(w.Messages), ms...)
	slices.SortStableFunc(all, func(a, b Message) int { return cmp.Compare(a.ID, b.ID) })
	// Keep the last copy of each id: newer data was appended after the old.
	out := all[:0]
	for i, m := range all {
		if i+1 < len(all) && all[i+1].ID == m.ID {
			continue
		}
		out = append(out, m)
	}
	if over := len(out) - windowCap; over > 0 {
		if older {
			out, w.AtNewest = out[:windowCap], false
		} else {
			out, w.AtOldest = out[over:], false
		}
	}
	w.Messages = out
	c.fetchReplies(ms)
}

func (c *Core) find(id int64) (int, bool) {
	return slices.BinarySearchFunc(c.win.Messages, id, func(m Message, id int64) int { return cmp.Compare(m.ID, id) })
}

// fetchReplies loads the messages that ms reply to and the window doesn't hold. Called with mu held.
func (c *Core) fetchReplies(ms []Message) {
	for _, m := range ms {
		key := [2]int64{m.ChatID, m.ReplyTo}
		if _, ok := c.replies[key]; m.ReplyTo == 0 || ok {
			continue
		}
		if _, ok := c.find(m.ReplyTo); ok {
			continue
		}
		if len(c.replies) > 2000 { // ponytail: crude bound; a reply fetched twice costs one local lookup
			clear(c.replies)
		}
		c.replies[key] = nil
		go func() {
			res, err := c.td.Send(map[string]any{"@type": "getMessage", "chat_id": key[0], "message_id": key[1]})
			var t tdMessage
			c.locked(func() {
				if err != nil || json.Unmarshal(res, &t) != nil {
					c.replies[key] = &Message{ID: key[1], ChatID: key[0], Kind: "Deleted"}
					return
				}
				c.replies[key] = c.message(&t)
			})
		}()
	}
}

// Replied returns the message m replies to, or nil when there is none or it isn't loaded yet.
func (c *Core) Replied(m Message) *Message {
	if m.reply != nil || m.ReplyTo == 0 {
		return m.reply
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if m.ChatID == c.win.ChatID {
		if i, ok := c.find(m.ReplyTo); ok {
			r := c.win.Messages[i]
			return &r
		}
	}
	return c.replies[[2]int64{m.ChatID, m.ReplyTo}]
}

// Window returns a copy of the open chat's loaded messages.
func (c *Core) Window() Window {
	c.mu.Lock()
	defer c.mu.Unlock()
	w := c.win
	w.Messages = slices.Clone(w.Messages)
	return w
}

// history asks TDLib for messages of chat id around from (newest first in TDLib's answer).
func (c *Core) history(id, from int64, offset, limit int) []Message {
	res, err := c.td.Send(map[string]any{"@type": "getChatHistory", "chat_id": id,
		"from_message_id": from, "offset": offset, "limit": limit})
	if err != nil {
		return nil
	}
	var r struct {
		Messages []*tdMessage `json:"messages"`
	}
	json.Unmarshal(res, &r)
	c.mu.Lock()
	defer c.mu.Unlock()
	out := make([]Message, 0, len(r.Messages))
	for _, t := range r.Messages {
		if t != nil {
			out = append(out, *c.message(t))
		}
	}
	return out
}

// older fetches up to pageSize messages older than from (0: from the newest). TDLib's
// first answers may hold only what it has cached, so it asks until the page is full; ok is
// false when the chat has no more.
func (c *Core) older(id, from int64) (ms []Message, ok bool) {
	for len(ms) < pageSize {
		got := 0
		for _, m := range c.history(id, from, 0, pageSize-len(ms)+1) { // +1: room for from
			if from == 0 || m.ID < from { // the answer may start with from itself
				ms = append(ms, m)
				got++
			}
		}
		if got == 0 {
			return ms, false
		}
		from = ms[len(ms)-1].ID
	}
	return ms, true
}

// OpenChat makes id the open chat and loads its newest messages.
func (c *Core) OpenChat(id int64) {
	c.switchTo(id)
	ms, more := c.older(id, 0)
	c.locked(func() {
		if c.win.ChatID == id {
			c.merge(ms, true)
			c.win.AtOldest, c.win.AtNewest, c.win.Loading = !more, true, false
		}
	})
}

// OpenChatAt opens chat id on message msg (a search result), with history around it.
func (c *Core) OpenChatAt(id, msg int64) {
	c.switchTo(id)
	ms := c.history(id, msg, -pageSize/2, pageSize)
	more := true
	if len(ms) > 0 {
		var old []Message
		old, more = c.older(id, ms[len(ms)-1].ID)
		ms = append(ms, old...)
	}
	c.locked(func() {
		if c.win.ChatID != id {
			return
		}
		c.win.AtNewest = false // merge must not drop the newer side
		c.merge(ms, true)
		w := &c.win
		ch := c.chats[id]
		w.AtOldest, w.Loading = !more, false
		w.AtNewest = len(w.Messages) == 0 || ch == nil || ch.Last == nil || w.Messages[len(w.Messages)-1].ID >= ch.Last.ID
	})
}

// switchTo closes the open chat and opens id with an empty window.
func (c *Core) switchTo(id int64) {
	c.mu.Lock()
	prev := c.win.ChatID
	c.win, c.viewed = Window{ChatID: id, Loading: true, AtNewest: true}, 0 // AtNewest: take live messages while loading
	c.mu.Unlock()
	c.notify()
	if prev != 0 && prev != id {
		c.send(map[string]any{"@type": "closeChat", "chat_id": prev})
	}
	c.send(map[string]any{"@type": "openChat", "chat_id": id})
}

// OpenSaved opens Saved Messages, the chat with oneself.
func (c *Core) OpenSaved() {
	me := c.Snapshot().Me
	if me == nil {
		return
	}
	c.send(map[string]any{"@type": "createPrivateChat", "user_id": me.ID})
	c.OpenChat(me.ID) // a private chat's id is the user's id
}

// start marks the window loading for a page request, and says where to start from.
func (c *Core) start(oldEnd bool) (id, from int64, ok bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	w := &c.win
	if w.Loading || len(w.Messages) == 0 || oldEnd && w.AtOldest || !oldEnd && w.AtNewest {
		return 0, 0, false
	}
	w.Loading = true
	if oldEnd {
		return w.ChatID, w.Messages[0].ID, true
	}
	return w.ChatID, w.Messages[len(w.Messages)-1].ID, true
}

// LoadOlder loads the page before the window, if there is one and nothing else is loading.
func (c *Core) LoadOlder() {
	id, from, ok := c.start(true)
	if !ok {
		return
	}
	ms, more := c.older(id, from)
	c.locked(func() {
		if c.win.ChatID == id {
			c.merge(ms, true)
			c.win.AtOldest, c.win.Loading = !more, false
		}
	})
}

// LoadNewer loads the page after the window, after scrolling back down.
func (c *Core) LoadNewer() {
	id, from, ok := c.start(false)
	if !ok {
		return
	}
	ms := c.history(id, from, -(pageSize - 1), pageSize)
	c.locked(func() {
		if c.win.ChatID != id {
			return
		}
		c.merge(ms, false)
		w := &c.win
		newest := w.Messages[len(w.Messages)-1].ID
		last := c.chats[id]
		w.AtNewest = newest == from || last == nil || last.Last == nil || newest >= last.Last.ID
		w.Loading = false
	})
}

// Viewed tells TDLib which messages of the open chat are on screen, which marks them read.
func (c *Core) Viewed(ids []int64) {
	if len(ids) == 0 {
		return
	}
	c.mu.Lock()
	id, newest := c.win.ChatID, slices.Max(ids)
	if newest <= c.viewed {
		c.mu.Unlock()
		return
	}
	c.viewed = newest
	c.mu.Unlock()
	c.send(map[string]any{"@type": "viewMessages", "chat_id": id, "message_ids": ids, "force_read": true})
}
