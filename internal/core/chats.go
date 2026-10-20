package core

import (
	"cmp"
	"encoding/json"
	"slices"
	"strconv"
	"strings"
)

// ChatList names a TDLib chat list: Main, Archive or FolderList(id).
type ChatList string

const (
	Main    ChatList = "main"
	Archive ChatList = "archive"
)

func FolderList(id int32) ChatList { return ChatList("folder:" + strconv.Itoa(int(id))) }

func (l ChatList) td() map[string]any {
	switch l {
	case Main:
		return map[string]any{"@type": "chatListMain"}
	case Archive:
		return map[string]any{"@type": "chatListArchive"}
	}
	id, _ := strconv.Atoi(strings.TrimPrefix(string(l), "folder:"))
	return map[string]any{"@type": "chatListFolder", "chat_folder_id": id}
}

type tdList struct {
	Type     string `json:"@type"`
	FolderID int32  `json:"chat_folder_id"`
}

func (l tdList) list() ChatList {
	switch l.Type {
	case "chatListMain":
		return Main
	case "chatListArchive":
		return Archive
	}
	return FolderList(l.FolderID)
}

type tdPosition struct {
	List   tdList `json:"list"`
	Order  int64  `json:"order,string"`
	Pinned bool   `json:"is_pinned"`
}

type ChatKind int

const (
	ChatPrivate ChatKind = iota
	ChatGroup
	ChatChannel // read-only for tgtui (decision D3)
)

type Chat struct {
	ID           int64
	Title        string
	Kind         ChatKind
	Unread       int
	Mentions     int
	MarkedUnread bool
	ReadInbox    int64
	ReadOutbox   int64 // outgoing messages up to here were read
	Muted        bool  // by its own setting or its kind's default
	Last         *Message
	Typing       []Typing // who is typing (or recording, sending…) now, oldest first
	Photo        Photo
	positions    []tdPosition
	notify       tdNotifySettings
	notifyRaw    json.RawMessage // all of TDLib's settings, to change only the mute
}

// Photo is a profile picture: the small (160 px) file, and the tiny inline JPEG to draw
// until it is downloaded. Zero when there is none.
type Photo struct {
	File int32
	Mini []byte
}

type tdChatPhoto struct { // chatPhotoInfo and profilePhoto
	Small tdFile  `json:"small"`
	Mini  *tdMini `json:"minithumbnail"`
}

// photo converts a TDLib photo, recording its file. Called with mu held.
func (c *Core) photo(p *tdChatPhoto) Photo {
	if p == nil || p.Small.ID == 0 {
		return Photo{}
	}
	c.files[p.Small.ID] = fileOf(p.Small)
	ph := Photo{File: p.Small.ID}
	if p.Mini != nil {
		ph.Mini = p.Mini.Data
	}
	return ph
}

// SenderPhoto is the picture of a message sender: a user, or a chat posting as itself.
func (c *Core) SenderPhoto(id int64) Photo {
	c.mu.Lock()
	defer c.mu.Unlock()
	if p, ok := c.userPhotos[id]; ok {
		return p
	}
	if ch := c.chats[id]; ch != nil {
		return ch.Photo
	}
	return Photo{}
}

// Typing is a member's current action in a chat, as words: "typing", "recording a voice note".
type Typing struct {
	SenderID int64
	Name     string
	Action   string
}

// actions are the chat actions tgtui shows; TDLib cancels each after a few seconds.
var actions = map[string]string{
	"chatActionTyping": "typing", "chatActionRecordingVoiceNote": "recording a voice note",
	"chatActionRecordingVideo": "recording a video", "chatActionRecordingVideoNote": "recording a video",
	"chatActionUploadingPhoto": "sending a photo", "chatActionUploadingVideo": "sending a video",
	"chatActionUploadingVoiceNote": "sending a voice note", "chatActionUploadingVideoNote": "sending a video",
	"chatActionUploadingDocument": "sending a file", "chatActionChoosingSticker": "choosing a sticker",
}

// Pinned reports whether the chat is pinned in list l.
func (ch *Chat) Pinned(l ChatList) bool {
	for _, p := range ch.positions {
		if p.List.list() == l {
			return p.Pinned
		}
	}
	return false
}

type tdChat struct {
	ID   int64 `json:"id"`
	Type struct {
		Type      string `json:"@type"`
		IsChannel bool   `json:"is_channel"`
	} `json:"type"`
	Title          string          `json:"title"`
	Photo          *tdChatPhoto    `json:"photo"`
	LastMessage    *tdMessage      `json:"last_message"`
	Positions      []tdPosition    `json:"positions"`
	Unread         int             `json:"unread_count"`
	Mentions       int             `json:"unread_mention_count"`
	MarkedUnread   bool            `json:"is_marked_as_unread"`
	LastReadInbox  int64           `json:"last_read_inbox_message_id"`
	LastReadOutbox int64           `json:"last_read_outbox_message_id"`
	Notify         json.RawMessage `json:"notification_settings"`
}

// applyChat handles the chat list updates. Called with mu held.
func (c *Core) applyChat(u *update) {
	if u.Type == "updateNewChat" && u.Chat != nil {
		t := u.Chat
		ch := &Chat{ID: t.ID, Title: t.Title, Unread: t.Unread, Mentions: t.Mentions, MarkedUnread: t.MarkedUnread,
			ReadInbox: t.LastReadInbox, ReadOutbox: t.LastReadOutbox, positions: t.Positions, notifyRaw: t.Notify, Photo: c.photo(t.Photo)}
		json.Unmarshal(t.Notify, &ch.notify)
		switch {
		case t.Type.Type == "chatTypeBasicGroup", t.Type.Type == "chatTypeSupergroup" && !t.Type.IsChannel:
			ch.Kind = ChatGroup
		case t.Type.Type == "chatTypeSupergroup":
			ch.Kind = ChatChannel
		}
		if t.LastMessage != nil {
			ch.Last = c.message(t.LastMessage)
		}
		ch.Muted = c.muted(ch)
		c.chats[ch.ID] = ch
		return
	}
	old := c.chats[u.ChatID]
	if old == nil {
		return
	}
	ch := *old // copy: the old value may be held by a UI copy
	switch u.Type {
	case "updateChatTitle":
		ch.Title = u.Title
	case "updateChatPhoto":
		ch.Photo = c.photo(u.Photo)
	case "updateChatPosition":
		ch.positions = slices.DeleteFunc(slices.Clone(ch.positions), func(p tdPosition) bool { return p.List == u.Position.List })
		if u.Position.Order != 0 {
			ch.positions = append(ch.positions, *u.Position)
		}
	case "updateChatLastMessage":
		ch.Last = nil
		if u.LastMessage != nil {
			ch.Last = c.message(u.LastMessage)
		}
		ch.positions = u.Positions // these two replace all positions
	case "updateChatDraftMessage":
		ch.positions = u.Positions
	case "updateChatReadInbox":
		ch.ReadInbox, ch.Unread = u.LastReadInbox, u.UnreadCount
	case "updateChatReadOutbox":
		ch.ReadOutbox = u.LastReadOutbox
	case "updateChatUnreadMentionCount":
		ch.Mentions = u.UnreadMentions
	case "updateChatIsMarkedAsUnread":
		ch.MarkedUnread = u.IsMarkedAsUnread
	case "updateChatAction":
		id, name := u.SenderID.UserID, c.users[u.SenderID.UserID]
		if u.SenderID.Type == "messageSenderChat" {
			id, name = u.SenderID.ChatID, ""
			if sc := c.chats[id]; sc != nil {
				name = sc.Title
			}
		}
		ch.Typing = slices.DeleteFunc(slices.Clone(ch.Typing), func(t Typing) bool { return t.SenderID == id })
		if a := actions[u.Action.Type]; a != "" {
			ch.Typing = append(ch.Typing, Typing{id, name, a})
		}
	default:
		return
	}
	c.chats[ch.ID] = &ch
}

// Chats returns the chats of list l in the official apps' order (pinned first). The chats
// are shared copies: don't change them.
func (c *Core) Chats(l ChatList) []*Chat {
	type item struct {
		ch    *Chat
		order int64
	}
	c.mu.Lock()
	var items []item
	for _, ch := range c.chats {
		for _, p := range ch.positions {
			if p.List.list() == l {
				items = append(items, item{ch, p.Order})
				break
			}
		}
	}
	c.mu.Unlock()
	slices.SortFunc(items, func(a, b item) int { return cmp.Or(cmp.Compare(b.order, a.order), cmp.Compare(b.ch.ID, a.ch.ID)) })
	out := make([]*Chat, len(items))
	for i, it := range items {
		out[i] = it.ch
	}
	return out
}

func (c *Core) Chat(id int64) *Chat {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.chats[id]
}

// UnreadChats is how many chats of list l have unread messages or are marked as unread,
// muted ones left out, as on the official apps' folder tabs.
func (c *Core) UnreadChats(l ChatList) int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.unreadIn[l]
}

// LoadList makes TDLib send the chats of l, as updates. Only the first call per list does anything.
func (c *Core) LoadList(l ChatList) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.loadList(l)
}

func (c *Core) loadList(l ChatList) {
	if c.loaded[l] {
		return
	}
	c.loaded[l] = true
	go func() {
		// Each call loads the next chats; the error 404 means all are loaded.
		for {
			if _, err := c.td.Send(map[string]any{"@type": "loadChats", "chat_list": l.td(), "limit": 100}); err != nil {
				return
			}
		}
	}()
}

// MarkRead marks every message of a chat as read, and clears "marked as unread".
func (c *Core) MarkRead(id int64) error {
	ch := c.Chat(id)
	if ch == nil {
		return nil
	}
	if ch.Last != nil {
		if err := c.send(map[string]any{"@type": "viewMessages", "chat_id": id, "message_ids": []int64{ch.Last.ID}, "force_read": true}); err != nil {
			return err
		}
	}
	if ch.MarkedUnread {
		return c.send(map[string]any{"@type": "toggleChatIsMarkedAsUnread", "chat_id": id, "is_marked_as_unread": false})
	}
	return nil
}
