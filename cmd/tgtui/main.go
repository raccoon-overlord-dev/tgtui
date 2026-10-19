package main

import (
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/raccoon-overlord-dev/tgtui/internal/config"
	"github.com/raccoon-overlord-dev/tgtui/internal/core"
	"github.com/raccoon-overlord-dev/tgtui/internal/keychain"
	"github.com/raccoon-overlord-dev/tgtui/internal/td"
	"github.com/raccoon-overlord-dev/tgtui/internal/ui"
)

// Set at build time: go build -ldflags "-X main.version=v0.1.0 -X main.apiID=… -X main.apiHash=…".
// Release builds embed the API credentials from a CI secret; they never live in the repo.
var (
	version = "dev"
	apiID   string
	apiHash string
)

func main() {
	showVersion := flag.Bool("version", false, "print version and exit")
	testDC := flag.Bool("test-dc", false, "use Telegram's test servers (separate account and database)")
	startupTime := flag.Bool("startup-time", false, "print the time to the first chat list and exit (warm start check)")
	flag.StringVar(&config.Path, "config", config.Path, "config file")
	flag.Parse()

	if *showVersion {
		fmt.Println("tgtui", version)
		return
	}
	if err := run(*testDC, *startupTime); err != nil {
		fmt.Fprintln(os.Stderr, "tgtui:", err)
		os.Exit(1)
	}
}

func run(testDC, startupTime bool) error {
	start := time.Now()
	cfg, err := config.Load()
	if err != nil {
		return fmt.Errorf("config: %w", err)
	}
	id, hash, err := cfg.Credentials(apiID, apiHash)
	if err != nil {
		return err
	}
	account, dbDir := "db-key", filepath.Join(config.DataDir(), "td")
	if testDC {
		account, dbDir = "db-key-test", dbDir+"-test"
	}
	for _, d := range []string{dbDir, config.CacheDir()} {
		if err := os.MkdirAll(d, 0o700); err != nil {
			return err
		}
	}
	key, warn := keychain.DBKey(account, dbDir)

	// TDLib logs to stderr by default, which would draw over the TUI.
	td.Execute(map[string]any{"@type": "setLogVerbosityLevel", "new_verbosity_level": 2})
	td.Execute(map[string]any{"@type": "setLogStream", "log_stream": map[string]any{
		"@type": "logStreamFile", "path": filepath.Join(config.CacheDir(), "tdlib.log"), "max_file_size": 10 << 20}})

	cl := td.New()
	c := core.New(cl, core.Params{TestDC: testDC, DatabaseDir: dbDir, DBKey: key, APIID: id, APIHash: hash, Version: version, Proxy: cfg.Proxy})
	cl.Run(c.Apply)
	err = ui.Run(c, ui.Options{Config: cfg, DataDir: config.DataDir(), Version: version, Warn: warn, TestDC: testDC, ExitWhenListed: startupTime})
	listed := time.Since(start)
	c.Close()
	if startupTime {
		fmt.Println("chat list after", listed.Round(time.Millisecond))
	}
	if c.LoggedOut() {
		if err := keychain.Delete(account); err != nil {
			fmt.Fprintln(os.Stderr, "tgtui: keychain:", err)
		}
		fmt.Println("Logged out.")
	}
	return err
}
