package core

import (
	"encoding/json"
	"strconv"
	"strings"
)

// markdown parses the official apps' markup (**bold**, __italic__, ~~strike~~, ||spoiler||,
// `code`, ```pre```, [text](url)) into a TDLib formattedText (decision O2).
func (c *Core) markdown(text string) (map[string]any, error) {
	res, err := c.td.Send(map[string]any{"@type": "parseMarkdown",
		"text": map[string]any{"@type": "formattedText", "text": text, "entities": []any{}}})
	if err != nil {
		return nil, err
	}
	var ft struct {
		Text     string           `json:"text"`
		Entities []map[string]any `json:"entities"`
	}
	json.Unmarshal(res, &ft)
	for _, e := range ft.Entities { // [Name](tg://user?id=N): a mention of a user without a username
		t, _ := e["type"].(map[string]any)
		if url, _ := t["url"].(string); strings.HasPrefix(url, mentionURL) {
			if id, err := strconv.ParseInt(strings.TrimPrefix(url, mentionURL), 10, 64); err == nil {
				e["type"] = map[string]any{"@type": "textEntityTypeMentionName", "user_id": id}
			}
		}
	}
	if ft.Entities == nil {
		ft.Entities = []map[string]any{}
	}
	return map[string]any{"@type": "formattedText", "text": ft.Text, "entities": ft.Entities}, nil
}

const mentionURL = "tg://user?id="

func (c *Core) openID() int64 {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.win.ChatID
}

// SendText sends a message with markup to the open chat, as a reply when replyTo isn't 0.
func (c *Core) SendText(text string, replyTo int64) error {
	ft, err := c.markdown(text)
	if err != nil {
		return err
	}
	req := map[string]any{"@type": "sendMessage", "chat_id": c.openID(),
		"input_message_content": map[string]any{"@type": "inputMessageText", "text": ft}}
	if replyTo != 0 {
		req["reply_to"] = map[string]any{"@type": "inputMessageReplyToMessage", "message_id": replyTo}
	}
	return c.send(req)
}

// SendTyping tells the open chat's members that the user is typing. It lasts a few
// seconds; sending a message ends it.
func (c *Core) SendTyping() error {
	return c.send(map[string]any{"@type": "sendChatAction", "chat_id": c.openID(),
		"action": map[string]any{"@type": "chatActionTyping"}})
}

// Markdown is m's text with its formatting written back as markup, to edit it. Mentions of
// users without a username become [Name](tg://user?id=N); underline, which markup can't
// express, is lost.
func (c *Core) Markdown(m Message) (string, error) {
	var ents []map[string]any
	json.Unmarshal(m.entities, &ents)
	for _, e := range ents {
		if t, _ := e["type"].(map[string]any); t["@type"] == "textEntityTypeMentionName" {
			id, _ := t["user_id"].(float64) // a JSON number; user ids fit in 53 bits
			e["type"] = map[string]any{"@type": "textEntityTypeTextUrl", "url": mentionURL + strconv.FormatInt(int64(id), 10)}
		}
	}
	if ents == nil {
		ents = []map[string]any{}
	}
	res, err := c.td.Send(map[string]any{"@type": "getMarkdownText",
		"text": map[string]any{"@type": "formattedText", "text": m.Text, "entities": ents}})
	if err != nil {
		return "", err
	}
	var ft formattedText
	json.Unmarshal(res, &ft)
	return ft.Text, nil
}

// EditText replaces the text of message id of the open chat.
func (c *Core) EditText(id int64, text string) error {
	ft, err := c.markdown(text)
	if err != nil {
		return err
	}
	return c.send(map[string]any{"@type": "editMessageText", "chat_id": c.openID(), "message_id": id,
		"input_message_content": map[string]any{"@type": "inputMessageText", "text": ft}})
}

// Props is what can be done with a message.
type Props struct {
	CanEdit, CanForward, CanReply bool
	DeleteForAll, DeleteForSelf   bool
}

func (c *Core) Properties(id int64) (Props, error) {
	res, err := c.td.Send(map[string]any{"@type": "getMessageProperties", "chat_id": c.openID(), "message_id": id})
	var p struct {
		Edit    bool `json:"can_be_edited"`
		Forward bool `json:"can_be_forwarded"`
		Reply   bool `json:"can_be_replied"`
		All     bool `json:"can_be_deleted_for_all_users"`
		Self    bool `json:"can_be_deleted_only_for_self"`
	}
	json.Unmarshal(res, &p)
	return Props{p.Edit, p.Forward, p.Reply, p.All, p.Self}, err
}

// Delete deletes messages of the open chat, for everyone when forAll.
func (c *Core) Delete(ids []int64, forAll bool) error {
	return c.send(map[string]any{"@type": "deleteMessages", "chat_id": c.openID(), "message_ids": ids, "revoke": forAll})
}

// Forward forwards messages of the open chat to chat to.
func (c *Core) Forward(to int64, ids []int64) error {
	return c.send(map[string]any{"@type": "forwardMessages", "chat_id": to, "from_chat_id": c.openID(), "message_ids": ids})
}
