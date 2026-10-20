package core

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"strings"
	"testing"
)

// historyTD serves getChatHistory from a chat of n messages with ids 1..n, answering the
// first request with a few messages only, as TDLib does from its cache.
type historyTD struct {
	n     int64
	calls int
}

func (h *historyTD) Send(req map[string]any) (json.RawMessage, error) {
	if req["@type"] != "getChatHistory" {
		return json.RawMessage(`{"@type":"ok"}`), nil
	}
	h.calls++
	from, offset, limit := req["from_message_id"].(int64), req["offset"].(int), req["limit"].(int)
	if h.calls == 1 {
		limit = 3
	}
	top := h.n // newest id included in the answer: from itself, plus -offset newer
	if from != 0 {
		top = min(from-int64(offset), h.n)
	}
	var ms []string
	for id := top; id >= 1 && len(ms) < limit; id-- {
		ms = append(ms, fmt.Sprintf(`{"id":%d,"chat_id":7,"sender_id":{"@type":"messageSenderUser","user_id":1},"date":%d,"content":{"@type":"messageText","text":{"text":"m%d"}}}`, id, id, id))
	}
	return json.RawMessage(fmt.Sprintf(`{"@type":"messages","messages":[%s]}`, join(ms))), nil
}

func join(s []string) string {
	out := ""
	for i, x := range s {
		if i > 0 {
			out += ","
		}
		out += x
	}
	return out
}

func checkWindow(t *testing.T, w Window) {
	t.Helper()
	if len(w.Messages) > windowCap {
		t.Fatalf("window holds %d messages, cap %d", len(w.Messages), windowCap)
	}
	for i := 1; i < len(w.Messages); i++ {
		if w.Messages[i].ID != w.Messages[i-1].ID+1 {
			t.Fatalf("gap or disorder at %d: %d after %d", i, w.Messages[i].ID, w.Messages[i-1].ID)
		}
	}
}

// Scrolling a 100k-message chat to its start and back keeps the window contiguous and capped.
func TestWindowPaging(t *testing.T) {
	const n = 100_000
	c := New(&historyTD{n: n}, Params{})
	c.chats[7] = &Chat{ID: 7, Last: &Message{ID: n}}

	c.OpenChat(7)
	w := c.Window()
	if len(w.Messages) != pageSize || w.Messages[len(w.Messages)-1].ID != n || !w.AtNewest || w.AtOldest {
		t.Fatalf("open: %d messages, newest %d, newest? %v, oldest? %v", len(w.Messages), w.Messages[len(w.Messages)-1].ID, w.AtNewest, w.AtOldest)
	}
	for !c.Window().AtOldest {
		c.LoadOlder()
		checkWindow(t, c.Window())
	}
	if w := c.Window(); w.Messages[0].ID != 1 || w.AtNewest {
		t.Fatalf("top: oldest %d, newest? %v", w.Messages[0].ID, w.AtNewest)
	}
	for !c.Window().AtNewest {
		c.LoadNewer()
		checkWindow(t, c.Window())
	}
	if w := c.Window(); w.Messages[len(w.Messages)-1].ID != n {
		t.Fatalf("bottom: newest %d", w.Messages[len(w.Messages)-1].ID)
	}

	// A live message is appended when the newest end is loaded, and an edit replaces content.
	c.Apply(json.RawMessage(fmt.Sprintf(`{"@type":"updateNewMessage","message":{"id":%d,"chat_id":7,"content":{"@type":"messageSticker","sticker":{"emoji":"👍"}}}}`, n+1)))
	c.Apply(json.RawMessage(fmt.Sprintf(`{"@type":"updateMessageContent","chat_id":7,"message_id":%d,"new_content":{"@type":"messageText","text":{"text":"edited"}}}`, n)))
	w = c.Window()
	last, prev := w.Messages[len(w.Messages)-1], w.Messages[len(w.Messages)-2]
	if last.ID != n+1 || last.Kind != "Sticker" || last.Detail != "👍" || prev.Text != "edited" {
		t.Fatalf("live: %+v / %+v", last, prev)
	}
}

func TestChatOrder(t *testing.T) {
	c := New(&historyTD{}, Params{})
	for _, s := range []string{
		`{"@type":"updateNewChat","chat":{"id":1,"title":"Old","type":{"@type":"chatTypePrivate"},"positions":[]}}`,
		`{"@type":"updateNewChat","chat":{"id":2,"title":"New","type":{"@type":"chatTypeSupergroup","is_channel":true},"positions":[]}}`,
		`{"@type":"updateNewChat","chat":{"id":3,"title":"Pinned","type":{"@type":"chatTypeBasicGroup"},"positions":[]}}`,
		// orders are int64 sent as strings, beyond float64 precision
		`{"@type":"updateChatPosition","chat_id":1,"position":{"list":{"@type":"chatListMain"},"order":"9007199254740993"}}`,
		`{"@type":"updateChatPosition","chat_id":2,"position":{"list":{"@type":"chatListMain"},"order":"9007199254740994"}}`,
		`{"@type":"updateChatPosition","chat_id":3,"position":{"list":{"@type":"chatListMain"},"order":"9223372036854775807","is_pinned":true}}`,
		`{"@type":"updateChatPosition","chat_id":2,"position":{"list":{"@type":"chatListFolder","chat_folder_id":5},"order":"10"}}`,
	} {
		c.Apply(json.RawMessage(s))
	}
	got := ""
	for _, ch := range c.Chats(Main) {
		got += ch.Title + " "
	}
	if got != "Pinned New Old " {
		t.Fatalf("main order: %q", got)
	}
	if f := c.Chats(FolderList(5)); len(f) != 1 || f[0].Kind != ChatChannel || !c.Chats(Main)[0].Pinned(Main) {
		t.Fatalf("folder: %v", f)
	}

	// The last message update replaces every position: here it leaves the folder.
	c.Apply(json.RawMessage(`{"@type":"updateChatLastMessage","chat_id":2,"last_message":{"id":9,"chat_id":2,"content":{"@type":"messagePhoto","caption":{"text":"hi"}}},
		"positions":[{"list":{"@type":"chatListMain"},"order":"9007199254740994"}]}`))
	if len(c.Chats(FolderList(5))) != 0 || c.Chat(2).Last.Text != "hi" || c.Chat(2).Last.Kind != "Photo" {
		t.Fatalf("after last message: %+v", c.Chat(2).Last)
	}
	c.Apply(json.RawMessage(`{"@type":"updateChatPosition","chat_id":1,"position":{"list":{"@type":"chatListMain"},"order":"0"}}`))
	if len(c.Chats(Main)) != 2 {
		t.Fatal("order 0 must remove the chat from the list")
	}
}

// Entity offsets count UTF-16 units: an emoji before the bold word takes two.
func TestSpans(t *testing.T) {
	text := "👍 bold ünd link"
	sp := spans(text, json.RawMessage(`[{"offset":3,"length":4,"type":{"@type":"textEntityTypeBold"}},
		{"offset":12,"length":4,"type":{"@type":"textEntityTypeTextUrl","url":"https://example.com"}},
		{"offset":0,"length":2,"type":{"@type":"textEntityTypeCustomEmoji"}}]`))
	if len(sp) != 2 || text[sp[0].From:sp[0].To] != "bold" || text[sp[1].From:sp[1].To] != "link" || sp[1].URL != "https://example.com" {
		t.Fatalf("spans: %+v", sp)
	}
}

// recTD answers parseMarkdown as TDLib would for **b**, and records every request.
type recTD struct{ reqs []map[string]any }

func (r *recTD) Send(req map[string]any) (json.RawMessage, error) {
	r.reqs = append(r.reqs, req)
	if req["@type"] == "parseMarkdown" {
		return json.RawMessage(`{"@type":"formattedText","text":"b","entities":[{"@type":"textEntity","offset":0,"length":1,"type":{"@type":"textEntityTypeBold"}}],"@extra":3}`), nil
	}
	return json.RawMessage(`{"@type":"ok"}`), nil
}

func TestSendAndReplies(t *testing.T) {
	td := &recTD{}
	c := New(td, Params{})
	c.win.ChatID, c.win.AtNewest = 7, true
	if err := c.SendText("**b**", 5); err != nil {
		t.Fatal(err)
	}
	b, _ := json.Marshal(td.reqs[len(td.reqs)-1])
	if s := string(b); !strings.Contains(s, `"reply_to":{"@type":"inputMessageReplyToMessage","message_id":5}`) ||
		!strings.Contains(s, `textEntityTypeBold`) || strings.Contains(s, "@extra") {
		t.Fatalf("sendMessage: %s", s)
	}

	// A reply to a message in the window resolves from it; a quote is kept.
	c.Apply(json.RawMessage(`{"@type":"updateNewMessage","message":{"id":1,"chat_id":7,"content":{"@type":"messageText","text":{"text":"first"}}}}`))
	c.Apply(json.RawMessage(`{"@type":"updateNewMessage","message":{"id":2,"chat_id":7,"content":{"@type":"messageText","text":{"text":"re"}},
		"reply_to":{"@type":"messageReplyToMessage","chat_id":7,"message_id":1,"quote":{"text":{"text":"fir"}}}}}`))
	w := c.Window()
	if r := c.Replied(w.Messages[1]); r == nil || r.Text != "first" || w.Messages[1].Quote != "fir" {
		t.Fatalf("replied: %+v", r)
	}
}

func TestMedia(t *testing.T) {
	c := New(&recTD{}, Params{})
	c.win.ChatID, c.win.AtNewest = 7, true
	c.Apply(json.RawMessage(`{"@type":"updateNewMessage","message":{"id":1,"chat_id":7,"content":{"@type":"messagePhoto",
		"caption":{"text":"cat"},"photo":{"minithumbnail":{"width":40,"height":30,"data":"AQID"},"sizes":[
		{"type":"s","width":90,"height":67,"photo":{"id":10,"size":1000,"local":{}}},
		{"type":"m","width":320,"height":240,"photo":{"id":11,"size":9000,"local":{}}},
		{"type":"y","width":1280,"height":960,"photo":{"id":12,"size":90000,"local":{}}}]}}}}`))
	md := c.Window().Messages[0].Media
	if md == nil || md.File != 12 || md.Thumb != 11 || md.W != 320 || string(md.Mini) != "\x01\x02\x03" || c.Window().Messages[0].Text != "cat" {
		t.Fatalf("photo: %+v", md)
	}
	c.Apply(json.RawMessage(`{"@type":"updateFile","file":{"id":11,"size":9000,"local":{"path":"/tmp/x.jpg","is_downloading_completed":true,"downloaded_size":9000}}}`))
	if f := c.File(11); !f.Done || f.Path != "/tmp/x.jpg" {
		t.Fatalf("file: %+v", f)
	}
}

// Photos go as one album with the caption on the first; other files in their own album.
func TestSendFiles(t *testing.T) {
	td := &recTD{}
	c := New(td, Params{})
	c.win.ChatID = 7
	if err := c.SendFiles([]string{"/a.jpg", "/b.pdf", "/c.png", "/d.mp4"}, "**b**", 0); err != nil {
		t.Fatal(err)
	}
	var sends []string
	for _, r := range td.reqs {
		if r["@type"] != "parseMarkdown" {
			b, _ := json.Marshal(r)
			sends = append(sends, string(b))
		}
	}
	if len(sends) != 2 || !strings.Contains(sends[0], `"sendMessageAlbum"`) || strings.Count(sends[0], "inputMessagePhoto") != 2 ||
		strings.Count(sends[0], "inputMessageVideo") != 1 || strings.Count(sends[0], `"caption"`) != 1 ||
		!strings.Contains(sends[1], `"sendMessage"`) || !strings.Contains(sends[1], `"inputDocument"`) || strings.Contains(sends[1], "caption") {
		t.Fatalf("requests:\n%s", strings.Join(sends, "\n"))
	}
}

// Mentions of users without a username go through markup as [Name](tg://user?id=N).
func TestMentionMarkup(t *testing.T) {
	td := &markTD{}
	c := New(td, Params{})
	m := Message{Text: "hi Jane", entities: json.RawMessage(`[{"@type":"textEntity","offset":3,"length":4,"type":{"@type":"textEntityTypeMentionName","user_id":1234567890}}]`)}
	c.Markdown(m)
	if !strings.Contains(td.last, `"url":"tg://user?id=1234567890"`) {
		t.Fatalf("getMarkdownText got %s", td.last)
	}
	ft, _ := c.markdown("hi [Jane](tg://user?id=1234567890)")
	if b, _ := json.Marshal(ft); !strings.Contains(string(b), `{"@type":"textEntityTypeMentionName","user_id":1234567890}`) {
		t.Fatalf("parsed: %s", b)
	}
}

// markTD answers parseMarkdown as TDLib does for a tg://user link, and records the last request.
type markTD struct{ last string }

func (m *markTD) Send(req map[string]any) (json.RawMessage, error) {
	b, _ := json.Marshal(req)
	m.last = string(b)
	return json.RawMessage(`{"@type":"formattedText","text":"hi Jane","entities":[{"@type":"textEntity","offset":3,"length":4,"type":{"@type":"textEntityTypeTextUrl","url":"tg://user?id=1234567890"}}]}`), nil
}

type funcTD func(req map[string]any) (json.RawMessage, error)

func (f funcTD) Send(req map[string]any) (json.RawMessage, error) { return f(req) }

// A new chat starts from a phone number or a @username.
func TestLookup(t *testing.T) {
	c := New(funcTD(func(req map[string]any) (json.RawMessage, error) {
		switch req["@type"] {
		case "searchUserByPhoneNumber":
			if req["phone_number"] != "+15550100" {
				t.Errorf("phone %v", req["phone_number"])
			}
			return json.RawMessage(`{"@type":"user","id":5,"first_name":"Ann","last_name":"Lee","usernames":{"editable_username":"ann"}}`), nil
		case "searchPublicChat":
			return json.RawMessage(`{"@type":"chat","id":-100,"title":"Go Weekly"}`), nil
		}
		return json.RawMessage(`{"@type":"ok"}`), nil
	}), Params{})
	if f, err := c.Lookup("+1 555 0100"); err != nil || f.UserID != 5 || f.Title != "Ann Lee" || f.Username != "ann" {
		t.Fatalf("phone: %+v %v", f, err)
	}
	if f, err := c.Lookup("@golangweekly"); err != nil || f.ChatID != -100 || f.Username != "golangweekly" {
		t.Fatalf("username: %+v %v", f, err)
	}
}

// Chat actions: one entry per sender, replaced by its next action, dropped on cancel.
func TestTyping(t *testing.T) {
	c := New(&historyTD{}, Params{})
	act := func(user int, action string) {
		c.Apply(json.RawMessage(fmt.Sprintf(`{"@type":"updateChatAction","chat_id":3,"sender_id":{"@type":"messageSenderUser","user_id":%d},"action":{"@type":"%s"}}`, user, action)))
	}
	c.Apply(json.RawMessage(`{"@type":"updateNewChat","chat":{"id":3,"title":"G","type":{"@type":"chatTypeBasicGroup"},"positions":[]}}`))
	c.Apply(json.RawMessage(`{"@type":"updateUser","user":{"id":7,"first_name":"Jane","usernames":{}}}`))
	act(7, "chatActionTyping")
	act(8, "chatActionTyping")
	act(7, "chatActionRecordingVoiceNote")
	if got := c.Chat(3).Typing; len(got) != 2 || got[0].SenderID != 8 || got[1] != (Typing{7, "Jane", "recording a voice note"}) {
		t.Fatalf("typing: %+v", got)
	}
	act(8, "chatActionCancel")
	act(7, "chatActionCancel")
	if got := c.Chat(3).Typing; len(got) != 0 {
		t.Fatalf("after cancel: %+v", got)
	}
}

// Waveforms go through Telegram's 5-bit packing unchanged, and TDLib's base64 bytes decode.
func TestWaveform(t *testing.T) {
	vs := []byte{0, 31, 7, 16, 1, 30, 9, 22, 3}
	if got := Unpack5(Pack5(vs))[:len(vs)]; string(got) != string(vs) {
		t.Fatalf("round trip: %v", got)
	}
	c := New(&historyTD{}, Params{})
	var m Message
	c.setMedia(&m, json.RawMessage(`{"@type":"messageVoiceNote","voice_note":{"duration":3,"waveform":"`+
		base64.StdEncoding.EncodeToString(Pack5(vs))+`","voice":{"id":5}}}`))
	if m.Media == nil || m.Media.Duration != 3 || string(m.Media.Wave[:len(vs)]) != string(vs) {
		t.Fatalf("voice note: %+v", m.Media)
	}
}

// Profile pictures: chats carry theirs, users' come from updateUser, and both change.
func TestPhotos(t *testing.T) {
	c := New(&historyTD{}, Params{})
	c.Apply(json.RawMessage(`{"@type":"updateNewChat","chat":{"id":-5,"title":"G","type":{"@type":"chatTypeBasicGroup"},"photo":{"small":{"id":11},"minithumbnail":{"data":"AAEC"}},"positions":[]}}`))
	c.Apply(json.RawMessage(`{"@type":"updateUser","user":{"id":7,"first_name":"Jane","usernames":{},"profile_photo":{"small":{"id":12}}}}`))
	if p := c.Chat(-5).Photo; p.File != 11 || len(p.Mini) != 3 {
		t.Fatalf("chat photo: %+v", p)
	}
	if c.SenderPhoto(7).File != 12 || c.SenderPhoto(-5).File != 11 || c.SenderPhoto(8).File != 0 {
		t.Fatal("sender photos")
	}
	c.Apply(json.RawMessage(`{"@type":"updateChatPhoto","chat_id":-5}`))
	if c.Chat(-5).Photo.File != 0 {
		t.Fatal("removed photo")
	}
}
