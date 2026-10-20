package ui

import (
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/raccoon-overlord-dev/tgtui/internal/config"
)

// Voice notes are OGG Opus, which macOS can't play by itself: playing and recording go
// through programs found at run time.
const (
	noPlayer   = "Install mpv or ffmpeg (ffplay) to play voice and audio messages"
	noRecorder = "Install ffmpeg to record voice messages"
)

// playback is the voice note or audio file playing, or paused, in an external player.
// Pause and seek restart the player at a position: every player can start at an offset,
// while controlling a running one differs from player to player.
type playback struct {
	msg     int64
	path    string
	dur     time.Duration // 0 when unknown
	at      time.Duration // the position when the player started, or where it paused
	started time.Time
	cmd     *exec.Cmd // nil while paused
	gen     int
}

func (p *playback) pos() time.Duration {
	if p.cmd == nil {
		return p.at
	}
	t := p.at + time.Since(p.started)
	if p.dur > 0 {
		t = min(t, p.dur)
	}
	return t
}

type playEndMsg struct {
	gen int
	err error
}

// playerCmd plays path from at with the first player installed, nil if none is.
func playerCmd(path string, at time.Duration) *exec.Cmd {
	s := strconv.FormatFloat(at.Seconds(), 'f', 1, 64)
	if p, err := exec.LookPath("mpv"); err == nil {
		return exec.Command(p, "--no-video", "--no-terminal", "--start="+s, path)
	}
	if p, err := exec.LookPath("ffplay"); err == nil {
		return exec.Command(p, "-nodisp", "-autoexit", "-loglevel", "quiet", "-ss", s, path)
	}
	return nil
}

// startPlay runs the player for p from p.at. Its stdin, stdout and stderr are /dev/null,
// so it never touches the TUI's terminal.
func (m *model) startPlay(p *playback) tea.Cmd {
	cmd := playerCmd(p.path, p.at)
	if cmd == nil {
		m.flash = "✕ " + noPlayer
		return nil
	}
	if err := cmd.Start(); err != nil {
		return func() tea.Msg { return doneMsg{err} }
	}
	m.playGen++
	p.cmd, p.gen, p.started = cmd, m.playGen, time.Now()
	m.play = p
	gen := p.gen
	return func() tea.Msg { return playEndMsg{gen, cmd.Wait()} }
}

// pausePlay stops the player and keeps the position.
func (m *model) pausePlay() {
	if p := m.play; p != nil && p.cmd != nil {
		p.at = p.pos()
		p.cmd.Process.Kill()
		p.cmd = nil // its playEndMsg is ignored
	}
}

// stopAudio ends playback and recording, as tgtui quits.
func (m *model) stopAudio() {
	m.pausePlay()
	m.play = nil
	if r := m.rec; r != nil {
		r.cmd.Process.Kill()
		os.Remove(r.path)
	}
}

func (m *model) playEnded(msg playEndMsg) {
	p := m.play
	if p == nil || p.gen != msg.gen || p.cmd == nil { // paused, seeking or replaced meanwhile
		return
	}
	m.play = nil
	if msg.err != nil && time.Since(p.started) < time.Second {
		m.flash = "✕ The player failed: " + msg.err.Error()
	}
}

// playKey plays or pauses the selected voice or audio message, after downloading it.
func (m *model) playKey() tea.Cmd {
	sel := m.selected()
	if sel == nil || sel.Media == nil || sel.Media.File == 0 || sel.Kind != "VoiceNote" && sel.Kind != "Audio" {
		m.flash = "✕ Not a voice or audio message"
		return nil
	}
	if p := m.play; p != nil && p.msg == sel.ID {
		if p.cmd != nil {
			m.pausePlay()
			return nil
		}
		if p.dur > 0 && p.at >= p.dur {
			p.at = 0
		}
		return m.startPlay(p)
	}
	m.pausePlay()
	m.play = nil
	id := sel.Media.File
	f := m.c.File(id)
	if !f.Done {
		m.pending[id] = fmt.Sprint("play:", sel.ID)
		m.flash = "Downloading…"
		return run(func() error { return m.c.Download(id, 32) })
	}
	return m.startPlay(&playback{msg: sel.ID, path: f.Path, dur: time.Duration(sel.Media.Duration) * time.Second})
}

// seek moves the playback by d, keeping it paused or playing.
func (m *model) seek(d time.Duration) tea.Cmd {
	p := m.play
	if p == nil {
		return nil
	}
	playing := p.cmd != nil
	m.pausePlay()
	p.at = max(p.at+d, 0)
	if p.dur > 0 {
		p.at = min(p.at, p.dur)
	}
	if playing {
		return m.startPlay(p)
	}
	return nil
}

// playing is the playback of message id, nil if it isn't the one playing or paused.
func (m *model) playing(id int64) *playback {
	if m.play != nil && m.play.msg == id {
		return m.play
	}
	return nil
}

// recording is a voice note being recorded by ffmpeg into path.
type recording struct {
	chat     int64
	path     string
	started  time.Time
	cmd      *exec.Cmd
	stdin    io.WriteCloser
	signaled time.Time // when the chat was last told "recording a voice note"
}

type recordedMsg struct {
	chat    int64
	path    string
	seconds int
	wave    []byte
	err     error
}

// recorderCmd records the default microphone into path as mono OGG Opus, what Telegram
// expects of a voice note.
func recorderCmd(ffmpeg, path string) *exec.Cmd {
	in := []string{"-f", "pulse", "-i", "default"} // PulseAudio, or PipeWire's pulse server
	if runtime.GOOS == "darwin" {
		in = []string{"-f", "avfoundation", "-i", ":default"} // the first use asks for the microphone
	}
	args := append([]string{"-hide_banner", "-loglevel", "error", "-y"}, in...)
	return exec.Command(ffmpeg, append(args, "-ac", "1", "-c:a", "libopus", "-b:a", "32k", path)...)
}

// recordKey starts recording a voice note for the open chat, or stops and sends it.
func (m *model) recordKey() tea.Cmd {
	if r := m.rec; r != nil {
		return m.stopRecording(true)
	}
	if !m.canWrite() {
		m.flash = "✕ Channels are read-only"
		return nil
	}
	ffmpeg, err := exec.LookPath("ffmpeg")
	if err != nil {
		m.flash = "✕ " + noRecorder
		return nil
	}
	dir := filepath.Join(config.CacheDir(), "voice")
	os.MkdirAll(dir, 0o700)
	cleanVoice(dir)
	path := filepath.Join(dir, fmt.Sprintf("%d.ogg", time.Now().UnixNano()))
	cmd := recorderCmd(ffmpeg, path)
	stdin, err := cmd.StdinPipe()
	if err == nil {
		err = cmd.Start()
	}
	if err != nil {
		return func() tea.Msg { return doneMsg{err} }
	}
	m.rec = &recording{chat: m.open, path: path, started: time.Now(), cmd: cmd, stdin: stdin}
	m.insert = true
	return m.signalRecording()
}

// signalRecording tells the chat "recording a voice note", every 5 s while it lasts.
func (m *model) signalRecording() tea.Cmd {
	r := m.rec
	if r == nil || time.Since(r.signaled) < 5*time.Second {
		return nil
	}
	r.signaled = time.Now()
	return run(func() error { return m.c.SendAction("chatActionRecordingVoiceNote") })
}

// stopRecording ends the recording: q makes ffmpeg finish the file. Sent, it goes out with
// its waveform; otherwise it is deleted.
func (m *model) stopRecording(send bool) tea.Cmd {
	r := m.rec
	m.rec = nil
	if !send {
		r.cmd.Process.Kill()
		r.cmd.Wait()
		os.Remove(r.path)
		return nil
	}
	seconds := int(time.Since(r.started).Round(time.Second) / time.Second)
	return func() tea.Msg {
		io.WriteString(r.stdin, "q")
		r.stdin.Close()
		t := time.AfterFunc(5*time.Second, func() { r.cmd.Process.Kill() }) // ffmpeg stuck: give up
		defer t.Stop()
		if err := r.cmd.Wait(); err != nil {
			os.Remove(r.path)
			return recordedMsg{err: fmt.Errorf("recording failed (%v): check the microphone permission", err)}
		}
		return recordedMsg{chat: r.chat, path: r.path, seconds: max(seconds, 1), wave: waveform(r.cmd.Path, r.path)}
	}
}

func (m *model) recorded(msg recordedMsg) tea.Cmd {
	if msg.err != nil {
		m.flash = "✕ " + msg.err.Error()
		return nil
	}
	if msg.chat != m.open { // the chat was left meanwhile
		os.Remove(msg.path)
		return nil
	}
	reply := m.replyTo
	m.replyTo = 0
	return run(func() error { return m.c.SendVoice(msg.path, msg.seconds, msg.wave, reply) })
}

// waveform decodes path to 8 kHz PCM and keeps the loudest sample of each of 100 slices,
// scaled to 0–31: the bars the apps draw.
func waveform(ffmpeg, path string) []byte {
	out, err := exec.Command(ffmpeg, "-loglevel", "error", "-i", path, "-f", "s16le", "-ac", "1", "-ar", "8000", "-").Output()
	n := len(out) / 2
	if err != nil || n == 0 {
		return nil
	}
	peaks, top := make([]int, 100), 1
	for i := range n {
		v := int(int16(uint16(out[2*i]) | uint16(out[2*i+1])<<8))
		v = max(v, -v)
		s := i * 100 / n
		peaks[s] = max(peaks[s], v)
		top = max(top, v)
	}
	wave := make([]byte, 100)
	for i, p := range peaks {
		wave[i] = byte(p * 31 / top)
	}
	return wave
}

// cleanVoice deletes recordings older than a day: TDLib uploads from the file, so it can't
// go right after sending.
func cleanVoice(dir string) {
	es, _ := os.ReadDir(dir)
	for _, e := range es {
		if fi, err := e.Info(); err == nil && time.Since(fi.ModTime()) > 24*time.Hour {
			os.Remove(filepath.Join(dir, e.Name()))
		}
	}
}

// bars draws a waveform in w cells, ▁ to █, the first played cells in the accent.
func bars(wave []byte, w, played int) string {
	if len(wave) == 0 || w <= 0 {
		return ""
	}
	const levels = "▁▂▃▄▅▆▇█"
	var done, rest strings.Builder
	for x := range w {
		lo, hi := x*len(wave)/w, max((x+1)*len(wave)/w, x*len(wave)/w+1)
		v := byte(0)
		for _, s := range wave[lo:min(hi, len(wave))] {
			v = max(v, s)
		}
		r := []rune(levels)[int(v)*7/31]
		if x < played {
			done.WriteRune(r)
		} else {
			rest.WriteRune(r)
		}
	}
	return accent.Render(done.String()) + dim.Render(rest.String())
}
