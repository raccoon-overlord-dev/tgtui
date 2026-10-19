package core

import (
	"encoding/json"
	"time"
)

// Notification is a message to tell the user about. TDLib decides which messages get one,
// so Telegram's mute settings, exceptions and mention rules apply exactly as in the
// official apps.
type Notification struct {
	ChatID  int64
	Chat    string // chat title
	Message Message
	Preview bool // the user's settings allow showing the text
	Silent  bool // sent silently: no sound
	Mention bool // a mention or reply in a chat that is otherwise muted
}

type tdNotification struct {
	Date   int64 `json:"date"`
	Silent bool  `json:"is_silent"`
	Type   struct {
		Type    string     `json:"@type"`
		Message *tdMessage `json:"message"`
		Preview bool       `json:"show_preview"`
	} `json:"type"`
}

// enableNotifications turns on TDLib's notification updates, off by default.
func (c *Core) enableNotifications() {
	c.send(map[string]any{"@type": "setOption", "name": "notification_group_count_max",
		"value": map[string]any{"@type": "optionValueInteger", "value": 5}})
}

// applyNotifications queues new message notifications. Called with mu held.
// updateActiveNotifications (the ones still shown from before this start) is ignored.
func (c *Core) applyNotifications(u *update) {
	if u.Type != "updateNotificationGroup" {
		return
	}
	var ns []tdNotification
	json.Unmarshal(u.AddedNotifications, &ns)
	for _, n := range ns {
		// After a long time offline TDLib can still add old messages: a flood helps no one.
		if n.Type.Type != "notificationTypeNewMessage" || n.Type.Message == nil || time.Since(time.Unix(n.Date, 0)) > 5*time.Minute {
			continue
		}
		note := Notification{ChatID: u.ChatID, Message: *c.message(n.Type.Message), Preview: n.Type.Preview,
			Silent: n.Silent, Mention: u.GroupType.Type == "notificationGroupTypeMentions"}
		if ch := c.chats[u.ChatID]; ch != nil {
			note.Chat = ch.Title
		}
		c.notes = append(c.notes, note)
	}
}

// TakeNotifications returns the notifications queued since the last call.
func (c *Core) TakeNotifications() []Notification {
	c.mu.Lock()
	defer c.mu.Unlock()
	n := c.notes
	c.notes = nil
	return n
}

type tdNotifySettings struct {
	UseDefault bool  `json:"use_default_mute_for"`
	MuteFor    int64 `json:"mute_for"`
}

// muted tells whether ch is muted, with its own setting or its kind's default. Called with mu held.
func (c *Core) muted(ch *Chat) bool {
	if ch.notify.UseDefault {
		return c.scopeMute[ch.Kind] > 0
	}
	return ch.notify.MuteFor > 0
}

// applyMute handles the mute settings of chats and of chat kinds. Called with mu held.
func (c *Core) applyMute(u *update) {
	switch u.Type {
	case "updateChatNotificationSettings":
		if old := c.chats[u.ChatID]; old != nil && u.NotificationSettings != nil {
			ch := *old
			ch.notifyRaw, ch.notify = u.NotificationSettings, tdNotifySettings{}
			json.Unmarshal(u.NotificationSettings, &ch.notify)
			ch.Muted = c.muted(&ch)
			c.chats[ch.ID] = &ch
		}
	case "updateScopeNotificationSettings":
		kind, ok := map[string]ChatKind{"notificationSettingsScopePrivateChats": ChatPrivate,
			"notificationSettingsScopeGroupChats": ChatGroup, "notificationSettingsScopeChannelChats": ChatChannel}[u.Scope.Type]
		var st tdNotifySettings
		if !ok || json.Unmarshal(u.NotificationSettings, &st) != nil {
			return
		}
		c.scopeMute[kind] = st.MuteFor
		for id, old := range c.chats { // chats are replaced, never changed: copy those that flip
			if old.Kind == kind && c.muted(old) != old.Muted {
				ch := *old
				ch.Muted = !old.Muted
				c.chats[id] = &ch
			}
		}
	}
}
