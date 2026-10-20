package ui

import (
	"embed"
	"fmt"
	"image/color"
	"strconv"
	"strings"

	"charm.land/lipgloss/v2"
	"github.com/BurntSushi/toml"
)

// The designed themes (tgtui-handoff/themes): every file has the same roles. The UI reads
// them into the package's styles below, so views name roles, never colours.
//
//go:embed themes/*.toml
var themeFiles embed.FS

var themeNames = []string{"terminal", "catppuccin-mocha", "catppuccin-latte", "harvest", "harvest-light"}

// The theme's roles as styles, set by setTheme. The names follow the handoff's role codes.
var (
	text, title, dim, borderSt, focusBorder, accent lipgloss.Style
	selFill, selTitle, selDim                       lipgloss.Style // X R Q: text, title, secondary on the selection
	badge, mutedBadge, mentionBadge                 lipgloss.Style
	replyBar, previewBar, readMark                  lipgloss.Style
	linkSt, mentionSt, codeSt, quoteSt              lipgloss.Style
	spoilerSt, revealSt                             lipgloss.Style
	bad, warnSt, okSt, connSt, updateSt             lipgloss.Style
	dimmed                                          lipgloss.Style // the screen behind an overlay
	names                                           []lipgloss.Style
	paintSeq                                        string // SGR of the painted background and text, "" for the terminal's
)

var ansiNames = map[string]color.Color{
	"black": lipgloss.Black, "red": lipgloss.Red, "green": lipgloss.Green, "yellow": lipgloss.Yellow,
	"blue": lipgloss.Blue, "magenta": lipgloss.Magenta, "cyan": lipgloss.Cyan, "white": lipgloss.White,
	"bright_black": lipgloss.BrightBlack, "bright_red": lipgloss.BrightRed, "bright_green": lipgloss.BrightGreen,
	"bright_yellow": lipgloss.BrightYellow, "bright_blue": lipgloss.BrightBlue, "bright_magenta": lipgloss.BrightMagenta,
	"bright_cyan": lipgloss.BrightCyan, "bright_white": lipgloss.BrightWhite,
}

type themeFile struct {
	t    map[string]map[string]any
	x256 bool // the terminal has 256 colours: use the x256 indexes, contrast-checked by the design
}

// get resolves a role ("base.text", "messages.senders.2"), following "@section.key" references.
func (f themeFile) get(ref string) map[string]any {
	for range 8 {
		sec, key, _ := strings.Cut(ref, ".")
		key, idx, isIdx := strings.Cut(key, ".")
		v := f.t[sec][key]
		if isIdx {
			i, _ := strconv.Atoi(idx)
			switch a := v.(type) {
			case []map[string]any:
				v = a[i%len(a)]
			case []any:
				v = a[i%len(a)]
			}
		}
		switch v := v.(type) {
		case string:
			ref = strings.TrimPrefix(v, "@")
			continue
		case map[string]any:
			return v
		}
		return nil
	}
	return nil
}

func (f themeFile) colour(v map[string]any) color.Color {
	if h, ok := v["hex"].(string); ok {
		if n, ok := v["x256"].(int64); ok && f.x256 {
			return lipgloss.Color(strconv.FormatInt(n, 10))
		}
		return lipgloss.Color(h)
	}
	if a, ok := v["ansi"].(string); ok {
		return ansiNames[a] // nil for "default"
	}
	return nil
}

// style is the role fg on the role bg ("" for none). A bg with reverse (the terminal
// theme's selection and badges) is drawn as reverse video of its colour.
func (f themeFile) style(fg, bg string) lipgloss.Style {
	v := f.get(fg)
	st := lipgloss.NewStyle().Bold(v["bold"] == true).Italic(v["italic"] == true).Underline(v["underline"] == true).
		Faint(v["faint"] == true).Reverse(v["reverse"] == true)
	if c := f.colour(v); c != nil {
		st = st.Foreground(c)
	}
	if bg == "" {
		return st
	}
	b := f.get(bg)
	if b["reverse"] == true {
		st = st.Reverse(true)
		if b["bold"] == true {
			st = st.Bold(true)
		}
		if c := f.colour(b); c != nil {
			st = st.Foreground(c)
		}
		return st
	}
	if c := f.colour(b); c != nil {
		st = st.Background(c)
	}
	return st
}

// setTheme loads the theme name (terminal when unknown) into the styles. paint false keeps
// the terminal's background whatever the theme says (Settings › Background).
// themeFor is name, or its pair ([meta] pair) when name is a dark theme on a light
// terminal or the other way round. The terminal theme has no pair.
func themeFor(name string, dark bool) string {
	var f struct {
		Meta struct{ Variant, Pair string } `toml:"meta"`
	}
	b, err := themeFiles.ReadFile("themes/" + name + ".toml")
	if err != nil || toml.Unmarshal(b, &f) != nil || f.Meta.Pair == "" {
		return name
	}
	if f.Meta.Variant == "dark" && !dark || f.Meta.Variant == "light" && dark {
		return f.Meta.Pair
	}
	return name
}

func setTheme(name string, x256, paint bool) {
	f := themeFile{x256: x256}
	b, err := themeFiles.ReadFile("themes/" + name + ".toml")
	if err != nil {
		b, _ = themeFiles.ReadFile("themes/terminal.toml")
	}
	if _, err := toml.Decode(string(b), &f.t); err != nil {
		panic("ui: bad theme " + name + ": " + err.Error())
	}
	iconBg, iconAccent = "#1c1612", "#e8833a"
	if bg, ok := f.get("base.background")["hex"].(string); ok {
		if ac, ok := f.get("focus.accent")["hex"].(string); ok {
			iconBg, iconAccent = bg, ac
		}
	}
	text, title, dim = f.style("base.text", ""), f.style("base.title", ""), f.style("base.secondary", "")
	borderSt, focusBorder, accent = f.style("base.border", ""), f.style("focus.focused_border", ""), f.style("focus.accent", "")
	selFill = f.style("focus.selection_fg", "focus.selection_bg")
	selTitle = f.style("base.title", "focus.selection_bg").Bold(true)
	selDim = f.style("base.secondary", "focus.selection_bg")
	badge = f.style("chat_list.unread_badge_fg", "chat_list.unread_badge_bg")
	mutedBadge = f.style("chat_list.muted_badge_fg", "chat_list.muted_badge_bg")
	mentionBadge = f.style("chat_list.mention_badge_fg", "chat_list.mention_badge_bg")
	replyBar, previewBar, readMark = f.style("messages.reply_bar", ""), f.style("messages.preview_bar", ""), f.style("messages.read", "")
	linkSt, mentionSt = f.style("format.link", ""), f.style("format.mention", "")
	codeSt, quoteSt = f.style("format.code_fg", "format.code_bg"), f.style("format.quote_text", "")
	spoilerSt, revealSt = f.style("format.spoiler_hidden", ""), f.style("format.spoiler_revealed", "")
	bad, warnSt, okSt = f.style("states.error", ""), f.style("states.warning", ""), f.style("states.success", "")
	connSt, updateSt = f.style("states.connection", ""), f.style("states.update", "")
	dimmed = borderSt.UnsetBold().UnsetItalic().UnsetUnderline()
	names = make([]lipgloss.Style, 6)
	for i := range names {
		names[i] = f.style(fmt.Sprint("messages.senders.", i), "")
	}
	paintSeq = ""
	if paint && f.t["meta"]["background"] == "paint" {
		paintSeq = sgr(f.get("base.background"), 48, x256) + sgr(f.get("base.text"), 38, x256)
	}
}

// sgr is the escape sequence that sets colour v as foreground (38) or background (48).
func sgr(v map[string]any, kind int, x256 bool) string {
	if n, ok := v["x256"].(int64); ok && x256 {
		return fmt.Sprintf("\x1b[%d;5;%dm", kind, n)
	}
	h, _ := v["hex"].(string)
	var r, g, b uint8
	fmt.Sscanf(h, "#%02x%02x%02x", &r, &g, &b)
	return fmt.Sprintf("\x1b[%d;2;%d;%d;%dm", kind, r, g, b)
}

// paint fills a finished screen line with the theme's background and text colour: it sets
// them at the start and again after every reset, so unstyled cells and padding get them too.
func paint(l string) string {
	if paintSeq == "" {
		return l
	}
	return paintSeq + strings.ReplaceAll(strings.ReplaceAll(l, "\x1b[m", "\x1b[m"+paintSeq), "\x1b[0m", "\x1b[0m"+paintSeq) + "\x1b[m"
}

func init() { setTheme("terminal", false, true) }
