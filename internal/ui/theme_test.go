package ui

import (
	tea "charm.land/bubbletea/v2"
	"image/color"
	"os"
	"strings"
	"testing"

	"github.com/BurntSushi/toml"
	"github.com/charmbracelet/x/ansi"
)

// Every theme defines every role the UI reads, directly or by reference.
func TestThemes(t *testing.T) {
	roles := []string{"base.background", "base.text", "base.title", "base.secondary", "base.border", "focus.focused_border",
		"focus.accent", "focus.selection_bg", "focus.selection_fg", "chat_list.unread_badge_fg", "chat_list.unread_badge_bg",
		"chat_list.muted_badge_fg", "chat_list.muted_badge_bg", "chat_list.mention_badge_fg", "chat_list.mention_badge_bg",
		"messages.reply_bar", "messages.preview_bar", "messages.read", "format.link", "format.mention", "format.code_fg",
		"format.code_bg", "format.quote_text", "format.spoiler_hidden", "format.spoiler_revealed", "states.error",
		"states.warning", "states.success", "states.connection", "states.update", "messages.senders.5"}
	for _, name := range themeNames {
		b, err := themeFiles.ReadFile("themes/" + name + ".toml")
		if err != nil {
			t.Fatal(err)
		}
		f := themeFile{}
		if _, err := toml.Decode(string(b), &f.t); err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		for _, r := range roles {
			if f.get(r) == nil {
				t.Errorf("%s: no %s", name, r)
			}
		}
		setTheme(name, true, true)
		if got := paint("x"); (name == "terminal") != (got == "x") {
			t.Errorf("%s: paint %q", name, got)
		}
	}
	setTheme("terminal", false, true)
}

func TestComposerWrap(t *testing.T) {
	in := newInput("one two three")
	ls, row := in.lines(text, 9)
	var got []string
	for _, l := range ls {
		got = append(got, ansi.Strip(l))
	}
	if strings.Join(got, "|") != "one two |three " || row != 1 {
		t.Fatalf("%q row %d", got, row)
	}
}

// The notification icon takes each painted theme's background and accent.
func TestIconColours(t *testing.T) {
	defer setTheme("terminal", false, false)
	setTheme("harvest-light", false, true)
	if iconBg != "#f6eedf" || iconAccent != "#ab510f" {
		t.Fatalf("harvest-light icon: %s %s", iconBg, iconAccent)
	}
	setTheme("terminal", false, false) // ANSI only: harvest's colours
	if iconBg != "#1c1612" || iconAccent != "#e8833a" {
		t.Fatalf("terminal icon: %s %s", iconBg, iconAccent)
	}
}

// docs/icon.svg, the README's icon, is the notification icon in harvest's colours.
func TestIconFile(t *testing.T) {
	b, err := os.ReadFile("../../docs/icon.svg")
	if err != nil || string(b) != iconSVG("#1c1612", "#e8833a") {
		t.Fatalf("docs/icon.svg is not iconSVG in harvest's colours (%v); rewrite it from iconSVG", err)
	}
}

// Follow light / dark: a theme on the wrong kind of terminal background gives way to its
// pair, as soon as the terminal reports its colour.
func TestAutoDark(t *testing.T) {
	for _, c := range []struct {
		name string
		dark bool
		want string
	}{{"harvest", false, "harvest-light"}, {"harvest", true, "harvest"}, {"catppuccin-latte", true, "catppuccin-mocha"},
		{"harvest-light", false, "harvest-light"}, {"terminal", false, "terminal"}} {
		if got := themeFor(c.name, c.dark); got != c.want {
			t.Errorf("themeFor(%s, dark %v) = %s", c.name, c.dark, got)
		}
	}
	defer setTheme("terminal", false, false)
	m := demo(100, 30)
	m.o.Config.Appearance.Theme, m.o.Config.Appearance.AutoDark = "harvest", true
	m.Update(tea.BackgroundColorMsg{Color: color.White})
	if m.themeUsed != "harvest-light" {
		t.Fatalf("light terminal: theme %s", m.themeUsed)
	}
}
