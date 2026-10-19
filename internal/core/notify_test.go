package core

import (
	"encoding/json"
	"fmt"
	"testing"
	"time"
)

func TestNotifications(t *testing.T) {
	c := New(&recTD{}, Params{})
	c.Apply(json.RawMessage(`{"@type":"updateNewChat","chat":{"id":3,"title":"Team","type":{"@type":"chatTypeBasicGroup"},
		"notification_settings":{"use_default_mute_for":true,"mute_for":0}}}`))
	now, old := time.Now().Unix(), time.Now().Add(-time.Hour).Unix()
	c.Apply(json.RawMessage(fmt.Sprintf(`{"@type":"updateNotificationGroup","notification_group_id":1,"type":{"@type":"notificationGroupTypeMentions"},
		"chat_id":3,"added_notifications":[
		{"id":1,"date":%d,"is_silent":true,"type":{"@type":"notificationTypeNewMessage","show_preview":true,
			"message":{"id":9,"chat_id":3,"content":{"@type":"messageText","text":{"text":"@jane look"}}}}},
		{"id":2,"date":%d,"type":{"@type":"notificationTypeNewMessage","message":{"id":8,"chat_id":3,"content":{}}}}],
		"removed_notification_ids":[]}`, now, old)))
	ns := c.TakeNotifications()
	if len(ns) != 1 || ns[0].Chat != "Team" || ns[0].Message.Text != "@jane look" || !ns[0].Mention || !ns[0].Silent || !ns[0].Preview {
		t.Fatalf("notifications: %+v", ns)
	}
	if len(c.TakeNotifications()) != 0 {
		t.Fatal("taken twice")
	}

	// Muting groups by default mutes the chat that follows the default, until it gets its own setting.
	c.Apply(json.RawMessage(`{"@type":"updateScopeNotificationSettings","scope":{"@type":"notificationSettingsScopeGroupChats"},"notification_settings":{"mute_for":3600}}`))
	if !c.Chat(3).Muted {
		t.Fatal("group default muted, chat not")
	}
	c.Apply(json.RawMessage(`{"@type":"updateChatNotificationSettings","chat_id":3,"notification_settings":{"use_default_mute_for":false,"mute_for":0}}`))
	if c.Chat(3).Muted {
		t.Fatal("own setting unmutes")
	}
}
