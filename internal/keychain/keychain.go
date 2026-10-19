// Package keychain keeps the TDLib database encryption key in the OS keychain
// (macOS Keychain, Linux Secret Service).
package keychain

import (
	"crypto/rand"
	"encoding/base64"
	"errors"
	"path/filepath"
	"time"

	"github.com/zalando/go-keyring"
)

const service = "tgtui"

// DBKey returns the encryption key of the TDLib database in dir, stored under account.
// A new database gets a new random key. It returns nil (no encryption) for a database
// created without a key, and nil plus a warning when no keychain is usable.
func DBKey(account, dir string) ([]byte, string) {
	s, err := timeout(func() (string, error) { return keyring.Get(service, account) })
	if err == nil {
		if k, err := base64.StdEncoding.DecodeString(s); err == nil {
			return k, ""
		}
		return nil, "keychain: unreadable database key"
	}
	if !errors.Is(err, keyring.ErrNotFound) {
		return nil, "database not encrypted: no keychain (" + err.Error() + ")"
	}
	if old, _ := filepath.Glob(filepath.Join(dir, "td*.binlog")); len(old) > 0 {
		return nil, "" // created unencrypted, while no keychain was usable
	}
	k := make([]byte, 32)
	rand.Read(k)
	if _, err := timeout(func() (string, error) { return "", keyring.Set(service, account, base64.StdEncoding.EncodeToString(k)) }); err != nil {
		return nil, "database not encrypted: no keychain (" + err.Error() + ")"
	}
	return k, ""
}

// Delete removes the key after logging out (TDLib has deleted the database).
func Delete(account string) error {
	_, err := timeout(func() (string, error) { return "", keyring.Delete(service, account) })
	if errors.Is(err, keyring.ErrNotFound) {
		return nil
	}
	return err
}

// timeout gives up on a keychain call after a few seconds: on Linux a Secret Service that
// D-Bus has to activate, or a locked keyring waiting for its prompt, can block for long.
// ponytail: the call keeps running in the background; fine for a once-per-start lookup.
func timeout(f func() (string, error)) (string, error) {
	type res struct {
		s   string
		err error
	}
	ch := make(chan res, 1)
	go func() { s, err := f(); ch <- res{s, err} }()
	select {
	case r := <-ch:
		return r.s, r.err
	case <-time.After(5 * time.Second):
		return "", errors.New("timed out")
	}
}
