package claude

import (
	"bufio"
	"bytes"
	"cmp"
	"context"
	"encoding/json"
	"errors"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/wloophq/wloop/internal/core/turns"
	"github.com/wloophq/wloop/internal/domain/turn"
)

// Prompts reads when a person last prompted a session from the transcript
// Claude Code writes.
type Prompts struct {
	UserHome string
	// Transcript is the transcript path and agent the hooks stored for
	// an agent session, "" when they stored none; nil reads none.
	Transcript func(ctx context.Context, agentSession string) (path, kind string, err error)
}

var _ turns.Prompts = Prompts{}

// LastUserPrompt finds the session's transcript where the hooks stored it,
// else where Claude Code keeps it for the session's worktree; a session of
// another agent has none.
func (p Prompts) LastUserPrompt(ctx context.Context, sess turn.Session) (time.Time, bool, error) {
	if sess.AgentSession == "" {
		return time.Time{}, false, nil
	}
	var paths []string
	if p.Transcript != nil {
		path, kind, err := p.Transcript(ctx, sess.AgentSession)
		if err != nil {
			return time.Time{}, false, err
		}
		if kind != "" && kind != (Agent{}).Name() {
			return time.Time{}, false, nil
		}
		if path != "" {
			paths = append(paths, path)
		}
	}
	if sess.Worktree != "" {
		home := cmp.Or(sess.Home, DefaultHome(p.UserHome))
		paths = append(paths, filepath.Join(home, "projects", projectSlug(sess.Worktree), sess.AgentSession+".jsonl"))
	}
	for _, path := range paths {
		at, err := transcripts.lastUserPrompt(ctx, path)
		if errors.Is(err, fs.ErrNotExist) {
			continue
		}
		return at, err == nil, err
	}
	return time.Time{}, false, nil
}

// transcriptCache keeps each transcript's last prompt by path, size and
// modification time, so an unchanged transcript is read once.
type transcriptCache struct {
	mu   sync.Mutex
	seen map[string]transcriptRead
}

type transcriptRead struct {
	size int64
	mod  time.Time
	at   time.Time
}

var transcripts = &transcriptCache{seen: map[string]transcriptRead{}}

func (c *transcriptCache) lastUserPrompt(ctx context.Context, path string) (time.Time, error) {
	info, err := os.Stat(path)
	if err != nil {
		return time.Time{}, err
	}
	c.mu.Lock()
	r, ok := c.seen[path]
	c.mu.Unlock()
	if ok && r.size == info.Size() && r.mod.Equal(info.ModTime()) {
		return r.at, nil
	}
	at, err := lastUserPrompt(ctx, path)
	if err != nil {
		return time.Time{}, err
	}
	c.mu.Lock()
	c.seen[path] = transcriptRead{size: info.Size(), mod: info.ModTime(), at: at}
	c.mu.Unlock()
	return at, nil
}

type promptLine struct {
	Type             string    `json:"type"`
	Timestamp        time.Time `json:"timestamp"`
	IsMeta           bool      `json:"isMeta"`
	IsSidechain      bool      `json:"isSidechain"`
	IsCompactSummary bool      `json:"isCompactSummary"`
	Message          struct {
		Content json.RawMessage `json:"content"`
	} `json:"message"`
	Attachment struct {
		Type        string `json:"type"`
		CommandMode string `json:"commandMode"`
	} `json:"attachment"`
}

var userType, queuedType = []byte(`"user"`), []byte(`"queued_command"`)

// lastUserPrompt is the time of the transcript's newest entry a person typed:
// a user message that is no tool result and none Claude Code wrote itself, or
// a prompt queued while the agent worked, which Claude Code keeps as an
// attachment.
func lastUserPrompt(ctx context.Context, path string) (time.Time, error) {
	f, err := os.Open(path)
	if err != nil {
		return time.Time{}, err
	}
	defer f.Close()
	var last time.Time
	r := bufio.NewReader(f)
	for {
		if err := ctx.Err(); err != nil {
			return time.Time{}, err
		}
		raw, err := r.ReadBytes('\n')
		if len(raw) > 0 && (bytes.Contains(raw, userType) || bytes.Contains(raw, queuedType)) {
			if at, ok := typedByPerson(raw); ok && at.After(last) {
				last = at
			}
		}
		if errors.Is(err, io.EOF) {
			return last, nil
		}
		if err != nil {
			return time.Time{}, err
		}
	}
}

func typedByPerson(raw []byte) (time.Time, bool) {
	var l promptLine
	if json.Unmarshal(raw, &l) != nil || l.IsMeta || l.IsSidechain || l.IsCompactSummary || l.Timestamp.IsZero() {
		return time.Time{}, false
	}
	if l.Type == "attachment" {
		return l.Timestamp, l.Attachment.Type == "queued_command" && l.Attachment.CommandMode == "prompt"
	}
	if l.Type != "user" {
		return time.Time{}, false
	}
	var text string
	if json.Unmarshal(l.Message.Content, &text) == nil {
		return l.Timestamp, true
	}
	var blocks []struct {
		Type string `json:"type"`
	}
	if json.Unmarshal(l.Message.Content, &blocks) != nil {
		return time.Time{}, false
	}
	typed := false
	for _, b := range blocks {
		if b.Type == "tool_result" {
			return time.Time{}, false
		}
		typed = typed || b.Type == "text"
	}
	return l.Timestamp, typed
}
