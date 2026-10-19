package core

import (
	"encoding/json"
	"errors"
	"os/exec"
	"strings"
	"testing"
	"time"
)

// fakeTD records requests and answers getMe with a fake user, loadChats with "all loaded".
type fakeTD struct{ reqs chan map[string]any }

func (f *fakeTD) Send(req map[string]any) (json.RawMessage, error) {
	switch req["@type"] {
	case "loadChats":
		return nil, errors.New("404 Not Found")
	case "disableProxy", "setOption": // sent in the background, not part of the flow checked
		return json.RawMessage(`{"@type":"ok"}`), nil
	}
	f.reqs <- req
	if req["@type"] == "getMe" {
		return json.RawMessage(`{"@type":"user","id":42,"first_name":"Jane","last_name":"Doe","phone_number":"99966123456","usernames":{"editable_username":"jane"}}`), nil
	}
	return json.RawMessage(`{"@type":"ok"}`), nil
}

func (f *fakeTD) next(t *testing.T) map[string]any {
	t.Helper()
	select {
	case r := <-f.reqs:
		return r
	case <-time.After(time.Second):
		t.Fatal("no request sent")
		return nil
	}
}

func state(s string) json.RawMessage {
	return json.RawMessage(`{"@type":"updateAuthorizationState","authorization_state":` + s + `}`)
}

func TestAuthFlow(t *testing.T) {
	td := &fakeTD{reqs: make(chan map[string]any, 10)}
	c := New(td, Params{TestDC: true, DatabaseDir: "/tmp/db", APIID: 1, APIHash: "fakehash", Version: "dev"})

	c.Apply(state(`{"@type":"authorizationStateWaitTdlibParameters"}`))
	if r := td.next(t); r["@type"] != "setTdlibParameters" || r["use_test_dc"] != true || r["api_id"] != 1 {
		t.Fatalf("parameters: %v", r)
	}

	c.Apply(state(`{"@type":"authorizationStateWaitPhoneNumber"}`))
	if k := c.Snapshot().Auth.Kind; k != AuthPhone {
		t.Fatalf("kind %v, want phone", k)
	}

	c.Apply(state(`{"@type":"authorizationStateWaitOtherDeviceConfirmation","link":"tg://login?token=fake"}`))
	if a := c.Snapshot().Auth; a.Kind != AuthQR || a.QRLink != "tg://login?token=fake" {
		t.Fatalf("qr: %+v", a)
	}

	c.Apply(state(`{"@type":"authorizationStateWaitCode","code_info":{"phone_number":"+99966123456",
		"type":{"@type":"authenticationCodeTypeSms","length":5},"timeout":60}}`))
	if a := c.Snapshot().Auth; a.Kind != AuthCode || a.Phone != "+99966123456" || a.CodeVia != "Sms" || a.CodeLength != 5 {
		t.Fatalf("code: %+v", a)
	}

	c.Apply(state(`{"@type":"authorizationStateWaitPassword","password_hint":"pet"}`))
	if a := c.Snapshot().Auth; a.Kind != AuthPassword || a.PasswordHint != "pet" {
		t.Fatalf("password: %+v", a)
	}

	c.Apply(state(`{"@type":"authorizationStateWaitRegistration","terms_of_service":{}}`))
	if a := c.Snapshot().Auth; a.Kind != AuthUnsupported || a.State != "authorizationStateWaitRegistration" {
		t.Fatalf("registration: %+v", a)
	}

	c.Apply(state(`{"@type":"authorizationStateReady"}`))
	if r := td.next(t); r["@type"] != "getMe" {
		t.Fatalf("after ready: %v", r)
	}
	deadline := time.After(time.Second)
	for c.Snapshot().Me == nil {
		select {
		case <-c.Changed():
		case <-deadline:
			t.Fatal("Me not loaded")
		}
	}
	c.Apply(json.RawMessage(`{"@type":"updateUser","user":{"id":42,"first_name":"Janet","usernames":{}}}`))
	c.Apply(json.RawMessage(`{"@type":"updateUser","user":{"id":7,"first_name":"Other","usernames":{}}}`))
	if me := c.Snapshot().Me; me.FirstName != "Janet" {
		t.Fatalf("me: %+v", me)
	}

	if c.LoggedOut() {
		t.Fatal("logged out before logging out")
	}
	c.Apply(state(`{"@type":"authorizationStateLoggingOut"}`))
	c.Apply(state(`{"@type":"authorizationStateClosed"}`))
	if !c.LoggedOut() || c.Snapshot().Auth.Kind != AuthClosed {
		t.Fatal("not logged out and closed")
	}
	c.Close() // returns at once: already closed
}

// The UI must stay replaceable: core knows no UI, and the UI goes through core only.
func TestLayering(t *testing.T) {
	for pkg, banned := range map[string][]string{
		".":     {"/internal/ui", "/internal/td", "charm.land/"},
		"../ui": {"/internal/td"},
	} {
		out, err := exec.Command("go", "list", "-deps", pkg).Output()
		if err != nil {
			t.Fatal(err)
		}
		for _, dep := range strings.Fields(string(out)) {
			for _, b := range banned {
				if strings.Contains(dep, b) {
					t.Errorf("%s imports %s", pkg, dep)
				}
			}
		}
	}
}
