package ui

import (
	"fmt"
	"strings"
	"unicode"

	tea "charm.land/bubbletea/v2"

	"github.com/raccoon-overlord-dev/tgtui/internal/core"
	"github.com/raccoon-overlord-dev/tgtui/internal/ui/termimg"
)

// Profile pictures (Settings › Appearance › Profile pictures): 4×2 cells in the chat
// list, 2×1 before the chat title and the sender lines of groups. Each is about square,
// a cell being twice as tall as wide.

// avKey names a picture drawn at a size: kitty images stay sent until the chat changes.
type avKey struct {
	src        string
	cols, rows int
}

// avatar is the picture of chat or sender id in cols×rows cells: with kitty graphics the
// photo (its minithumbnail until the small file is downloaded), otherwise the initials on
// the sender's colour. Half blocks get initials too: 4×4 pixels show only a blur. nil when
// pictures are off.
func (m *model) avatar(ph core.Photo, id int64, name string, cols, rows int) []string {
	if !m.o.Config.Appearance.Avatars {
		return nil
	}
	if m.images == termimg.Kitty {
		path, data, src := "", ph.Mini, fmt.Sprint("m", id)
		if ph.File != 0 {
			if f := m.c.File(ph.File); f.Done {
				path, data, src = f.Path, nil, fmt.Sprint("f", ph.File)
			} else if !m.requested[ph.File] {
				m.requested[ph.File] = true
				m.avatarDL = append(m.avatarDL, ph.File)
			}
		}
		if path != "" || len(data) > 0 {
			k := avKey{src, cols, rows}
			if l, ok := m.avatars[k]; ok {
				return l
			}
			if l := m.drawImage(path, data, cols, rows); l != nil {
				m.avatars[k] = l
				return l
			}
		}
	}
	k := avKey{fmt.Sprint("i", id, name), cols, rows}
	if l, ok := m.avatars[k]; ok {
		return l
	}
	m.avatars[k] = initials(name, id, cols, rows)
	return m.avatars[k]
}

func (m *model) chatAvatar(ch *core.Chat, cols, rows int) []string {
	return m.avatar(ch.Photo, ch.ID, m.chatName(ch), cols, rows)
}

// initials are the first letters of up to two words of name (one in 2 cells), centred on
// the top row of a block in id's colour.
func initials(name string, id int64, cols, rows int) []string {
	var in []rune
	for _, w := range strings.Fields(name) {
		if r := []rune(w)[0]; unicode.IsLetter(r) || unicode.IsDigit(r) {
			in = append(in, unicode.ToUpper(r))
		}
	}
	in = in[:min(len(in), max(cols/2, 1))]
	st := nameStyle(id).Reverse(true).Bold(true)
	out := make([]string, rows)
	for y := range out {
		s := ""
		if y == 0 {
			s = string(in)
		}
		out[y] = st.Render(center(s, cols))
	}
	return out
}

// warmAvatars draws the pictures View will show, before it: kitty transmissions are
// queued while drawing and must be written ahead of the frame. It returns the downloads
// of pictures not on disk yet.
func (m *model) warmAvatars() []tea.Cmd {
	if m.o.Config.Appearance.Avatars && m.images == termimg.Kitty {
		m.scrollList() // as listPane will
		for i := m.listTop; i < min(m.listTop+m.listRows(), len(m.chats)); i++ {
			m.chatAvatar(m.chats[i], 4, 2)
		}
		if ch := m.openChatInfo(); ch != nil {
			m.chatAvatar(ch, 2, 1)
		}
	}
	var cmds []tea.Cmd
	for _, id := range m.avatarDL {
		cmds = append(cmds, run(func() error { return m.c.Download(id, 1) }))
	}
	m.avatarDL = nil
	return cmds
}
