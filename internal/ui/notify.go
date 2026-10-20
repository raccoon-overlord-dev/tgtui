package ui

import (
	"os"
	"os/exec"
	"runtime"
	"strings"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/raccoon-overlord-dev/tgtui/internal/core"
	"github.com/raccoon-overlord-dev/tgtui/internal/ui/termimg"
)

// notifyMode resolves the setting (auto | off | terminal | system). Over ssh or in tmux an
// OS notification would pop up on the wrong machine or nowhere, so auto asks the terminal.
func notifyMode(setting string) string {
	if setting != "" && setting != "auto" {
		return setting
	}
	term, prog := os.Getenv("TERM"), os.Getenv("TERM_PROGRAM")
	switch {
	case os.Getenv("SSH_TTY") != "", os.Getenv("TMUX") != "", os.Getenv("HERDR_ENV") != "",
		term == "xterm-kitty", term == "xterm-ghostty", prog == "ghostty", prog == "iTerm.app", prog == "WezTerm":
		return "terminal"
	}
	return "system"
}

// plain drops control characters, so a message can't end the escape sequence early.
func plain(s string) string {
	return strings.Map(func(r rune) rune {
		if r < 0x20 || r == 0x7f || (r >= 0x80 && r < 0xa0) {
			return -1
		}
		return r
	}, s)
}

// oscNotify is the terminal's own notification: kitty's OSC 99, Ghostty's OSC 777 (title
// and body), else iTerm2's OSC 9, which most terminals understand.
func oscNotify(title, body string) string {
	title, body = plain(title), plain(body)
	term, prog := os.Getenv("TERM"), os.Getenv("TERM_PROGRAM")
	var seq string
	switch {
	case term == "xterm-kitty":
		seq = ansi.DesktopNotification(title, "i=1", "d=0") + ansi.DesktopNotification(body, "i=1", "d=1", "p=body")
	case term == "xterm-ghostty" || prog == "ghostty":
		seq = "\x1b]777;notify;" + strings.ReplaceAll(title, ";", ",") + ";" + body + "\x07"
	default:
		seq = ansi.Notify(title + ": " + body)
	}
	return termimg.Passthrough(seq)
}

func systemNotify(title, body string) tea.Cmd {
	icon := iconPath() // here: setTheme changes the colours on the UI goroutine
	return func() tea.Msg {
		var err error
		switch runtime.GOOS {
		case "darwin": // text passed as arguments, never inside the script
			err = exec.Command("osascript", "-e", "on run argv", "-e", "display notification (item 2 of argv) with title (item 1 of argv)",
				"-e", "end run", title, body).Run()
		default:
			args := []string{"-a", "tgtui", title, body}
			if icon != "" {
				args = append([]string{"-i", icon}, args...)
			}
			err = exec.Command("notify-send", args...).Run()
		}
		if err != nil {
			return flashMsg("Notification failed: " + err.Error())
		}
		return nil
	}
}

// notify shows a notification: nothing for the chat in front of the user, the status bar
// while the terminal has focus, the terminal's or the OS's notification otherwise.
func (m *model) notify(n core.Notification) tea.Cmd {
	if m.notifyTo == "off" || n.ChatID == m.open && m.chatShown() && m.focused != focusAway {
		return nil
	}
	title, body := n.Chat, label(n.Message)
	if ch := m.c.Chat(n.ChatID); ch != nil && ch.Kind == core.ChatGroup && n.Message.Sender != "" {
		body = n.Message.Sender + ": " + body
	}
	if !n.Preview || !m.o.Config.Notifications.Preview {
		body = "New message"
	}
	body = strings.ReplaceAll(body, "\n", " ")
	var cmds []tea.Cmd
	if m.o.Config.Notifications.Sound && !n.Silent {
		cmds = append(cmds, tea.Raw("\a"))
	}
	switch {
	case m.focused == focusHere:
		m.flash = title + ": " + body
	case m.notifyTo == "terminal":
		cmds = append(cmds, tea.Raw(oscNotify(title, body)))
	default:
		cmds = append(cmds, systemNotify(title, body))
	}
	return tea.Batch(cmds...)
}
