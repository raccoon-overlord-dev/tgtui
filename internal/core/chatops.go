package core

import (
	"encoding/json"

	"github.com/raccoon-overlord-dev/tgtui/internal/config"
)

// SetMuted mutes a chat for good, or unmutes it. Its other notification settings stay.
func (c *Core) SetMuted(id int64, muted bool) error {
	ch := c.Chat(id)
	if ch == nil {
		return nil
	}
	var s map[string]any
	json.Unmarshal(ch.notifyRaw, &s)
	if s == nil {
		s = map[string]any{"@type": "chatNotificationSettings"}
	}
	s["use_default_mute_for"], s["mute_for"] = false, 0
	if muted {
		s["mute_for"] = 1<<31 - 1 // as the official apps' "mute forever"
	}
	return c.send(map[string]any{"@type": "setChatNotificationSettings", "chat_id": id, "notification_settings": s})
}

// SetPinned pins or unpins a chat in list l.
func (c *Core) SetPinned(l ChatList, id int64, pinned bool) error {
	return c.send(map[string]any{"@type": "toggleChatIsPinned", "chat_list": l.td(), "chat_id": id, "is_pinned": pinned})
}

// SetArchived moves a chat to the archive or back to the main list.
func (c *Core) SetArchived(id int64, archived bool) error {
	l := Main
	if archived {
		l = Archive
	}
	return c.send(map[string]any{"@type": "addChatToList", "chat_id": id, "chat_list": l.td()})
}

// In reports whether the chat is in list l.
func (ch *Chat) In(l ChatList) bool {
	for _, p := range ch.positions {
		if p.List.list() == l {
			return true
		}
	}
	return false
}

// setProxy makes TDLib connect through p, or directly when p.Type is "". TDLib keeps
// proxies in its database, so removing one from the config must disable it.
func (c *Core) setProxy(p config.Proxy) {
	var t map[string]any
	switch p.Type {
	case "":
		c.send(map[string]any{"@type": "disableProxy"})
		return
	case "socks5":
		t = map[string]any{"@type": "proxyTypeSocks5", "username": p.Username, "password": p.Password}
	case "http":
		t = map[string]any{"@type": "proxyTypeHttp", "username": p.Username, "password": p.Password}
	case "mtproto":
		t = map[string]any{"@type": "proxyTypeMtproto", "secret": p.Secret}
	default:
		c.locked(func() { c.snap.Err = "proxy: unknown type " + p.Type })
		return
	}
	c.request(map[string]any{"@type": "addProxy", "enable": true, "comment": "tgtui config",
		"proxy": map[string]any{"@type": "proxy", "server": p.Server, "port": p.Port, "type": t}})
}
