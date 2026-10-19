package keychain

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/zalando/go-keyring"
)

func TestDBKey(t *testing.T) {
	keyring.MockInit()
	dir := t.TempDir()

	k, warn := DBKey("a", dir)
	if len(k) != 32 || warn != "" {
		t.Fatalf("new database: key %d bytes, warn %q", len(k), warn)
	}
	if again, _ := DBKey("a", dir); string(again) != string(k) {
		t.Fatal("key changed between runs")
	}

	// A database that exists without a stored key was made unencrypted: keep it that way.
	os.WriteFile(filepath.Join(dir, "td_test.binlog"), nil, 0o600)
	if k, warn := DBKey("b", dir); k != nil || warn != "" {
		t.Fatalf("old unencrypted database: key %v, warn %q", k, warn)
	}

	if err := Delete("a"); err != nil {
		t.Fatal(err)
	}
	if err := Delete("a"); err != nil {
		t.Fatal("deleting a missing key:", err)
	}
}
