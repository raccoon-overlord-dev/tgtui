package config

import "testing"

func TestCredentials(t *testing.T) {
	c := Default()
	t.Setenv("TGTUI_API_ID", "")
	t.Setenv("TGTUI_API_HASH", "")
	if _, _, err := c.Credentials("", ""); err == nil {
		t.Fatal("no credentials anywhere: want an error")
	}
	if id, h, _ := c.Credentials("1", "embedded"); id != 1 || h != "embedded" {
		t.Fatalf("embedded: %d %s", id, h)
	}
	c.Telegram.APIID, c.Telegram.APIHash = 2, "config"
	if id, h, _ := c.Credentials("1", "embedded"); id != 2 || h != "config" {
		t.Fatalf("config over embedded: %d %s", id, h)
	}
	t.Setenv("TGTUI_API_ID", "3")
	if id, h, _ := c.Credentials("1", "embedded"); id != 2 || h != "config" {
		t.Fatalf("env id without hash must not mix with config: %d %s", id, h)
	}
	t.Setenv("TGTUI_API_HASH", "env")
	if id, h, _ := c.Credentials("1", "embedded"); id != 3 || h != "env" {
		t.Fatalf("env over config: %d %s", id, h)
	}
}
