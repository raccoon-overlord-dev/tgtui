// Package td is a thin cgo binding over TDLib's JSON interface (td/telegram/td_json_client.h).
// The authoritative API is td/generate/scheme/td_api.tl at the pinned TDLib commit.
package td

/*
#include <stdlib.h>
#include <td/telegram/td_json_client.h>
*/
import "C"

import (
	"encoding/json"
	"fmt"
	"sync"
	"unsafe"
)

// Error is a TDLib error response, e.g. 400 PHONE_NUMBER_INVALID.
type Error struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

func (e *Error) Error() string { return fmt.Sprintf("%d %s", e.Code, e.Message) }

type Client struct {
	id      C.int
	mu      sync.Mutex
	next    int64
	pending map[int64]chan json.RawMessage // by "@extra"
}

func New() *Client {
	return &Client{id: C.td_create_client_id(), pending: map[int64]chan json.RawMessage{}}
}

// Run starts the receive loop and the client. onUpdate gets every update in order, on the
// loop's goroutine, so it must not block on Send.
func (c *Client) Run(onUpdate func(json.RawMessage)) {
	go func() {
		for {
			raw := C.td_receive(1)
			if raw == nil {
				continue
			}
			b := json.RawMessage(C.GoString(raw))
			var h struct {
				Extra *int64 `json:"@extra"`
			}
			json.Unmarshal(b, &h)
			if h.Extra == nil {
				onUpdate(b)
				continue
			}
			c.mu.Lock()
			ch := c.pending[*h.Extra]
			delete(c.pending, *h.Extra)
			c.mu.Unlock()
			if ch != nil {
				ch <- b
			}
		}
	}()
	c.send(map[string]any{"@type": "getOption", "name": "version"}) // the first request starts the client
}

// Send sends req (it sets req["@extra"]) and waits for its response. A TDLib error
// response comes back as *Error.
func (c *Client) Send(req map[string]any) (json.RawMessage, error) {
	ch := make(chan json.RawMessage, 1)
	c.mu.Lock()
	c.next++
	id := c.next
	c.pending[id] = ch
	c.mu.Unlock()
	req["@extra"] = id
	c.send(req)
	res := <-ch
	var e struct {
		Type string `json:"@type"`
		Error
	}
	if json.Unmarshal(res, &e) == nil && e.Type == "error" {
		return res, &e.Error
	}
	return res, nil
}

func (c *Client) send(req map[string]any) {
	s := cstring(req)
	defer C.free(unsafe.Pointer(s))
	C.td_send(c.id, s)
}

// Execute runs one of the requests TDLib can answer synchronously (logging, parseMarkdown...).
func Execute(req map[string]any) json.RawMessage {
	s := cstring(req)
	defer C.free(unsafe.Pointer(s))
	return json.RawMessage(C.GoString(C.td_execute(s)))
}

func cstring(req map[string]any) *C.char {
	b, _ := json.Marshal(req)
	return C.CString(string(b))
}
