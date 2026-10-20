// Package config loads ~/.config/tgtui/config.toml and resolves tgtui's XDG paths.
package config

import (
	_ "embed"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"

	"github.com/BurntSushi/toml"
)

//go:embed default.toml
var defaultTOML string

type Config struct {
	Telegram struct {
		APIID   int    `toml:"api_id"`
		APIHash string `toml:"api_hash"`
	} `toml:"telegram"`
	Appearance struct {
		Theme      string `toml:"theme"`
		Background string `toml:"background"`  // theme | terminal: paint the theme's background, or keep the terminal's
		TimeFormat string `toml:"time_format"` // 24h | 12h
		Avatars    bool   `toml:"avatars"`     // profile pictures in the list, the chat title and group sender lines
	} `toml:"appearance"`
	Notifications struct {
		Mode    string `toml:"mode"` // auto | off | terminal | system
		Preview bool   `toml:"preview"`
		Sound   bool   `toml:"sound"`
	} `toml:"notifications"`
	Media struct {
		Images string `toml:"images"` // auto | kitty | blocks | off
		Attach string `toml:"attach"` // dialog | path: how files to send are picked
	} `toml:"media"`
	Keys struct {
		Keymap string `toml:"keymap"`  // vim+telegram | telegram
		OpenIn string `toml:"open_in"` // insert | normal: the mode a chat opens in
		Layout string `toml:"layout"`  // us | it: which keys the punctuation shortcuts use
	} `toml:"keys"`
	Updates struct {
		Check bool `toml:"check"`
	} `toml:"updates"`
	Proxy Proxy `toml:"proxy"`
}

// Proxy is the one proxy TDLib connects through (decision D7: config file only).
type Proxy struct {
	Type     string `toml:"type"` // "" (none) | socks5 | http | mtproto
	Server   string `toml:"server"`
	Port     int    `toml:"port"`
	Username string `toml:"username"`
	Password string `toml:"password"`
	Secret   string `toml:"secret"` // mtproto
}

// Dir is $XDG_CONFIG_HOME/tgtui, or ~/.config/tgtui (macOS included).
func Dir() string { return xdgDir("XDG_CONFIG_HOME", ".config") }

// DataDir is $XDG_DATA_HOME/tgtui, or ~/.local/share/tgtui: the TDLib database.
func DataDir() string { return xdgDir("XDG_DATA_HOME", ".local/share") }

// CacheDir is $XDG_CACHE_HOME/tgtui, or ~/.cache/tgtui.
func CacheDir() string { return xdgDir("XDG_CACHE_HOME", ".cache") }

func xdgDir(env, fallback string) string {
	if d := os.Getenv(env); d != "" {
		return filepath.Join(d, "tgtui")
	}
	home, _ := os.UserHomeDir()
	return filepath.Join(home, fallback, "tgtui")
}

// Path is the config file; --config overrides it.
var Path = filepath.Join(Dir(), "config.toml")

func Default() *Config {
	var c Config
	if _, err := toml.Decode(defaultTOML, &c); err != nil {
		panic("config: bad embedded default.toml: " + err.Error())
	}
	return &c
}

// Load reads Path over the defaults. On first run it writes the commented default file.
// The file may hold an api_hash, so it is private to the user.
func Load() (*Config, error) {
	c := Default()
	_, err := toml.DecodeFile(Path, c)
	if errors.Is(err, os.ErrNotExist) {
		if err := os.MkdirAll(filepath.Dir(Path), 0o700); err != nil {
			return c, err
		}
		return c, os.WriteFile(Path, []byte(defaultTOML), 0o600)
	}
	return c, err
}

// Credentials picks the Telegram api_id / api_hash: the TGTUI_API_ID / TGTUI_API_HASH
// environment, then the config file, then the values embedded in release builds.
// The first source that sets both wins.
func (c *Config) Credentials(embeddedID, embeddedHash string) (int, string, error) {
	cfgID := ""
	if c.Telegram.APIID != 0 {
		cfgID = strconv.Itoa(c.Telegram.APIID)
	}
	for _, s := range [][2]string{
		{os.Getenv("TGTUI_API_ID"), os.Getenv("TGTUI_API_HASH")},
		{cfgID, c.Telegram.APIHash},
		{embeddedID, embeddedHash},
	} {
		if s[0] == "" || s[1] == "" {
			continue
		}
		id, err := strconv.Atoi(s[0])
		if err != nil {
			return 0, "", fmt.Errorf("api_id %q is not a number", s[0])
		}
		return id, s[1], nil
	}
	return 0, "", fmt.Errorf("no Telegram API credentials: get an api_id and api_hash at https://my.telegram.org, "+
		"then set TGTUI_API_ID / TGTUI_API_HASH or [telegram] in %s", Path)
}

// Save overwrites Path with c. The comments of the default file are not kept.
func Save(c *Config) error {
	f, err := os.OpenFile(Path, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0o600)
	if err != nil {
		return err
	}
	if err := toml.NewEncoder(f).Encode(c); err != nil {
		f.Close()
		return err
	}
	return f.Close()
}
