package core

import (
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
)

// File is a TDLib file and how much of it is on disk.
type File struct {
	ID          int32
	Size        int64
	Path        string // local path, complete when Done
	Done        bool
	Downloading bool
	Downloaded  int64
}

// Media is the file part of a message: what to open or save, and what to preview.
type Media struct {
	File     int32  // the file itself (largest photo size, video, document…)
	Thumb    int32  // a JPEG / PNG / WebP to draw as preview, 0 for none
	Mini     []byte // tiny blurry JPEG that TDLib sends inline, drawn until Thumb is downloaded
	W, H     int    // the preview's size in pixels
	Name     string
	Size     int64
	Duration int // seconds
}

// LinkPreview is the card TDLib attaches to a text message with a link.
type LinkPreview struct {
	URL, Site, Title, Description string
}

type tdFile struct {
	ID       int32 `json:"id"`
	Size     int64 `json:"size"`
	Expected int64 `json:"expected_size"`
	Local    struct {
		Path        string `json:"path"`
		Downloading bool   `json:"is_downloading_active"`
		Done        bool   `json:"is_downloading_completed"`
		Downloaded  int64  `json:"downloaded_size"`
	} `json:"local"`
}

type tdMini struct {
	Width  int    `json:"width"`
	Height int    `json:"height"`
	Data   []byte `json:"data"` // base64 in JSON, as all TDLib bytes
}

type tdThumb struct {
	Format struct {
		Type string `json:"@type"`
	} `json:"format"`
	Width  int    `json:"width"`
	Height int    `json:"height"`
	File   tdFile `json:"file"`
}

func drawable(format string) bool {
	switch format {
	case "thumbnailFormatJpeg", "thumbnailFormatPng", "thumbnailFormatWebp":
		return true
	}
	return false
}

// tdContent holds the media fields of every content type core reads.
type tdContent struct {
	Photo *struct {
		Mini  *tdMini       `json:"minithumbnail"`
		Sizes []tdPhotoSize `json:"sizes"`
	} `json:"photo"`
	Video     *tdVideo `json:"video"`
	Animation *tdVideo `json:"animation"`
	VideoNote *tdVideo `json:"video_note"`
	Document  *struct {
		FileName string   `json:"file_name"`
		Mini     *tdMini  `json:"minithumbnail"`
		Thumb    *tdThumb `json:"thumbnail"`
		File     tdFile   `json:"document"`
	} `json:"document"`
	Sticker *struct {
		Width  int `json:"width"`
		Height int `json:"height"`
		Format struct {
			Type string `json:"@type"`
		} `json:"format"`
		Thumb *tdThumb `json:"thumbnail"`
		File  tdFile   `json:"sticker"`
	} `json:"sticker"`
	Audio *struct {
		Duration  int    `json:"duration"`
		Title     string `json:"title"`
		Performer string `json:"performer"`
		FileName  string `json:"file_name"`
		File      tdFile `json:"audio"`
	} `json:"audio"`
	VoiceNote *struct {
		Duration int    `json:"duration"`
		File     tdFile `json:"voice"`
	} `json:"voice_note"`
	LinkPreview *struct {
		URL         string        `json:"url"`
		Site        string        `json:"site_name"`
		Title       string        `json:"title"`
		Description formattedText `json:"description"`
	} `json:"link_preview"`
}

type tdPhotoSize struct {
	Photo  tdFile `json:"photo"`
	Width  int    `json:"width"`
	Height int    `json:"height"`
}

type tdVideo struct {
	Duration int      `json:"duration"`
	Width    int      `json:"width"`
	Height   int      `json:"height"`
	FileName string   `json:"file_name"`
	Mini     *tdMini  `json:"minithumbnail"`
	Thumb    *tdThumb `json:"thumbnail"`
	File     tdFile   `json:"video"`
	Anim     tdFile   `json:"animation"`
}

// setMedia fills m.Media and m.Preview from a content. Called with mu held: it records
// the state of the files it meets.
func (c *Core) setMedia(m *Message, raw json.RawMessage) {
	var ct tdContent
	json.Unmarshal(raw, &ct)
	md := &Media{}
	file := func(f tdFile) int32 {
		c.files[f.ID] = fileOf(f)
		md.Size = max(f.Size, f.Expected)
		return f.ID
	}
	thumb := func(t *tdThumb, mini *tdMini) {
		if mini != nil {
			md.Mini, md.W, md.H = mini.Data, mini.Width, mini.Height
		}
		if t != nil && drawable(t.Format.Type) {
			c.files[t.File.ID] = fileOf(t.File)
			md.Thumb, md.W, md.H = t.File.ID, t.Width, t.Height
		}
	}
	switch {
	case ct.Photo != nil && len(ct.Photo.Sizes) > 0:
		s := ct.Photo.Sizes
		md.File = file(s[len(s)-1].Photo) // sizes go from small to large
		// Preview from the smallest size at least 320 px wide: sharp enough in a terminal.
		i := slices.IndexFunc(s, func(p tdPhotoSize) bool { return p.Width >= 320 })
		if i < 0 {
			i = len(s) - 1
		}
		c.files[s[i].Photo.ID] = fileOf(s[i].Photo)
		md.Thumb, md.W, md.H = s[i].Photo.ID, s[i].Width, s[i].Height
		if ct.Photo.Mini != nil {
			md.Mini = ct.Photo.Mini.Data
		}
	case ct.Video != nil || ct.Animation != nil || ct.VideoNote != nil:
		v := cmpOr(ct.Video, ct.Animation, ct.VideoNote)
		f := v.File
		if ct.Animation != nil {
			f = v.Anim
		}
		md.File, md.Name, md.Duration = file(f), v.FileName, v.Duration
		thumb(v.Thumb, v.Mini)
	case ct.Document != nil:
		md.File, md.Name = file(ct.Document.File), ct.Document.FileName
		thumb(ct.Document.Thumb, ct.Document.Mini)
	case ct.Sticker != nil:
		md.File = file(ct.Sticker.File)
		if ct.Sticker.Format.Type == "stickerFormatWebp" { // static: draw the sticker itself
			md.Thumb, md.W, md.H = ct.Sticker.File.ID, ct.Sticker.Width, ct.Sticker.Height
		} else {
			thumb(ct.Sticker.Thumb, nil)
		}
	case ct.Audio != nil:
		a := ct.Audio
		md.File, md.Duration = file(a.File), a.Duration
		md.Name = strings.TrimSpace(strings.Trim(a.Performer+" – "+a.Title, " –"))
		if md.Name == "" {
			md.Name = a.FileName
		}
	case ct.VoiceNote != nil:
		md.File, md.Duration = file(ct.VoiceNote.File), ct.VoiceNote.Duration
	default:
		md = nil
	}
	m.Media = md
	m.Preview = nil
	if p := ct.LinkPreview; p != nil {
		m.Preview = &LinkPreview{URL: p.URL, Site: p.Site, Title: p.Title, Description: p.Description.Text}
	}
}

func cmpOr[T any](ps ...*T) *T {
	for _, p := range ps {
		if p != nil {
			return p
		}
	}
	return nil
}

func fileOf(f tdFile) File {
	return File{ID: f.ID, Size: max(f.Size, f.Expected), Path: f.Local.Path, Done: f.Local.Done,
		Downloading: f.Local.Downloading, Downloaded: f.Local.Downloaded}
}

// File returns the state of file id.
func (c *Core) File(id int32) File {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.files[id]
}

// Download starts downloading file id; its progress arrives as changes of File(id).
// Previews pass a lower priority than files the user asked for.
func (c *Core) Download(id int32, priority int) error {
	return c.send(map[string]any{"@type": "downloadFile", "file_id": id, "priority": priority})
}

// SaveToDownloads copies the downloaded file id to ~/Downloads as name, without
// overwriting: "name (1).ext" and so on. It returns the new path.
func (c *Core) SaveToDownloads(id int32, name string) (string, error) {
	f := c.File(id)
	if !f.Done {
		return "", errors.New("not downloaded yet")
	}
	if name == "" {
		name = filepath.Base(f.Path)
	}
	home, _ := os.UserHomeDir()
	dir := filepath.Join(home, "Downloads")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", err
	}
	ext := filepath.Ext(name)
	base := strings.TrimSuffix(filepath.Base(name), ext)
	dst := filepath.Join(dir, base+ext)
	for i := 1; ; i++ {
		if _, err := os.Stat(dst); errors.Is(err, os.ErrNotExist) {
			break
		}
		dst = filepath.Join(dir, base+" ("+strconv.Itoa(i)+")"+ext)
	}
	in, err := os.Open(f.Path)
	if err != nil {
		return "", err
	}
	defer in.Close()
	out, err := os.OpenFile(dst, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o644)
	if err != nil {
		return "", err
	}
	if _, err := io.Copy(out, in); err != nil {
		out.Close()
		os.Remove(dst)
		return "", err
	}
	return dst, out.Close()
}

// sendKind is how a local file is sent: photo, video, or as a file.
func sendKind(path string) string {
	switch strings.ToLower(filepath.Ext(path)) {
	case ".jpg", ".jpeg", ".png", ".webp":
		return "Photo"
	case ".mp4", ".mov", ".m4v":
		return "Video"
	}
	return "Document"
}

// SendFiles sends local files to the open chat with caption (markup) on the first one.
// Photos and videos go together as albums, other files as albums of files, up to 10 per
// album as Telegram allows.
func (c *Core) SendFiles(paths []string, caption string, replyTo int64) error {
	var media, docs []string
	for _, p := range paths {
		if sendKind(p) == "Document" {
			docs = append(docs, p)
		} else {
			media = append(media, p)
		}
	}
	first := true
	for _, group := range [][]string{media, docs} {
		for len(group) > 0 {
			n := min(len(group), 10)
			var contents []map[string]any
			for _, p := range group[:n] {
				ct, err := c.inputFile(p, caption, first)
				if err != nil {
					return err
				}
				contents, first = append(contents, ct), false
			}
			group = group[n:]
			req := map[string]any{"chat_id": c.openID()}
			if replyTo != 0 {
				req["reply_to"] = map[string]any{"@type": "inputMessageReplyToMessage", "message_id": replyTo}
			}
			if len(contents) == 1 {
				req["@type"], req["input_message_content"] = "sendMessage", contents[0]
			} else {
				req["@type"], req["input_message_contents"] = "sendMessageAlbum", contents
			}
			if err := c.send(req); err != nil {
				return err
			}
		}
	}
	return nil
}

func (c *Core) inputFile(path, caption string, withCaption bool) (map[string]any, error) {
	abs, err := filepath.Abs(path)
	if err != nil {
		return nil, err
	}
	in := map[string]any{"@type": "inputFileLocal", "path": abs}
	ct := map[string]any{}
	switch kind := sendKind(path); kind {
	case "Photo":
		ct["@type"], ct["photo"] = "inputMessagePhoto", map[string]any{"@type": "inputPhoto", "photo": in}
	case "Video":
		ct["@type"], ct["video"] = "inputMessageVideo", map[string]any{"@type": "inputVideo", "video": in, "supports_streaming": true}
	default:
		ct["@type"], ct["document"] = "inputMessageDocument", map[string]any{"@type": "inputDocument", "document": in}
	}
	if withCaption && strings.TrimSpace(caption) != "" {
		ft, err := c.markdown(caption)
		if err != nil {
			return nil, err
		}
		ct["caption"] = ft
	}
	return ct, nil
}
