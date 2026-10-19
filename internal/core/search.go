package core

import (
	"encoding/json"
	"slices"
	"strings"
)

// Found is a chat or a user from a search.
type Found struct {
	ChatID   int64 // 0: a contact without a chat yet (OpenUser opens one)
	UserID   int64
	Title    string
	Username string
}

func ids(res json.RawMessage, field string) []int64 {
	var r map[string][]int64
	json.Unmarshal(res, &r)
	return r[field]
}

// SearchChats finds chats by title (known ones, then Telegram's), and contacts by name.
func (c *Core) SearchChats(q string) ([]Found, error) {
	res, err := c.td.Send(map[string]any{"@type": "searchChats", "query": q, "limit": 50})
	if err != nil {
		return nil, err
	}
	chats := ids(res, "chat_ids")
	if q != "" {
		if res, err := c.td.Send(map[string]any{"@type": "searchChatsOnServer", "query": q, "limit": 20}); err == nil {
			chats = append(chats, ids(res, "chat_ids")...)
		}
	}
	users, err := c.contacts(q)
	if err != nil {
		return nil, err
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	var out []Found
	for _, id := range chats {
		if ch := c.chats[id]; ch != nil && !slices.ContainsFunc(out, func(f Found) bool { return f.ChatID == id }) {
			out = append(out, Found{ChatID: id, Title: ch.Title})
		}
	}
	for _, u := range users {
		// A private chat's id is its user's id: skip contacts already found as chats.
		if !slices.ContainsFunc(out, func(f Found) bool { return f.ChatID == u }) {
			out = append(out, c.user(u))
		}
	}
	return out, nil
}

// Contacts lists the user's contacts.
func (c *Core) Contacts() ([]Found, error) {
	users, err := c.contacts("")
	c.mu.Lock()
	defer c.mu.Unlock()
	out := make([]Found, len(users))
	for i, u := range users {
		out[i] = c.user(u)
	}
	return out, err
}

func (c *Core) contacts(q string) ([]int64, error) {
	res, err := c.td.Send(map[string]any{"@type": "searchContacts", "query": q, "limit": 500})
	return ids(res, "user_ids"), err
}

// user describes user id. Called with mu held.
func (c *Core) user(id int64) Found {
	f := Found{UserID: id, Title: c.users[id], Username: c.usernames[id]}
	if c.chats[id] != nil {
		f.ChatID = id
	}
	return f
}

// SearchMessages finds messages with q in all chats, or in chat when it isn't 0, newest first.
func (c *Core) SearchMessages(q string, chat int64) ([]Message, error) {
	req := map[string]any{"@type": "searchMessages", "query": q, "offset": "", "limit": 50}
	if chat != 0 {
		req = map[string]any{"@type": "searchChatMessages", "chat_id": chat, "query": q, "limit": 50}
	}
	res, err := c.td.Send(req)
	if err != nil {
		return nil, err
	}
	var r struct {
		Messages []*tdMessage `json:"messages"`
	}
	json.Unmarshal(res, &r)
	c.mu.Lock()
	defer c.mu.Unlock()
	var out []Message
	for _, t := range r.Messages {
		if t != nil {
			out = append(out, *c.message(t))
		}
	}
	return out, nil
}

// Lookup finds someone to start a chat with by phone number (+…) or by @username, which
// can also name a public group or channel.
func (c *Core) Lookup(q string) (Found, error) {
	q = strings.TrimSpace(q)
	if strings.HasPrefix(q, "+") {
		res, err := c.td.Send(map[string]any{"@type": "searchUserByPhoneNumber", "phone_number": strings.ReplaceAll(q, " ", ""), "only_local": false})
		if err != nil {
			return Found{}, err
		}
		u := parseUser(res)
		return Found{UserID: u.ID, Title: strings.TrimSpace(u.FirstName + " " + u.LastName), Username: u.Username}, nil
	}
	name := strings.TrimPrefix(q, "@")
	res, err := c.td.Send(map[string]any{"@type": "searchPublicChat", "username": name})
	if err != nil {
		return Found{}, err
	}
	var ch struct {
		ID    int64  `json:"id"`
		Title string `json:"title"`
	}
	json.Unmarshal(res, &ch)
	return Found{ChatID: ch.ID, Title: ch.Title, Username: name}, nil
}

// OpenUser opens the private chat with a user, creating it if needed.
func (c *Core) OpenUser(id int64) {
	c.send(map[string]any{"@type": "createPrivateChat", "user_id": id})
	c.OpenChat(id)
}

// Members finds members of the open chat for @mentions: by name or username.
func (c *Core) Members(q string) ([]Found, error) {
	id := c.openID()
	ch := c.Chat(id)
	if ch == nil {
		return nil, nil
	}
	var users []int64
	if ch.Kind == ChatPrivate {
		users = []int64{id}
	} else {
		res, err := c.td.Send(map[string]any{"@type": "searchChatMembers", "chat_id": id, "query": q, "limit": 20})
		if err != nil {
			return nil, err
		}
		var r struct {
			Members []struct {
				Member struct {
					UserID int64 `json:"user_id"`
				} `json:"member_id"`
			} `json:"members"`
		}
		json.Unmarshal(res, &r)
		for _, m := range r.Members {
			if m.Member.UserID != 0 {
				users = append(users, m.Member.UserID)
			}
		}
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	var out []Found
	for _, u := range users {
		if c.snap.Me == nil || u != c.snap.Me.ID {
			out = append(out, c.user(u))
		}
	}
	return out, nil
}
