package core

import (
	"encoding/json"
	"strings"
)

// Format is a set of text styles; the UI decides how each looks.
type Format uint16

const (
	Bold Format = 1 << iota
	Italic
	Underline
	Strike
	Spoiler
	Code    // inline code and pre blocks
	Link    // Span.URL is the target
	Mention // @mentions, hashtags, bot commands
	Quote   // block quote
)

// Span styles Text[From:To] (byte offsets). Spans may overlap; their formats add up.
type Span struct {
	From, To int
	Format   Format
	URL      string
}

type tdEntity struct {
	Offset int `json:"offset"` // UTF-16 code units, as all TDLib text offsets
	Length int `json:"length"`
	Type   struct {
		Type string `json:"@type"`
		URL  string `json:"url"`
	} `json:"type"`
}

var entityFormats = map[string]Format{
	"Bold": Bold, "Italic": Italic, "Underline": Underline, "Strikethrough": Strike, "Spoiler": Spoiler,
	"Code": Code, "Pre": Code, "PreCode": Code, "Url": Link, "TextUrl": Link, "EmailAddress": Link,
	"Mention": Mention, "MentionName": Mention, "Hashtag": Mention, "Cashtag": Mention, "BotCommand": Mention,
	"BlockQuote": Quote, "ExpandableBlockQuote": Quote,
}

// spans converts TDLib entities of text to Spans.
func spans(text string, raw json.RawMessage) []Span {
	var ents []tdEntity
	if json.Unmarshal(raw, &ents) != nil || len(ents) == 0 {
		return nil
	}
	at := make([]int, 0, len(text)+1) // UTF-16 offset → byte offset
	for b, r := range text {
		at = append(at, b)
		if r >= 0x10000 { // a surrogate pair: two UTF-16 units
			at = append(at, b)
		}
	}
	at = append(at, len(text))
	byteAt := func(u int) int { return at[min(max(u, 0), len(at)-1)] }
	var out []Span
	for _, e := range ents {
		kind := strings.TrimPrefix(e.Type.Type, "textEntityType")
		f, ok := entityFormats[kind]
		if !ok {
			continue
		}
		s := Span{From: byteAt(e.Offset), To: byteAt(e.Offset + e.Length), Format: f, URL: e.Type.URL}
		switch kind {
		case "Url":
			s.URL = text[s.From:s.To]
			if !strings.Contains(s.URL, "://") {
				s.URL = "https://" + s.URL
			}
		case "EmailAddress":
			s.URL = "mailto:" + text[s.From:s.To]
		}
		out = append(out, s)
	}
	return out
}
