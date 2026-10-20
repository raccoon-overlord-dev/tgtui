package ui

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/charmbracelet/x/ansi"

	tea "charm.land/bubbletea/v2"

	"github.com/raccoon-overlord-dev/tgtui/internal/config"
	"github.com/raccoon-overlord-dev/tgtui/internal/update"
)

// setRow is one row of Settings: a choice (opts, get, set), an action (act) or a line
// of information (info).
type setRow struct {
	section, label string
	opts           []string
	get            func(*config.Config) string
	set            func(*config.Config, string)
	act            func(*model) tea.Cmd
	info           func(*model) string
}

func choice(section, label string, opts []string, field func(*config.Config) *string) setRow {
	return setRow{section: section, label: label, opts: opts,
		get: func(c *config.Config) string { return *field(c) },
		set: func(c *config.Config, v string) { *field(c) = v }}
}

func onOff(section, label string, field func(*config.Config) *bool) setRow {
	return setRow{section: section, label: label, opts: []string{"on", "off"},
		get: func(c *config.Config) string { return map[bool]string{true: "on", false: "off"}[*field(c)] },
		set: func(c *config.Config, v string) { *field(c) = v == "on" }}
}

var settingRows = []setRow{
	choice("Appearance", "Theme", themeNames, func(c *config.Config) *string { return &c.Appearance.Theme }),
	choice("Appearance", "Background", []string{"theme", "terminal"}, func(c *config.Config) *string { return &c.Appearance.Background }),
	choice("Appearance", "Time format", []string{"24h", "12h"}, func(c *config.Config) *string { return &c.Appearance.TimeFormat }),
	onOff("Appearance", "Profile pictures", func(c *config.Config) *bool { return &c.Appearance.Avatars }),
	choice("Appearance", "Images", []string{"auto", "kitty", "blocks", "off"}, func(c *config.Config) *string { return &c.Media.Images }),

	choice("Files", "Attach with", []string{"dialog", "path"}, func(c *config.Config) *string { return &c.Media.Attach }),

	choice("Notifications", "Notifications", []string{"auto", "terminal", "system", "off"}, func(c *config.Config) *string { return &c.Notifications.Mode }),
	onOff("Notifications", "Message text", func(c *config.Config) *bool { return &c.Notifications.Preview }),
	onOff("Notifications", "Sound (terminal bell)", func(c *config.Config) *bool { return &c.Notifications.Sound }),
	{section: "Notifications", label: "Send a test notification", act: (*model).testNotify},

	choice("Keys", "Keymap", []string{"vim+telegram", "telegram"}, func(c *config.Config) *string { return &c.Keys.Keymap }),
	choice("Keys", "Keyboard layout", []string{"us", "it"}, func(c *config.Config) *string { return &c.Keys.Layout }),
	choice("Keys", "Chats open in", []string{"insert", "normal"}, func(c *config.Config) *string { return &c.Keys.OpenIn }),

	onOff("Updates", "Check daily", func(c *config.Config) *bool { return &c.Updates.Check }),
	{section: "Updates", label: "Check now", act: func(m *model) tea.Cmd { m.flash = "Checking…"; return m.checkUpdate(true) }},
	{section: "Updates", label: "Install update", act: func(m *model) tea.Cmd { m.settings = false; m.askUpdate(); return nil }},

	{section: "Account", label: "Signed in as", info: func(m *model) string {
		me := m.snap.Me
		if me == nil {
			return ""
		}
		s := strings.TrimSpace(me.FirstName + " " + me.LastName)
		if me.Username != "" {
			s += " @" + me.Username
		}
		return s + " · +" + me.Phone
	}},
	{section: "Account", label: "Servers", info: func(m *model) string {
		return map[bool]string{true: "test DC", false: "production"}[m.o.TestDC]
	}},
	{section: "Account", label: "Log out", act: func(m *model) tea.Cmd { m.settings, m.askLogout = false, true; return nil }},

	{section: "About", label: "Version", info: func(m *model) string { return "tgtui " + m.o.Version }},
	{section: "About", label: "TDLib", info: func(m *model) string { return m.snap.TDLib }},
	{section: "About", label: "Config", info: func(*model) string { return config.Path }},
	{section: "About", label: "Data", info: func(m *model) string { return m.o.DataDir }},
	{section: "About", label: "Cache", info: func(*model) string { return config.CacheDir() }},
	{section: "About", label: "Project", info: func(*model) string { return update.Repo }},
}

func (r setRow) selectable() bool { return r.info == nil }

func (m *model) openSettings() {
	m.settings, m.setSel = true, 0
}

func (m *model) settingsKey(key string) tea.Cmd {
	r := settingRows[m.setSel]
	switch key {
	case "esc", "q", "f2":
		m.settings = false
	case "up", "k", "shift+tab":
		m.moveSetting(-1)
	case "down", "j", "tab":
		m.moveSetting(1)
	case "left", "h", "right", "l", "enter", "space":
		if r.act != nil {
			if key == "enter" || key == "space" {
				return r.act(m)
			}
			return nil
		}
		if r.opts == nil {
			return nil
		}
		d := 1
		if key == "left" || key == "h" {
			d = -1
		}
		cur := 0
		for i, o := range r.opts {
			if o == r.get(m.o.Config) {
				cur = i
			}
		}
		r.set(m.o.Config, r.opts[(cur+d+len(r.opts))%len(r.opts)])
		m.applyConfig()
		if err := config.Save(m.o.Config); err != nil {
			m.flash = "✕ Settings not saved: " + err.Error()
		}
	}
	return nil
}

func (m *model) moveSetting(d int) {
	for i := m.setSel + d; i >= 0 && i < len(settingRows); i += d {
		if settingRows[i].selectable() {
			m.setSel = i
			return
		}
	}
}

// settingsView: the sections on the left, all rows in one scrolling list on the right.
func (m *model) settingsView(h int) []string {
	navW := 18
	if m.w >= 100 {
		navW = 20
	}
	cur := settingRows[m.setSel].section
	var nav []string
	for i, r := range settingRows {
		if i > 0 && settingRows[i-1].section == r.section {
			continue
		}
		if r.section == cur {
			nav = append(nav, accent.Render("▌")+fit(title.Render(r.section), navW-4)+" ")
		} else {
			nav = append(nav, pad(r.section, navW-4))
		}
	}
	left := box(navW, h, borderSt, title.Render("Settings"), "", "", nav)
	w := m.w - navW - 1
	iw := w - 4
	lab := 22
	if iw >= 50 {
		lab = 26
	}
	var lines []string
	selLine, section := 0, settingRows[0].section
	for i, r := range settingRows {
		if r.section != section {
			section = r.section
			lines = append(lines, "", pad(title.Render(section), iw))
		}
		sel := i == m.setSel
		t, T, s, fill := rowStyles(sel)
		var val string
		switch {
		case r.info != nil:
			val = dim.Render(r.info(m))
		case r.act != nil:
			val = s.Render("enter")
		default:
			v := t
			if sel {
				v = T
			}
			val = s.Render("‹ ") + v.Render(r.get(m.o.Config)) + s.Render(" ›")
		}
		note := ""
		if r.label == "Theme" && iw-lab-ansi.StringWidth(val) > 30 {
			note = s.Render(map[bool]string{true: "uses its own background", false: "the terminal's colours"}[paintSeq != ""])
		}
		if sel {
			selLine = len(lines)
		}
		lines = append(lines, row(iw, fill, t.Render(fit(r.label, lab-1))+fill.Render(" ")+val, note))
	}
	top := max(selLine-(h-3), 0)
	right := box(w, h, focusBorder, title.Render(cur), "", "", lines[top:])
	out := make([]string, h)
	for i := range out {
		out[i] = left[i] + " " + right[i]
	}
	return out
}

// testNotify sends a notification the way real ones would go, whatever the focus.
func (m *model) testNotify() tea.Cmd {
	var cmds []tea.Cmd
	if m.o.Config.Notifications.Sound {
		cmds = append(cmds, tea.Raw("\a"))
	}
	switch m.notifyTo {
	case "off":
		m.flash = "Notifications are off"
		return nil
	case "terminal":
		cmds = append(cmds, tea.Raw(oscNotify("tgtui", "Test notification")))
	default:
		cmds = append(cmds, systemNotify("tgtui", "Test notification"))
	}
	m.flash = "✓ Test notification sent (" + m.notifyTo + ")"
	return tea.Batch(cmds...)
}

type updateMsg struct {
	tag    string
	err    error
	manual bool
}

// checkUpdate looks for a newer release: once a day on start when enabled, or now (manual).
func (m *model) checkUpdate(manual bool) tea.Cmd {
	if !manual && !m.o.Config.Updates.Check {
		return nil
	}
	return func() tea.Msg {
		tag, err := update.Check(context.Background(), manual)
		return updateMsg{tag, err, manual}
	}
}

func (m *model) updated(u updateMsg) {
	switch {
	case u.err != nil && u.manual:
		m.flash = "✕ Update check failed: " + u.err.Error()
	case update.Newer(u.tag, m.o.Version):
		m.newVersion = u.tag
		if u.manual {
			m.flash = "tgtui " + u.tag + " is available: Settings › Install update"
		}
	case u.manual && m.o.Version == "dev":
		m.flash = "Development build: latest release is " + u.tag
	case u.manual:
		m.flash = "✓ tgtui is up to date (" + m.o.Version + ")"
	}
}

// askUpdate offers to install the newer release (U, Settings › Install update).
func (m *model) askUpdate() {
	if m.newVersion == "" {
		m.flash = "No update available (tgtui " + m.o.Version + "): Settings › Check now"
		return
	}
	tag := m.newVersion
	m.menu = &chatMenu{title: "Update tgtui?", busy: "Downloading " + tag + "…", quote: "tgtui " + m.o.Version + " → " + tag, items: []menuItem{
		{"Download and install " + tag, "", func() error { return installUpdate(tag) }, "Installed " + tag + ": restart tgtui to use it"},
		{label: "Cancel"},
	}}
}

// installUpdate replaces the running binary with release tag, checksum verified.
func installUpdate(tag string) error {
	exe, err := os.Executable()
	if err == nil {
		exe, err = filepath.EvalSymlinks(exe)
	}
	if err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()
	if err := update.Apply(ctx, tag, exe); errors.Is(err, update.ErrNotWritable) {
		return fmt.Errorf("no write access to %s: update with %s", exe, update.Command)
	} else if err != nil {
		return err
	}
	return nil
}
