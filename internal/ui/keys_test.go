package ui

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"

	"github.com/raccoon-overlord-dev/tgtui/internal/config"
	"github.com/raccoon-overlord-dev/tgtui/internal/core"
)

// press sends keys as tea reports them: "x" (with text), "ctrl+x", "alt+x", "enter"…
func press(m *model, keys ...string) tea.Cmd {
	var cmd tea.Cmd
	for _, k := range keys {
		cmd = m.key(keyMsg(k))
	}
	return cmd
}

func keyMsg(k string) tea.KeyPressMsg {
	named := map[string]rune{"enter": tea.KeyEnter, "esc": tea.KeyEscape, "tab": tea.KeyTab, "up": tea.KeyUp,
		"down": tea.KeyDown, "left": tea.KeyLeft, "right": tea.KeyRight, "f1": tea.KeyF1, "f2": tea.KeyF2,
		"pgup": tea.KeyPgUp, "space": tea.KeySpace}
	var mod tea.KeyMod
	for {
		switch {
		case len(k) > 5 && k[:5] == "ctrl+":
			mod, k = mod|tea.ModCtrl, k[5:]
			continue
		case len(k) > 4 && k[:4] == "alt+":
			mod, k = mod|tea.ModAlt, k[4:]
			continue
		}
		break
	}
	if r, ok := named[k]; ok {
		return tea.KeyPressMsg{Code: r, Mod: mod}
	}
	r := []rune(k)[0]
	msg := tea.KeyPressMsg{Code: r, Mod: mod}
	if mod == 0 {
		msg.Text = k
	}
	return msg
}

// legacyModel has two folders, a chat open with messages, one of them own.
func legacyModel(t *testing.T, keymap string) *model {
	m := demo(120, 40)
	m.o.Config.Keys.Keymap = keymap
	m.snap.Lists = []core.List{{ID: core.Main}, {ID: core.FolderList(1), Name: "Work"}}
	m.snap.Me = &core.Me{ID: 42}
	m.win.Messages[len(m.win.Messages)-1].Outgoing = true
	m.insert = true
	return m
}

// Every action of ANALYSIS.md §5 with keys a legacy terminal (no kitty keyboard protocol)
// delivers: no ctrl+digit, ctrl+shift, shift+enter, ctrl+i or ctrl+, anywhere here.
func TestLegacyKeys(t *testing.T) {
	for _, keymap := range []string{"vim+telegram", "telegram"} {
		vim := keymap != "telegram"
		check := func(what string, ok bool) {
			t.Helper()
			if !ok {
				t.Errorf("%s: %s", keymap, what)
			}
		}
		m := legacyModel(t, keymap)

		press(m, "f1")
		check("help", m.help)
		press(m, "esc")
		press(m, "f2")
		check("settings", m.settings)
		press(m, "esc")

		press(m, "ctrl+\\")
		check("chat menu", m.menu != nil)
		press(m, "esc")

		press(m, "ctrl+f")
		check("search in chat", m.pick != nil && m.pick.kind == pickInChat)
		press(m, "esc")

		check("attach", press(m, "ctrl+o") == nil && m.pathIn != nil)
		press(m, "esc")

		m.comp = newInput("a")
		press(m, "alt+enter")
		check("new line", m.comp.value() == "a\n")
		m.comp = newInput("")

		press(m, "ctrl+up")
		check("reply to previous", m.replyTo == m.win.Messages[len(m.win.Messages)-1].ID)
		press(m, "esc")

		// Actions on a selected message, while typing (both keymaps).
		press(m, "ctrl+up")
		check("edit selected", press(m, "alt+e") != nil)
		press(m, "ctrl+up")
		check("delete selected", press(m, "alt+d") != nil)
		press(m, "ctrl+up")
		press(m, "alt+f")
		check("forward selected", m.pick != nil && m.pick.kind == pickForward)
		press(m, "esc")

		// Back to the chat list, and folders, saved messages, archive, search from there.
		m.insert, m.replyTo, m.cur = true, 0, 0
		press(m, "esc")
		if vim {
			press(m, "esc") // normal mode, then the list
		}
		check("back to the list", m.focus == focusList)
		if vim {
			press(m, "2")
		} else {
			press(m, "right")
		}
		check("next folder", m.tab == 1)
		if vim {
			press(m, "g", "a")
		} else {
			press(m, "right")
		}
		check("archive", m.tabs()[m.tab].ID == core.Archive)
		m.tab = 0
		if vim {
			press(m, "/")
		} else {
			press(m, "s")
		}
		check("find chats", m.pick != nil && m.pick.kind == pickChats)
		press(m, "esc")
		press(m, "ctrl+n")
		check("new chat", m.pick != nil && m.pick.kind == pickNew)
		press(m, "esc")
		if vim {
			press(m, "g", "s")
			check("saved messages", m.open == 42)
		}
		check("mark read", press(m, "ctrl+r") != nil)
		check("quit", press(m, "ctrl+q") != nil)
	}
}

// A setting applies at once and lands in the config file.
func TestSettingsApplyAndSave(t *testing.T) {
	config.Path = filepath.Join(t.TempDir(), "config.toml")
	m := demo(120, 50)
	press(m, "f2")
	for settingRows[m.setSel].label != "Time format" {
		press(m, "down")
	}
	press(m, "right")
	if m.o.Config.Appearance.TimeFormat != "12h" || m.clock != "3:04 PM" {
		t.Fatalf("not applied: %q %q", m.o.Config.Appearance.TimeFormat, m.clock)
	}
	b, err := os.ReadFile(config.Path)
	if err != nil || !strings.Contains(string(b), `time_format = "12h"`) {
		t.Fatalf("not saved: %v\n%s", err, b)
	}
	for _, mode := range []string{"terminal", "system"} {
		m.notifyTo = mode
		if m.testNotify() == nil {
			t.Fatalf("test notification (%s) sent nothing", mode)
		}
	}
	m.notifyTo = "off"
	if m.testNotify() != nil || m.flash != "Notifications are off" {
		t.Fatal("off must not notify")
	}
}

// The Italian layout uses the keys in the US punctuation's places; typing keeps them.
func TestItalianLayout(t *testing.T) {
	m := legacyModel(t, "vim+telegram")
	m.o.Config.Keys.Layout = "it"
	m.comp = newInput("")
	press(m, "+", "-")
	if m.comp.value() != "+-" {
		t.Fatalf("typing: %q", m.comp.value())
	}
	m.insert, m.focus = false, focusList
	press(m, "+")
	if m.tab != 1 {
		t.Fatal("+ must be the next folder")
	}
	press(m, "-")
	if m.pick == nil || m.pick.kind != pickChats {
		t.Fatal("- must find chats")
	}
	press(m, "esc")
	m.focus = focusChat // the open chat, which the fake core knows
	press(m, "f3")
	if m.menu == nil {
		t.Fatal("f3 must open the chat menu")
	}
}

// : finds chats; after > it finds settings and commands, and picking one goes there.
func TestPalette(t *testing.T) {
	m := legacyModel(t, "vim+telegram")
	m.insert, m.focus = false, focusList
	press(m, ":")
	if m.pick == nil || m.pick.kind != pickPalette {
		t.Fatal(": must open the palette")
	}
	press(m, ">", "t", "h", "e", "m")
	if len(m.pick.items) == 0 || m.pick.items[0].title != "Theme" {
		t.Fatalf("> them: %+v", m.pick.items)
	}
	press(m, "enter")
	if !m.settings || settingRows[m.setSel].label != "Theme" {
		t.Fatal("picking a setting must open it in Settings")
	}
	press(m, "esc", ":", ">", "n", "e", "w")
	if it := m.pick.items; len(it) != 1 || it[0].cmd != "new" {
		t.Fatalf("> new: %+v", it)
	}
	press(m, "enter")
	if m.pick == nil || m.pick.kind != pickNew {
		t.Fatal(":new must open the new chat picker")
	}
	m = legacyModel(t, "telegram") // no :, typing searches: > still leads to the settings
	m.insert, m.focus = false, focusList
	press(m, ">", "t", "h", "e", "m", "enter")
	if !m.settings || settingRows[m.setSel].label != "Theme" {
		t.Fatal("telegram keymap: > must find settings")
	}
	if c, ok := commandNamed("quit"); !ok || c.cmd != "q" {
		t.Fatal("quit: no command")
	}
}

// As in Telegram, esc pressed enough times leaves the chat list with no chat open.
func TestEscClosesChat(t *testing.T) {
	for keymap, n := range map[string]int{"vim+telegram": 3, "telegram": 2} {
		m := legacyModel(t, keymap)
		press(m, slices.Repeat([]string{"esc"}, n-1)...)
		if m.open == 0 || m.focus != focusList {
			t.Fatalf("%s: esc ×%d should only leave the chat (open %d)", keymap, n-1, m.open)
		}
		press(m, "esc")
		if m.open != 0 || m.prevOpen == 0 {
			t.Fatalf("%s: esc ×%d should close the chat", keymap, n)
		}
	}
}
