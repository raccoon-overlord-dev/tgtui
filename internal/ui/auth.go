package ui

import (
	"strings"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/raccoon-overlord-dev/tgtui/internal/core"
)

// authState is the sign-in screen's own state; it resets when TDLib's step changes.
type authState struct {
	input      []rune // ponytail: append/backspace only, enough for a number, a code, a password
	phoneEntry bool   // typing a phone number while TDLib waits for something else (QR, code, password)
	busy       bool   // a sign-in request is in flight
	err        string
}

func (m *model) authKey(k tea.KeyPressMsg) tea.Cmd {
	s, kind := &m.auth, m.snap.Auth.Kind
	switch kind {
	case core.AuthPhone, core.AuthCode, core.AuthPassword, core.AuthQR:
	default:
		return nil
	}
	if s.busy {
		return nil
	}
	do := func(f func() error) tea.Cmd { s.busy, s.err = true, ""; return run(f) }
	switch k.String() {
	case "enter":
		v := string(s.input)
		switch {
		case v == "":
		case s.phoneEntry || kind == core.AuthPhone:
			return do(func() error { return m.c.SetPhone(v) })
		case kind == core.AuthCode:
			return do(func() error { return m.c.CheckCode(v) })
		case kind == core.AuthPassword:
			return do(func() error { return m.c.CheckPassword(v) })
		}
	case "tab":
		if kind == core.AuthPhone {
			return do(m.c.RequestQR)
		}
		if kind == core.AuthQR {
			s.phoneEntry, s.input, s.err = !s.phoneEntry, nil, ""
		}
	case "esc":
		if kind != core.AuthPhone { // back from the phone entry, or to it from a code / password
			s.phoneEntry, s.input, s.err = !s.phoneEntry, nil, ""
		}
	case "backspace":
		if len(s.input) > 0 {
			s.input = s.input[:len(s.input)-1]
		}
	default:
		s.input = append(s.input, []rune(k.Text)...)
	}
	return nil
}

// signin is the sign-in pane, centred: rows (w-6 cells each) inside, keys in the bottom border.
func (m *model) signin(h, w int, rows []string, bottom string, focused bool) []string {
	w = min(w, m.w-4)
	inner := []string{""}
	for _, r := range rows {
		inner = append(inner, "  "+fit(r, w-6)+"  ")
	}
	b := box(w, len(inner)+3, borderOf(focused), title.Render("tgtui")+"  "+dim.Render("an unofficial Telegram client"), "", bottom, inner)
	screen := make([]string, h)
	place(screen, b, (m.w-w)/2, max((h-len(b))/2, 0), m.w)
	return screen
}

// keyHints writes "enter send code · tab QR code instead" with the keys bold.
func keyHints(pairs ...string) string {
	var parts []string
	for i := 0; i+1 < len(pairs); i += 2 {
		parts = append(parts, title.Render(pairs[i])+dim.Render(" "+pairs[i+1]))
	}
	return strings.Join(parts, dim.Render(" · "))
}

// input is the sign-in field: its own box, dimmed while a request is in flight, red on an error.
func (m *model) input(w int, mask bool, codeLen int) []string {
	s := &m.auth
	v := string(s.input)
	switch {
	case mask:
		v = strings.Repeat("•", len(s.input))
	case codeLen > 0: // the code's digits spaced, with blanks for the missing ones
		v = strings.Join(strings.Split(v, ""), " ")
	}
	content := text.Render(v)
	if s.busy {
		content = dim.Render(v)
	} else {
		if v != "" && codeLen > 0 {
			content += " "
		}
		content += text.Reverse(true).Render(" ")
		if n := codeLen - len(s.input) - 1; n > 0 {
			content += dim.Render(strings.Repeat(" _", n))
		}
	}
	bst := borderOf(!s.busy)
	if s.err != "" {
		bst = bad.UnsetBold()
	}
	return box(w, 3, bst, "", "", "", []string{" " + content})
}

func (m *model) authView(h int) []string {
	a, s := m.snap.Auth, &m.auth
	w := min(52, m.w-4)
	var rows []string
	add := func(l ...string) { rows = append(rows, l...) }
	field := func(mask bool, codeLen int) {
		add(m.input(w-6, mask, codeLen)...)
		if s.err != "" {
			add("", bad.Render("✕ "+s.err))
		}
	}
	explain := func(t string) {
		for _, l := range renderText(t, nil, w-6, true) {
			add(dim.Render(l))
		}
	}
	keys := func(pairs ...string) string {
		if s.busy {
			return title.Render(m.spinner() + " checking")
		}
		return keyHints(pairs...)
	}
	switch {
	case a.Kind == core.AuthStarting, a.Kind == core.AuthClosing:
		what, why := "Starting", "opening the local database"
		if a.Kind == core.AuthClosing {
			what, why = "Closing", "logging out"
		}
		return m.signin(h, w, []string{title.Render(m.spinner() + " " + what), dim.Render(why)}, "", false)
	case s.phoneEntry || a.Kind == core.AuthPhone:
		add(title.Render("Sign in with your phone number"), "", dim.Render("Phone number, with country code"))
		field(false, 0)
		add("")
		explain("Telegram sends a code to your other devices first, by SMS only if none is signed in.")
		if a.Kind == core.AuthPhone {
			return m.signin(h, w, rows, keys("enter", "send code", "tab", "QR code instead"), true)
		}
		return m.signin(h, w, rows, keys("enter", "send code", "esc", "back"), true)
	case a.Kind == core.AuthQR:
		return m.qrView(h)
	case a.Kind == core.AuthCode:
		add(title.Render("Enter the code"))
		switch a.CodeVia {
		case "TelegramMessage":
			add(dim.Render("Sent to Telegram on your other devices"), dim.Render("for "+a.Phone))
		case "Sms":
			add(dim.Render("Sent by SMS to " + a.Phone))
		case "Call":
			add(dim.Render("We're calling " + a.Phone))
		default:
			add(dim.Render("Sent to " + a.Phone))
		}
		add("")
		field(false, a.CodeLength)
		return m.signin(h, w, rows, keys("enter", "sign in", "esc", "change number"), true)
	case a.Kind == core.AuthPassword:
		add(title.Render("Two-step verification"))
		if a.PasswordHint != "" {
			add(dim.Render("Hint: ") + a.PasswordHint)
		}
		add("")
		field(true, 0)
		return m.signin(h, w, rows, keys("enter", "sign in", "esc", "change number"), true)
	}
	add(title.Render("Finish signing in with an official app"), "")
	explain("This account needs a step tgtui can't do (" + a.State + "). Complete it in Telegram, then start tgtui again.")
	return m.signin(h, w, rows, keyHints("ctrl+q", "quit"), true)
}

// qrView shows the login QR code (black on white in every theme) and how to scan it.
func (m *model) qrView(h int) []string {
	qr := strings.Split(qrBlock(m.snap.Auth.QRLink), "\n")
	qw := ansi.StringWidth(qr[0])
	w := min(qw+43, m.w-2)
	steps := []string{title.Render("Scan with your phone"), "", dim.Render("1  ") + "Open Telegram on your phone",
		dim.Render("2  ") + "Settings › Devices", "   › Link Desktop Device", dim.Render("3  ") + "Point the camera here"}
	inner := []string{""}
	for i := range max(len(qr), len(steps)) {
		l, r := strings.Repeat(" ", qw), ""
		if i < len(qr) {
			l = qr[i]
		}
		if i < len(steps) {
			r = steps[i]
		}
		if i == len(qr)-2 {
			r = dim.Render(m.spinner() + " waiting for the scan")
		}
		inner = append(inner, "  "+l+"   "+r)
	}
	b := box(w, len(inner)+3, focusBorder, title.Render("tgtui")+"  "+dim.Render("an unofficial Telegram client"), "",
		keyHints("tab", "phone number instead"), inner)
	screen := make([]string, h)
	place(screen, b, (m.w-w)/2, max((h-len(b))/2, 0), m.w)
	return screen
}
