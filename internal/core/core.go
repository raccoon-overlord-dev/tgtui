// Package core owns tgtui's Telegram state. It applies TDLib updates, offers the commands
// the UI needs, and hands out copies (Snapshot, Chats, Window) for the UI to show. It knows
// nothing about the UI or about how TDLib is linked (TD is satisfied by internal/td).
package core

import (
	"encoding/json"
	"runtime"
	"strings"
	"sync"
	"time"

	"github.com/raccoon-overlord-dev/tgtui/internal/config"
)

// TD sends one TDLib request and waits for its response.
type TD interface {
	Send(req map[string]any) (json.RawMessage, error)
}

type Params struct {
	TestDC      bool
	DatabaseDir string
	DBKey       []byte // nil: unencrypted database
	APIID       int
	APIHash     string
	Version     string
	Proxy       config.Proxy
}

type AuthKind int

const (
	AuthStarting    AuthKind = iota // TDLib not ready yet
	AuthPhone                       // waiting for the phone number
	AuthQR                          // waiting for the QR code to be scanned on another device
	AuthCode                        // waiting for the login code
	AuthPassword                    // waiting for the 2FA cloud password
	AuthReady                       // signed in
	AuthUnsupported                 // a step tgtui doesn't handle (registration, email setup...): State says which
	AuthClosing                     // logging out or closing
	AuthClosed
)

type Auth struct {
	Kind         AuthKind
	State        string // TDLib's authorizationState* type
	QRLink       string // AuthQR: tg://login?token=…, refreshed by TDLib every ~30 s
	Phone        string // AuthCode: where the code went
	CodeVia      string // AuthCode: TelegramMessage | Sms | Call | …
	CodeLength   int    // AuthCode: 0 when unknown
	PasswordHint string // AuthPassword
}

type Me struct {
	ID        int64
	FirstName string
	LastName  string
	Username  string
	Phone     string
}

// List is a chat list as the official apps show it: "All chats" (Main) and the user's folders.
type List struct {
	ID   ChatList
	Name string // "" for Main
}

// Snapshot is the small part of the state; chats and messages have their own getters.
type Snapshot struct {
	Auth  Auth
	Me    *Me    // nil until signed in
	Err   string // last failure of a request core sent on its own (e.g. setTdlibParameters)
	Conn  string // WaitingForNetwork | ConnectingToProxy | Connecting | Updating | Ready
	Lists []List // Main and the folders, in the user's order (the archive is not among them)
	TDLib string // TDLib's version
}

type Core struct {
	td      TD
	params  Params
	changed chan struct{}
	closed  chan struct{}
	once    sync.Once

	// Everything below is guarded by mu: TDLib's receive loop applies updates while UI
	// commands merge their results.
	mu         sync.Mutex
	snap       Snapshot
	loggingOut bool
	folders    []List
	mainPos    int
	users      map[int64]string // display names, for message senders
	usernames  map[int64]string
	userPhotos map[int64]Photo
	unreadIn   map[ChatList]int // unread unmuted chats of each list, from updateUnreadChatCount
	chats      map[int64]*Chat  // values are replaced, never changed, so copies can share them
	loaded     map[ChatList]bool
	win        Window // the open chat
	viewed     int64  // newest message of win already passed to viewMessages
	files      map[int32]File
	notes      []Notification
	scopeMute  map[ChatKind]int64    // mute_for of each chat kind's default settings
	replies    map[[2]int64]*Message // replied messages outside the window, by chat and id; nil value: being fetched
}

func New(td TD, p Params) *Core {
	return &Core{td: td, params: p, changed: make(chan struct{}, 1), closed: make(chan struct{}),
		users: map[int64]string{}, usernames: map[int64]string{}, userPhotos: map[int64]Photo{}, unreadIn: map[ChatList]int{}, chats: map[int64]*Chat{}, loaded: map[ChatList]bool{}, files: map[int32]File{}, scopeMute: map[ChatKind]int64{}, replies: map[[2]int64]*Message{}}
}

// Changed receives after the state changed. Bursts collapse into one receive.
func (c *Core) Changed() <-chan struct{} { return c.changed }

func (c *Core) notify() {
	select {
	case c.changed <- struct{}{}:
	default:
	}
}

func (c *Core) Snapshot() Snapshot {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.snap
}

// locked runs f under the lock, then notifies.
func (c *Core) locked(f func()) {
	c.mu.Lock()
	f()
	c.mu.Unlock()
	c.notify()
}

// Apply handles one TDLib update. TDLib's receive loop calls it, so requests it needs
// are sent from new goroutines.
func (c *Core) Apply(raw json.RawMessage) {
	var u update
	if json.Unmarshal(raw, &u) != nil {
		return
	}
	c.locked(func() {
		switch u.Type {
		case "updateAuthorizationState":
			c.applyAuth(u.AuthorizationState)
		case "updateUser":
			me := parseUser(u.User)
			c.users[me.ID] = strings.TrimSpace(me.FirstName + " " + me.LastName)
			c.usernames[me.ID] = me.Username
			var p struct {
				Photo *tdChatPhoto `json:"profile_photo"`
			}
			json.Unmarshal(u.User, &p)
			c.userPhotos[me.ID] = c.photo(p.Photo)
			if c.snap.Me != nil && c.snap.Me.ID == me.ID {
				c.snap.Me = me
			}
		case "updateFile":
			if u.File != nil {
				c.files[u.File.ID] = fileOf(*u.File)
			}
		case "updateOption":
			if u.Name == "version" {
				json.Unmarshal(u.Value.Value, &c.snap.TDLib)
			}
		case "updateUnreadChatCount":
			c.unreadIn[u.ChatList.list()] = u.UnreadUnmuted
		case "updateConnectionState":
			c.snap.Conn = strings.TrimPrefix(u.State.Type, "connectionState")
		case "updateChatFolders":
			c.folders, c.mainPos = nil, u.MainChatListPosition
			for _, f := range u.ChatFolders {
				c.folders = append(c.folders, List{FolderList(f.ID), f.Name.Text.Text})
			}
			c.snap.Lists = c.lists()
		default:
			c.applyChat(&u)
			c.applyMessage(&u)
			c.applyNotifications(&u)
			c.applyMute(&u)
		}
	})
}

// update holds the fields of every update core reads; each update type fills its own.
type update struct {
	Type               string          `json:"@type"`
	AuthorizationState json.RawMessage `json:"authorization_state"`
	User               json.RawMessage `json:"user"`
	State              struct {
		Type string `json:"@type"`
	} `json:"state"`
	ChatFolders []struct {
		ID   int32 `json:"id"`
		Name struct {
			Text formattedText `json:"text"`
		} `json:"name"`
	} `json:"chat_folders"`
	MainChatListPosition int `json:"main_chat_list_position"`

	Chat             *tdChat         `json:"chat"`
	ChatID           int64           `json:"chat_id"`
	Title            string          `json:"title"`
	Photo            *tdChatPhoto    `json:"photo"`                // updateChatPhoto
	ChatList         tdList          `json:"chat_list"`            // updateUnreadChatCount
	UnreadUnmuted    int             `json:"unread_unmuted_count"` // updateUnreadChatCount
	Position         *tdPosition     `json:"position"`
	Positions        []tdPosition    `json:"positions"`
	LastMessage      *tdMessage      `json:"last_message"`
	LastReadInbox    int64           `json:"last_read_inbox_message_id"`
	LastReadOutbox   int64           `json:"last_read_outbox_message_id"`
	UnreadCount      int             `json:"unread_count"`
	UnreadMentions   int             `json:"unread_mention_count"`
	IsMarkedAsUnread bool            `json:"is_marked_as_unread"`
	Message          *tdMessage      `json:"message"`
	OldMessageID     int64           `json:"old_message_id"`
	MessageID        int64           `json:"message_id"`
	MessageIDs       []int64         `json:"message_ids"`
	NewContent       json.RawMessage `json:"new_content"`
	EditDate         int64           `json:"edit_date"`
	IsPermanent      bool            `json:"is_permanent"`
	File             *tdFile         `json:"file"`
	SenderID         struct {
		Type   string `json:"@type"`
		UserID int64  `json:"user_id"`
		ChatID int64  `json:"chat_id"`
	} `json:"sender_id"`
	Action struct {
		Type string `json:"@type"`
	} `json:"action"`

	AddedNotifications json.RawMessage `json:"added_notifications"`
	GroupType          struct {
		Type string `json:"@type"`
	} `json:"type"`
	NotificationSettings json.RawMessage `json:"notification_settings"`
	Scope                struct {
		Type string `json:"@type"`
	} `json:"scope"`
	Name  string `json:"name"` // updateOption
	Value struct {
		Value json.RawMessage `json:"value"`
	} `json:"value"`
}

type formattedText struct {
	Text     string          `json:"text"`
	Entities json.RawMessage `json:"entities"`
}

// lists puts Main among the folders where the user placed it.
func (c *Core) lists() []List {
	pos := min(max(c.mainPos, 0), len(c.folders))
	out := append([]List{}, c.folders[:pos]...)
	out = append(out, List{ID: Main})
	return append(out, c.folders[pos:]...)
}

func (c *Core) applyAuth(raw json.RawMessage) {
	var st struct {
		Type     string `json:"@type"`
		Link     string `json:"link"`
		Hint     string `json:"password_hint"`
		CodeInfo struct {
			Phone string `json:"phone_number"`
			Type  struct {
				Type   string `json:"@type"`
				Length int    `json:"length"`
			} `json:"type"`
		} `json:"code_info"`
	}
	json.Unmarshal(raw, &st)
	a := Auth{State: st.Type}
	switch st.Type {
	case "authorizationStateWaitTdlibParameters":
		go func() {
			if _, err := c.request(c.tdlibParameters()); err == nil {
				c.setProxy(c.params.Proxy)
			}
		}()
	case "authorizationStateWaitPhoneNumber":
		a.Kind = AuthPhone
	case "authorizationStateWaitOtherDeviceConfirmation":
		a.Kind, a.QRLink = AuthQR, st.Link
	case "authorizationStateWaitCode":
		a.Kind, a.Phone = AuthCode, st.CodeInfo.Phone
		a.CodeVia = strings.TrimPrefix(st.CodeInfo.Type.Type, "authenticationCodeType")
		a.CodeLength = st.CodeInfo.Type.Length
	case "authorizationStateWaitPassword":
		a.Kind, a.PasswordHint = AuthPassword, st.Hint
	case "authorizationStateReady":
		a.Kind = AuthReady
		go c.loadMe()
		go c.enableNotifications()
		c.loadList(Main)
	case "authorizationStateLoggingOut":
		a.Kind = AuthClosing
		c.loggingOut = true // also when the session was terminated from another device
	case "authorizationStateClosing":
		a.Kind = AuthClosing
	case "authorizationStateClosed":
		a.Kind = AuthClosed
		defer c.once.Do(func() { close(c.closed) })
	default:
		a.Kind = AuthUnsupported
	}
	c.snap.Auth = a
}

func (c *Core) tdlibParameters() map[string]any {
	p := c.params
	return map[string]any{
		"@type":                   "setTdlibParameters",
		"use_test_dc":             p.TestDC,
		"database_directory":      p.DatabaseDir,
		"database_encryption_key": p.DBKey, // []byte marshals as base64, as TDLib wants bytes
		"use_file_database":       true,
		"use_chat_info_database":  true,
		"use_message_database":    true, // warm start shows cached chats before the network answers
		"api_id":                  p.APIID,
		"api_hash":                p.APIHash,
		"system_language_code":    "en",
		"device_model":            map[string]string{"darwin": "macOS", "linux": "Linux"}[runtime.GOOS],
		"application_version":     p.Version,
	}
}

// request sends a request core needs on its own; a failure is kept in Snapshot.Err.
func (c *Core) request(req map[string]any) (json.RawMessage, error) {
	res, err := c.td.Send(req)
	if err != nil {
		c.locked(func() { c.snap.Err = req["@type"].(string) + ": " + err.Error() })
	}
	return res, err
}

func (c *Core) loadMe() {
	if res, err := c.request(map[string]any{"@type": "getMe"}); err == nil {
		c.locked(func() { c.snap.Me = parseUser(res) })
	}
}

func parseUser(raw json.RawMessage) *Me {
	var u struct {
		ID        int64  `json:"id"`
		FirstName string `json:"first_name"`
		LastName  string `json:"last_name"`
		Phone     string `json:"phone_number"`
		Usernames struct {
			Active   []string `json:"active_usernames"`
			Editable string   `json:"editable_username"`
		} `json:"usernames"`
	}
	json.Unmarshal(raw, &u)
	name := u.Usernames.Editable
	if len(u.Usernames.Active) > 0 {
		name = u.Usernames.Active[0]
	}
	return &Me{ID: u.ID, FirstName: u.FirstName, LastName: u.LastName, Username: name, Phone: u.Phone}
}

func (c *Core) send(req map[string]any) error {
	_, err := c.td.Send(req)
	return err
}

// SetPhone starts phone sign-in. It also works while a QR code, a code or a password is
// being asked, to switch to another number.
func (c *Core) SetPhone(phone string) error {
	return c.send(map[string]any{"@type": "setAuthenticationPhoneNumber", "phone_number": phone})
}

// RequestQR switches sign-in to a QR code to scan from a device already signed in.
func (c *Core) RequestQR() error {
	return c.send(map[string]any{"@type": "requestQrCodeAuthentication", "other_user_ids": []int64{}})
}

func (c *Core) CheckCode(code string) error {
	return c.send(map[string]any{"@type": "checkAuthenticationCode", "code": code})
}

func (c *Core) CheckPassword(pw string) error {
	return c.send(map[string]any{"@type": "checkAuthenticationPassword", "password": pw})
}

// LogOut signs out; TDLib then deletes its database and closes.
func (c *Core) LogOut() error { return c.send(map[string]any{"@type": "logOut"}) }

// LoggedOut reports whether the session ended by logging out (here or from another device).
func (c *Core) LoggedOut() bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.loggingOut
}

// Close shuts TDLib down so its database is flushed, waiting at most a few seconds.
func (c *Core) Close() {
	select {
	case <-c.closed:
		return
	default:
	}
	go c.td.Send(map[string]any{"@type": "close"})
	select {
	case <-c.closed:
	case <-time.After(5 * time.Second):
	}
}
