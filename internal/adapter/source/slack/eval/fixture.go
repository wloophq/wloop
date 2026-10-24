// Package eval measures the Slack extractor: an oracle reads a conversation
// and writes the viewer's briefing, a blind reader writes the same briefing
// from the extractor's ledger alone, and a judge with the conversation
// compares them.
package eval

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/wloophq/wloop/internal/adapter/source/slack/extract"
	"github.com/wloophq/wloop/internal/domain/slack"
)

// DefaultNow is the "now" of a fixture that names none: when the first
// fixtures were measured.
const DefaultNow = "2026-10-21T21:00:00-03:00"

const (
	SplitDev     = "dev"
	SplitHoldout = "holdout"
)

const (
	usersFile  = "users.json"
	viewerFile = "viewer.json"
)

type Fixture struct {
	ID          string `json:"id"`
	Kind        string `json:"kind"`
	Split       string `json:"split,omitempty"`
	Now         string `json:"now,omitempty"`
	ChannelID   string `json:"channel_id"`
	ChannelName string `json:"channel_name"`
	ChannelType string `json:"channel_type"`
	Permalink   string `json:"permalink"`
	FocusTS     string `json:"focus_ts"`
	// Context are the messages before a DM segment, shown to the extractor only, read only.
	Context  []Message `json:"context,omitempty"`
	Messages []Message `json:"messages"`
}

type Message struct {
	TS         string     `json:"ts"`
	ThreadTS   string     `json:"thread_ts"`
	UserID     string     `json:"user_id"`
	UserName   string     `json:"user_name"`
	IsBot      bool       `json:"is_bot"`
	AppName    string     `json:"app_name"`
	Text       string     `json:"text"`
	Reactions  []Reaction `json:"reactions"`
	ReplyCount int        `json:"reply_count"`
	Edited     bool       `json:"edited"`
	Files      []string   `json:"files,omitempty"`
}

type Reaction struct {
	Name  string   `json:"name"`
	Users []string `json:"users"`
}

// SplitOf is the fixture's split: its own, else holdout when the two-digit
// prefix of its id is divisible by 3.
func (f Fixture) SplitOf() string {
	if f.Split != "" {
		return f.Split
	}
	if len(f.ID) >= 2 {
		if n, err := strconv.Atoi(f.ID[:2]); err == nil && n%3 == 0 {
			return SplitHoldout
		}
	}
	return SplitDev
}

// Root is the ts the conversation hangs from: its first message's thread,
// or its own ts.
func (f Fixture) Root() string {
	if len(f.Messages) == 0 {
		return ""
	}
	first := f.Messages[0]
	for _, m := range f.Messages[1:] {
		if tsLess(m.TS, first.TS) {
			first = m
		}
	}
	if first.ThreadTS != "" {
		return first.ThreadTS
	}
	return first.TS
}

// NowAt is the fixture's now, overridden by override when set.
func (f Fixture) NowAt(override string) (time.Time, error) {
	s := override
	if s == "" {
		s = f.Now
	}
	if s == "" {
		s = DefaultNow
	}
	t, err := time.Parse(time.RFC3339, s)
	if err != nil {
		return time.Time{}, fmt.Errorf("fixture %s: now %q: %w", f.ID, s, err)
	}
	return t, nil
}

// Set is a fixtures directory: its fixtures by id, the user names of
// users.json and the viewer of viewer.json, when present.
type Set struct {
	Fixtures []Fixture
	Users    map[string]string
	Viewer   extract.Viewer
}

// Load reads every <id>.json of dir but users.json and viewer.json; the
// file name is the fixture's id, whatever its id field says.
func Load(dir string) (Set, error) {
	paths, err := filepath.Glob(filepath.Join(dir, "*.json"))
	if err != nil {
		return Set{}, err
	}
	s := Set{Users: map[string]string{}}
	for _, p := range paths {
		switch filepath.Base(p) {
		case usersFile:
			if s.Users, err = loadUsers(p); err != nil {
				return Set{}, err
			}
			continue
		case viewerFile:
			if err := readJSON(p, &s.Viewer); err != nil {
				return Set{}, err
			}
			continue
		}
		var f Fixture
		if err := readJSON(p, &f); err != nil {
			return Set{}, err
		}
		f.ID = strings.TrimSuffix(filepath.Base(p), ".json")
		if f.Split != "" && f.Split != SplitDev && f.Split != SplitHoldout {
			return Set{}, fmt.Errorf("%s: split %q is neither dev nor holdout", p, f.Split)
		}
		slices.SortStableFunc(f.Messages, func(a, b Message) int { return tsCompare(a.TS, b.TS) })
		s.Fixtures = append(s.Fixtures, f)
	}
	if len(s.Fixtures) == 0 {
		return Set{}, fmt.Errorf("no fixture in %s", dir)
	}
	slices.SortFunc(s.Fixtures, func(a, b Fixture) int { return strings.Compare(a.ID, b.ID) })
	return s, nil
}

// loadUsers reads users.json, whose values are a name or {"name": ...}.
func loadUsers(path string) (map[string]string, error) {
	var raw map[string]json.RawMessage
	if err := readJSON(path, &raw); err != nil {
		return nil, err
	}
	out := make(map[string]string, len(raw))
	for id, v := range raw {
		var name string
		if json.Unmarshal(v, &name) != nil {
			var obj struct {
				Name string `json:"name"`
			}
			if err := json.Unmarshal(v, &obj); err != nil {
				return nil, fmt.Errorf("%s: %s: %w", path, id, err)
			}
			name = obj.Name
		}
		out[id] = name
	}
	return out, nil
}

func readJSON(path string, v any) error {
	raw, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	if err := json.Unmarshal(raw, v); err != nil {
		return fmt.Errorf("%s: %w", path, err)
	}
	return nil
}

// marshal is indented JSON that keeps <, > and & as written.
func marshal(v any) ([]byte, error) {
	var b bytes.Buffer
	enc := json.NewEncoder(&b)
	enc.SetEscapeHTML(false)
	enc.SetIndent("", " ")
	if err := enc.Encode(v); err != nil {
		return nil, err
	}
	return b.Bytes(), nil
}

func writeJSON(path string, v any) error {
	raw, err := marshal(v)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	return os.WriteFile(path, raw, 0o600)
}

// Input is the extractor's input for f: an empty ledger, no segments and no
// window, so Render shows the whole fixture; a DM segment's context, bounds
// and focus are marked.
func (f Fixture) Input(v extract.Viewer, names map[string]string, now time.Time, language string) extract.Input {
	in := extract.Input{
		Viewer:   v,
		Now:      now,
		Language: language,
		Conversation: slack.Conversation{
			ID: f.ChannelID, Name: f.ChannelName, Kind: conversationKind(f.ChannelType), IsMember: true,
		},
		Users: map[string]slack.User{},
	}
	for id, name := range names {
		in.Users[id] = slack.User{ID: id, Name: name}
	}
	for _, m := range f.Context {
		if m.UserID != "" && m.UserName != "" {
			in.Users[m.UserID] = slack.User{ID: m.UserID, Name: m.UserName, IsBot: m.IsBot && m.AppName == ""}
		}
		in.Context = append(in.Context, m.message())
	}
	for _, m := range f.Messages {
		if m.UserID != "" && m.UserName != "" {
			in.Users[m.UserID] = slack.User{ID: m.UserID, Name: m.UserName, IsBot: m.IsBot && m.AppName == ""}
		}
		in.Messages = append(in.Messages, m.message())
		if in.Conversation.Kind == slack.KindIM && in.Conversation.DMUser == "" && m.UserID != "" && m.UserID != v.ID {
			in.Conversation.DMUser = m.UserID
		}
	}
	if f.Kind == "dm_segment" && len(in.Messages) > 0 {
		in.Segment = slack.Span{FromTS: in.Messages[0].TS}
		in.Focus = f.FocusTS
	}
	return in
}

// Plain is in without context, segment or focus: what the oracle and the judge read.
func Plain(in extract.Input) extract.Input {
	in.Context, in.Segment, in.Focus = nil, slack.Span{}, ""
	return in
}

func (m Message) message() slack.Message {
	out := slack.Message{TS: m.TS, User: m.UserID, Text: m.Text, ReplyCount: m.ReplyCount}
	if m.ThreadTS != m.TS {
		out.ThreadTS = m.ThreadTS
	}
	if m.Edited {
		out.EditedTS = m.TS
	}
	if m.UserID == "" {
		out.BotID, out.Username = "bot", cmpOr(m.AppName, m.UserName)
	}
	for _, f := range m.Files {
		out.Files = append(out.Files, slack.File{Title: f})
	}
	for _, r := range m.Reactions {
		out.Reactions = append(out.Reactions, slack.Reaction{Name: r.Name, Users: r.Users})
	}
	return out
}

func conversationKind(t string) slack.ConversationKind {
	switch t {
	case "private":
		return slack.KindPrivate
	case "im":
		return slack.KindIM
	case "mpim":
		return slack.KindMPIM
	}
	return slack.KindChannel
}

func cmpOr(a, b string) string {
	if a != "" {
		return a
	}
	return b
}

func tsCompare(a, b string) int {
	switch {
	case tsLess(a, b):
		return -1
	case tsLess(b, a):
		return 1
	}
	return 0
}

// tsLess compares Slack timestamps ("<seconds>.<micros>") numerically.
func tsLess(a, b string) bool {
	as, af, _ := strings.Cut(a, ".")
	bs, bf, _ := strings.Cut(b, ".")
	if len(as) != len(bs) {
		return len(as) < len(bs)
	}
	if as != bs {
		return as < bs
	}
	return af < bf
}

var errNoViewer = errors.New("no viewer: pass --viewer-id or put viewer.json in the fixtures directory")
