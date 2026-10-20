package ui

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/charmbracelet/x/ansi"

	"github.com/raccoon-overlord-dev/tgtui/internal/core"
)

// A voice note plays, pauses, seeks and resumes through the player found on PATH (a stub
// here), and its block keeps its width.
func TestVoicePlayback(t *testing.T) {
	bin := t.TempDir()
	if err := os.WriteFile(filepath.Join(bin, "mpv"), []byte("#!/bin/sh\nsleep 30\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin)
	m := demo(120, 40)
	defer m.stopAudio()
	m.c.Apply(json.RawMessage(`{"@type":"updateFile","file":{"id":9,"size":100,"local":{"path":"/tmp/v.ogg","is_downloading_completed":true}}}`))
	voice := core.Message{ID: 5000, ChatID: 1, Kind: "VoiceNote", Date: 1_790_300_000,
		Media: &core.Media{File: 9, Duration: 42, Wave: []byte{0, 8, 31, 16, 4, 20}}}
	m.win.Messages = append(m.win.Messages, voice)
	m.cur = voice.ID

	press(m, "alt+p")
	if m.play == nil || m.play.cmd == nil {
		t.Fatalf("alt+p didn't start the player (%q)", m.flash)
	}
	press(m, "alt+p")
	if m.play == nil || m.play.cmd != nil {
		t.Fatal("alt+p again should pause")
	}
	press(m, "alt+>")
	if m.play.at < 5*time.Second {
		t.Fatalf("seek: at %v", m.play.at)
	}
	press(m, "alt+p")
	if m.play.cmd == nil {
		t.Fatal("alt+p should resume")
	}
	if w := ansi.StringWidth(bars(voice.Media.Wave, 24, 7)); w != 24 {
		t.Fatalf("waveform is %d cells", w)
	}

	t.Setenv("PATH", t.TempDir()) // no player: advice, not an error
	m.stopAudio()
	press(m, "alt+p")
	if m.play != nil || m.flash != "✕ "+noPlayer {
		t.Fatalf("without a player: %q", m.flash)
	}
}
