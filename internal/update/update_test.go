package update

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

func TestNewer(t *testing.T) {
	for _, c := range []struct {
		tag, cur string
		want     bool
	}{
		{"v0.4.0", "0.3.0", true}, {"v0.3.1", "v0.3.0", true}, {"v0.10.0", "0.9.9", true},
		{"v0.3.0", "0.3.0", false}, {"v0.2.9", "0.3.0", false}, {"v1.0.0", "dev", false},
	} {
		if Newer(c.tag, c.cur) != c.want {
			t.Errorf("Newer(%q, %q) != %v", c.tag, c.cur, c.want)
		}
	}
}

// A fake GitHub: /latest redirects to the tag. The second check within a day uses the stamp.
func TestCheck(t *testing.T) {
	t.Setenv("XDG_CACHE_HOME", t.TempDir())
	hits := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits++
		http.Redirect(w, r, "/tag/v9.9.9", http.StatusFound)
	}))
	defer srv.Close()
	Releases = srv.URL
	ctx := context.Background()
	for _, force := range []bool{false, false, true} {
		if tag, err := Check(ctx, force); err != nil || tag != "v9.9.9" {
			t.Fatalf("check: %q %v", tag, err)
		}
	}
	if hits != 2 {
		t.Fatalf("%d requests, want 2 (one cached)", hits)
	}
}

// Apply replaces the binary only with a release whose checksum matches.
func TestApply(t *testing.T) {
	var tgz bytes.Buffer
	gz := gzip.NewWriter(&tgz)
	tw := tar.NewWriter(gz)
	bin := []byte("#!/bin/sh\necho new\n")
	tw.WriteHeader(&tar.Header{Name: "tgtui", Mode: 0o755, Size: int64(len(bin))})
	tw.Write(bin)
	tw.Close()
	gz.Close()
	name := fmt.Sprintf("tgtui_%s_%s.tar.gz", runtime.GOOS, runtime.GOARCH)
	sums := fmt.Sprintf("%x  %s\n", sha256.Sum256(tgz.Bytes()), name)
	mux := http.NewServeMux()
	mux.HandleFunc("/download/v9.9.9/checksums.txt", func(w http.ResponseWriter, r *http.Request) { w.Write([]byte(sums)) })
	mux.HandleFunc("/download/v9.9.9/"+name, func(w http.ResponseWriter, r *http.Request) { w.Write(tgz.Bytes()) })
	srv := httptest.NewServer(mux)
	defer srv.Close()
	Releases = srv.URL

	ctx := context.Background()
	exe := filepath.Join(t.TempDir(), "tgtui")
	os.WriteFile(exe, []byte("old"), 0o755)
	if err := Apply(ctx, "v9.9.9", exe); err != nil {
		t.Fatal(err)
	}
	if got, _ := os.ReadFile(exe); !bytes.Equal(got, bin) {
		t.Fatalf("binary not replaced: %q", got)
	}
	sums = fmt.Sprintf("%064d  %s\n", 0, name) // tampered
	os.WriteFile(exe, []byte("old"), 0o755)
	if err := Apply(ctx, "v9.9.9", exe); err == nil {
		t.Fatal("bad checksum accepted")
	}
	if got, _ := os.ReadFile(exe); string(got) != "old" {
		t.Fatal("binary changed despite bad checksum")
	}
	if ents, _ := os.ReadDir(filepath.Dir(exe)); len(ents) != 1 {
		t.Fatalf("temp file left behind: %v", ents)
	}
}
