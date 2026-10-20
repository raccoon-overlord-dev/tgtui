package ui

import (
	"fmt"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"

	tea "charm.land/bubbletea/v2"

	"github.com/raccoon-overlord-dev/tgtui/internal/core"
	"github.com/raccoon-overlord-dev/tgtui/internal/ui/termimg"
)

// imageSource picks what to draw for a message's media: the downloaded preview, else the
// inline minithumbnail. key names the choice for the line cache ("" when nothing to draw).
func (m *model) imageSource(msg core.Message) (key, path string, data []byte) {
	md := msg.Media
	if md == nil || m.images == termimg.Off {
		return "", "", nil
	}
	if md.Thumb != 0 {
		if f := m.c.File(md.Thumb); f.Done {
			return fmt.Sprint("f", md.Thumb), f.Path, nil
		}
	}
	if len(md.Mini) > 0 {
		return fmt.Sprint("m", msg.ID), "", md.Mini
	}
	return "", "", nil
}

// drawImage renders an image in cols×rows cells. With kitty graphics it queues the image
// for transmission (flushed by after) and returns the placeholder cells.
func (m *model) drawImage(path string, data []byte, cols, rows int) []string {
	img, err := termimg.Decode(path, data)
	if err != nil {
		return nil
	}
	if m.images == termimg.Kitty {
		m.nextImg = m.nextImg%(1<<24-1) + 1
		m.sendQ = append(m.sendQ, termimg.Transmit(m.nextImg, img, cols, rows))
		return termimg.Placeholders(m.nextImg, cols, rows)
	}
	return termimg.DrawBlocks(img, cols, rows)
}

func size(n int64) string {
	switch {
	case n >= 1<<20:
		return fmt.Sprintf("%.1f MB", float64(n)/(1<<20))
	case n >= 1<<10:
		return fmt.Sprintf("%d KB", n>>10)
	}
	return fmt.Sprintf("%d B", n)
}

// mediaWork runs after an update: downloads the previews on screen, does open / save once
// their file is there, and flushes images queued for the terminal.
func (m *model) mediaWork(ids []int64) []tea.Cmd {
	var cmds []tea.Cmd
	for _, id := range ids {
		i, ok := m.indexOf(id)
		if !ok || m.win.Messages[i].Media == nil {
			continue
		}
		if t := m.win.Messages[i].Media.Thumb; t != 0 && !m.requested[t] && !m.c.File(t).Done {
			m.requested[t] = true
			cmds = append(cmds, run(func() error { return m.c.Download(t, 1) }))
		}
	}
	for id, act := range m.pending {
		if f := m.c.File(id); f.Done {
			delete(m.pending, id)
			cmds = append(cmds, m.fileAction(id, act))
		}
	}
	if len(m.sendQ) > 0 {
		cmds = append(cmds, tea.Raw(strings.Join(m.sendQ, "")))
		m.sendQ = nil
	}
	return cmds
}

// fileKey handles o (open) and s (save) on the selected message: now if the file is
// there, after downloading it otherwise.
func (m *model) fileKey(act string) tea.Cmd {
	sel := m.selected()
	if sel == nil || sel.Media == nil || sel.Media.File == 0 {
		m.flash = "✕ No file in this message"
		return nil
	}
	id := sel.Media.File
	if act == "save" {
		act += ":" + sel.Media.Name
	}
	if m.c.File(id).Done {
		return m.fileAction(id, act)
	}
	m.pending[id] = act
	m.flash = "Downloading…"
	return run(func() error { return m.c.Download(id, 32) })
}

func (m *model) fileAction(id int32, act string) tea.Cmd {
	if name, ok := strings.CutPrefix(act, "save:"); ok {
		return func() tea.Msg {
			p, err := m.c.SaveToDownloads(id, name)
			if err != nil {
				return doneMsg{err}
			}
			return flashMsg("✓ Saved to " + p)
		}
	}
	path := m.c.File(id).Path
	return func() tea.Msg {
		opener := "xdg-open"
		if runtime.GOOS == "darwin" {
			opener = "open"
		}
		cmd := exec.Command(opener, path) // stdout / stderr: /dev/null, not over the TUI
		if err := cmd.Start(); err != nil {
			return doneMsg{err}
		}
		go cmd.Wait()
		return nil
	}
}

// fileDialog is the system's file picker, or nil where there is none (over ssh, no
// desktop, no zenity / kdialog on Linux). Each prints the files chosen, one per line.
func fileDialog() *exec.Cmd {
	if os.Getenv("SSH_TTY") != "" {
		return nil
	}
	if runtime.GOOS == "darwin" {
		return exec.Command("osascript", "-e", `set out to ""`,
			"-e", `repeat with f in (choose file with prompt "Attach files" with multiple selections allowed)`,
			"-e", "set out to out & POSIX path of f & linefeed", "-e", "end repeat", "-e", "return out")
	}
	if os.Getenv("DISPLAY") == "" && os.Getenv("WAYLAND_DISPLAY") == "" {
		return nil
	}
	if p, err := exec.LookPath("zenity"); err == nil {
		return exec.Command(p, "--file-selection", "--multiple", "--separator=\n", "--title=Attach files")
	}
	if p, err := exec.LookPath("kdialog"); err == nil {
		home, _ := os.UserHomeDir()
		return exec.Command(p, "--getopenfilename", home, "--multiple", "--separate-output", "--title", "Attach files")
	}
	return nil
}

type chosenMsg struct {
	chat  int64
	paths []string
}

// chooseFiles runs the file picker for chat; cancelling it (exit status 1) attaches nothing.
func chooseFiles(chat int64, cmd *exec.Cmd) tea.Cmd {
	return func() tea.Msg {
		out, err := cmd.Output() // stdin is /dev/null: the picker never reads the TUI's input
		if ee, ok := err.(*exec.ExitError); ok && ee.ExitCode() == 1 {
			return chosenMsg{chat: chat}
		} else if err != nil {
			return doneMsg{err}
		}
		var paths []string
		for _, l := range strings.Split(string(out), "\n") {
			if l = strings.TrimSpace(l); l != "" {
				paths = append(paths, l)
			}
		}
		return chosenMsg{chat, paths}
	}
}

// pathKey edits the "attach file" prompt: tab completes, ↑ ↓ pick a completion, enter
// adds the file (or opens the folder picked).
func (m *model) pathKey(k tea.KeyPressMsg) tea.Cmd {
	switch k.String() {
	case "esc":
		m.pathIn = nil
		return nil
	case "up":
		m.pathSel = max(m.pathSel-1, -1)
		return nil
	case "down":
		m.pathSel = min(m.pathSel+1, len(m.pathItems)-1)
		return nil
	case "tab":
		*m.pathIn = newInput(complete(m.pathIn.value()))
	case "enter":
		p := expand(strings.TrimSpace(m.pathIn.value()))
		if m.pathSel >= 0 && m.pathSel < len(m.pathItems) {
			p = m.pathItems[m.pathSel]
		}
		st, err := os.Stat(p)
		switch {
		case err == nil && st.IsDir():
			*m.pathIn = newInput(strings.TrimSuffix(p, "/") + "/")
		case err != nil || !st.Mode().IsRegular():
			m.flash = "✕ Not a file: " + p
			return nil
		default:
			m.attach, m.pathIn = append(m.attach, p), nil
			return nil
		}
	default:
		m.pathIn.key(k, false)
	}
	m.listPaths()
	return nil
}

// listPaths finds the first completions of the typed path.
func (m *model) listPaths() {
	ms, _ := filepath.Glob(expand(m.pathIn.value()) + "*")
	m.pathItems, m.pathSel = ms[:min(len(ms), 4)], -1
}

func expand(p string) string {
	if rest, ok := strings.CutPrefix(p, "~/"); ok {
		home, _ := os.UserHomeDir()
		return filepath.Join(home, rest)
	}
	return p
}

// complete extends a path to the longest prefix all matching files share, and adds a
// slash to a single matching directory.
func complete(s string) string {
	ms, _ := filepath.Glob(expand(s) + "*")
	if len(ms) == 0 {
		return s
	}
	common := ms[0]
	for _, x := range ms[1:] {
		for !strings.HasPrefix(x, common) {
			common = common[:len(common)-1]
		}
	}
	if st, err := os.Stat(common); len(ms) == 1 && err == nil && st.IsDir() {
		common += "/"
	}
	return common
}

// pastedPaths reads a paste as file paths, as terminals paste dropped files: one per line
// or separated by spaces, shell-quoted or escaped, or file:// URLs. It returns nil unless
// every one is an existing file.
func pastedPaths(s string) []string {
	var out, words []string
	var cur strings.Builder
	quote, esc, quoted := rune(0), false, false
	for _, r := range strings.TrimSpace(s) {
		switch {
		case esc:
			cur.WriteRune(r)
			esc = false
		case r == '\\' && quote != '\'':
			esc = true
		case quote != 0 && r == quote:
			quote = 0
		case quote == 0 && (r == '\'' || r == '"'):
			quote, quoted = r, true
		case quote == 0 && (r == ' ' || r == '\n' || r == '\r' || r == '\t'):
			if cur.Len() > 0 || quoted {
				words, quoted = append(words, cur.String()), false
				cur.Reset()
			}
		default:
			cur.WriteRune(r)
		}
	}
	if cur.Len() > 0 {
		words = append(words, cur.String())
	}
	for _, w := range words {
		if u, ok := strings.CutPrefix(w, "file://"); ok {
			w, _ = url.PathUnescape(u)
		}
		if st, err := os.Stat(expand(w)); err != nil || !st.Mode().IsRegular() {
			return nil
		}
		out = append(out, expand(w))
	}
	return out
}
