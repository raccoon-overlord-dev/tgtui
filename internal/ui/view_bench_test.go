package ui

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"image"
	"image/color"
	"image/jpeg"
	"os"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/raccoon-overlord-dev/tgtui/internal/config"
	"github.com/raccoon-overlord-dev/tgtui/internal/core"
	"github.com/raccoon-overlord-dev/tgtui/internal/ui/termimg"
)

type nopTD struct{}

func (nopTD) Send(map[string]any) (json.RawMessage, error) { return json.RawMessage(`{}`), nil }

// demo is a signed-in model with 5000 chats and a full message window (fake data).
func demo(w, h int) *model {
	c := core.New(nopTD{}, core.Params{})
	c.Apply(json.RawMessage(`{"@type":"updateAuthorizationState","authorization_state":{"@type":"authorizationStateReady"}}`))
	c.Apply(json.RawMessage(`{"@type":"updateNewChat","chat":{"id":1,"title":"Group 🦊 chat","type":{"@type":"chatTypeBasicGroup"}}}`))
	m := newModel(c, Options{Config: config.Default(), Version: "dev"})
	m.Update(changedMsg{})
	m.w, m.h = w, h
	words := []string{"hello", "👍", "über", "naïve", "日本語", "👨‍👩‍👧", "🇮🇹", "a-very-long-word-without-breaks-that-must-be-hard-wrapped-somewhere", "ok"}
	for i := range 1000 {
		var b strings.Builder
		for j := range i%40 + 1 {
			b.WriteString(words[(i+j)%len(words)] + " ")
		}
		text := b.String()
		m.win.Messages = append(m.win.Messages, core.Message{ID: int64(i + 1), ChatID: 1, SenderID: int64(i / 3 % 4),
			Sender: "Jane", Date: int64(1_790_000_000 + i*200), Kind: "Text", Text: text, Outgoing: i%5 == 0,
			Spans: []core.Span{{From: 0, To: len(text) / 2, Format: core.Bold | core.Link, URL: "https://example.com"},
				{From: len(text) / 3, To: len(text), Format: core.Spoiler | core.Code}}})
	}
	m.win.ChatID, m.win.AtNewest = 1, true
	for i := range 5000 {
		m.chats = append(m.chats, &core.Chat{ID: int64(i + 2), Title: fmt.Sprintf("Chat %d 🎉", i), Unread: i % 7,
			Last: &core.Message{Kind: "Photo", Text: "caption", Date: 1_790_000_000}})
	}
	m.open, m.focus = 1, focusChat
	return m
}

// The screen is exactly h rows of w cells, whatever the text: emoji, ZWJ, flags, CJK.
func TestViewFits(t *testing.T) {
	for _, size := range [][2]int{{160, 50}, {80, 24}, {60, 20}, {30, 10}} {
		m := demo(size[0], size[1])
		m.o.Config.Appearance.Theme = "harvest"
		m.applyConfig()
		check := func(what string) {
			t.Helper()
			lines := strings.Split(m.View().Content, "\n")
			if len(lines) != size[1] {
				t.Fatalf("%v %s: %d lines", size, what, len(lines))
			}
			for i, l := range lines {
				if w := ansi.StringWidth(l); w != size[0] {
					t.Fatalf("%v %s: line %d is %d cells: %q", size, what, i, w, ansi.Strip(l))
				}
			}
		}
		for range 30 {
			m.scroll(7)
			check("chat")
		}
		m.openPicker(pickChats, nil)
		check("picker")
		m.pick = nil
		m.help = true
		check("help")
		m.help, m.settings = false, true
		check("settings")
		m.settings, m.askLogout = false, true
		check("log out")
		m.askLogout, m.focus = false, focusList
		check("list")
		m.snap.Auth.Kind = core.AuthPhone
		check("sign-in")
	}
	setTheme("terminal", false, true)
}

// Scrolling a long chat: one frame per line scrolled, with warm caches.
func BenchmarkScroll(b *testing.B) {
	m := demo(160, 50)
	for b.Loop() {
		m.scroll(1)
		if m.anchorIndex() == 0 {
			m.anchor, m.off = 0, 0
		}
		_ = m.View()
	}
}

// A frame with empty caches, as after a resize.
func BenchmarkColdFrame(b *testing.B) {
	m := demo(160, 50)
	for b.Loop() {
		m.cache = map[lineKey]rendered{}
		_ = m.View()
	}
}

var _ tea.Model = (*model)(nil)

func TestRenderText(t *testing.T) {
	text := "🇮🇹 bold secret and more words here"
	spans := []core.Span{{From: len("🇮🇹 "), To: len("🇮🇹 bold"), Format: core.Bold},
		{From: len("🇮🇹 bold "), To: len("🇮🇹 bold secret"), Format: core.Spoiler}}
	lines := renderText(text, spans, 12, false)
	plain := ""
	for _, l := range lines {
		if w := ansi.StringWidth(l); w > 12 {
			t.Fatalf("line %q is %d cells", ansi.Strip(l), w)
		}
		plain += ansi.Strip(l) + "|"
	}
	if strings.Contains(plain, "secret") || !strings.Contains(plain, "▒▒▒▒▒▒") {
		t.Fatalf("spoiler shown: %s", plain)
	}
	if got := ansi.Strip(strings.Join(renderText(text, spans, 100, true), "")); got != text {
		t.Fatalf("revealed: %q", got)
	}
}

func TestPastedPaths(t *testing.T) {
	dir := t.TempDir()
	a, b := dir+"/a b.jpg", dir+"/c.pdf"
	os.WriteFile(a, nil, 0o600)
	os.WriteFile(b, nil, 0o600)
	for in, want := range map[string]int{
		strings.ReplaceAll(a, " ", `\ `) + " " + b:    2, // shell-escaped, as kitty / Ghostty drop files
		"'" + a + "'\n" + b:                           2,
		"file://" + strings.ReplaceAll(a, " ", "%20"): 1,
		"just some text":                              0,
		a + " nope":                                   0,
	} {
		if got := pastedPaths(in); len(got) != want {
			t.Errorf("%q: %v", in, got)
		}
	}
}

// Images keep the frame exact in both modes.
func TestViewFitsImages(t *testing.T) {
	img := image.NewRGBA(image.Rect(0, 0, 40, 30))
	var buf bytes.Buffer
	jpeg.Encode(&buf, img, nil)
	for _, mode := range []termimg.Mode{termimg.Blocks, termimg.Kitty} {
		m := demo(100, 40)
		m.images = mode
		for i := range m.win.Messages {
			if i%3 == 0 {
				m.win.Messages[i].Kind = "Photo"
				m.win.Messages[i].Media = &core.Media{Mini: buf.Bytes(), W: 40, H: 30}
			}
		}
		for range 20 {
			m.scroll(5)
			m.after()
			for i, l := range strings.Split(m.View().Content, "\n") {
				if w := ansi.StringWidth(l); w != 100 {
					t.Fatalf("%s: line %d is %d cells", mode, i, w)
				}
			}
		}
		if mode == termimg.Kitty && m.nextImg == 0 {
			t.Fatal("kitty: no image sent")
		}
	}
}

func TestMentionInsert(t *testing.T) {
	m := demo(100, 30)
	m.insert = true
	m.comp = newInput("hi @ja")
	m.mention = &mention{start: 3, query: "ja", items: []core.Found{{UserID: 5, Title: "Jane [Doe]"}, {UserID: 6, Title: "Jo", Username: "jo"}}}
	m.composerKey(tea.KeyPressMsg{Code: tea.KeyTab})
	if got := m.comp.value(); got != "hi [Jane Doe](tg://user?id=5) " {
		t.Fatalf("no username: %q", got)
	}
	m.comp = newInput("@j")
	m.mention = &mention{start: 0, query: "j", items: []core.Found{{UserID: 6, Title: "Jo", Username: "jo"}}}
	m.composerKey(tea.KeyPressMsg{Code: tea.KeyEnter})
	if got := m.comp.value(); got != "@jo " {
		t.Fatalf("username: %q", got)
	}
}

func TestNotifyRouting(t *testing.T) {
	m := demo(100, 30)
	m.notifyTo = "terminal"
	n := core.Notification{ChatID: 1, Chat: "Group 🦊 chat", Preview: true, Message: core.Message{Kind: "Text", Text: "hi\x1b]evil", Sender: "Jo"}}
	if m.notify(n) != nil || m.flash != "" {
		t.Fatal("the open chat on screen must not notify")
	}
	n.ChatID = 99
	m.focused = focusHere
	m.notify(n)
	if m.flash == "" {
		t.Fatal("focused: want the status bar")
	}
	m.flash, m.focused = "", focusAway
	if m.notify(n) == nil || m.flash != "" {
		t.Fatal("away: want a terminal notification")
	}
	if s := oscNotify("a", "hi\x1b]evil"); strings.Count(s, "\x1b") != strings.Count(oscNotify("a", "b"), "\x1b") {
		t.Fatalf("escape not stripped: %q", s)
	}
}

// Profile pictures as kitty placeholders and as initials keep every frame w × h, and
// the kitty images are sent before the frame that shows them.
func TestAvatarsFit(t *testing.T) {
	var png bytes.Buffer
	img := image.NewRGBA(image.Rect(0, 0, 8, 8))
	img.Set(2, 2, color.RGBA{200, 80, 20, 255})
	jpeg.Encode(&png, img, nil)
	for _, kitty := range []bool{true, false} {
		m := demo(120, 40)
		m.c.Apply(json.RawMessage(`{"@type":"updateNewChat","chat":{"id":1,"title":"Group 🦊 chat","type":{"@type":"chatTypeBasicGroup"},"photo":{"small":{"id":77},"minithumbnail":{"data":"` +
			base64.StdEncoding.EncodeToString(png.Bytes()) + `"}}}}`))
		m.chats[0] = m.c.Chat(1)
		if kitty {
			m.images = termimg.Kitty
		}
		m.after()
		if kitty && !strings.Contains(fmt.Sprint(m.avatars), "\U0010EEEE") {
			t.Fatal("no kitty placeholders drawn")
		}
		for _, f := range []focus{focusChat, focusList} {
			m.focus = f
			for i, l := range strings.Split(m.View().Content, "\n") {
				if w := ansi.StringWidth(l); w != 120 {
					t.Fatalf("kitty %v: line %d is %d cells: %q", kitty, i, w, ansi.Strip(l))
				}
			}
		}
	}
}

// Pictures that come into view as the list scrolls are sent before the frame shows
// them: View must find nothing left to transmit.
func TestAvatarsSentBeforeFrame(t *testing.T) {
	var jpg bytes.Buffer
	jpeg.Encode(&jpg, image.NewRGBA(image.Rect(0, 0, 8, 8)), nil)
	m := demo(120, 40)
	m.images = termimg.Kitty
	for i, ch := range m.chats[:40] {
		c := *ch
		c.Photo = core.Photo{Mini: jpg.Bytes()}
		m.chats[i] = &c
	}
	m.focus = focusList
	for range 30 {
		m.Update(keyMsg("j"))
		m.View()
		if len(m.sendQ) > 0 {
			t.Fatalf("row %d: %d transmissions queued while drawing, sent only on the next update", m.sel, len(m.sendQ))
		}
	}
}
