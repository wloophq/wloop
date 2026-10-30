package claude

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"io"
	"io/fs"
	"os"
	"strings"

	"github.com/wloophq/wloop/internal/core/turns"
)

// Transcripts pages a Claude Code session's transcript: the messages a
// person typed and the text the agent answered, oldest first.
type Transcripts struct {
	// Transcript is the transcript path and agent the hooks stored for an
	// agent session, "" when they stored none.
	Transcript func(ctx context.Context, agentSession string) (path, kind string, err error)
}

var _ turns.Transcripts = Transcripts{}

// Page reads the whole transcript on each call: its line count is part of
// the answer. A session with no transcript on disk, or another agent's,
// has none.
func (t Transcripts) Page(ctx context.Context, agentSession string, from, limit int) ([]turns.TranscriptLine, int, error) {
	if t.Transcript == nil || agentSession == "" {
		return nil, 0, nil
	}
	path, kind, err := t.Transcript(ctx, agentSession)
	if err != nil || path == "" || kind != "" && kind != (Agent{}).Name() {
		return nil, 0, err
	}
	all, err := transcriptMessages(ctx, path)
	if errors.Is(err, fs.ErrNotExist) {
		return nil, 0, nil
	}
	if err != nil {
		return nil, 0, err
	}
	from = max(from, 0)
	if from >= len(all) || limit <= 0 {
		return nil, len(all), nil
	}
	return all[from:min(len(all), from+limit)], len(all), nil
}

type messageLine struct {
	promptLine
	Attachment struct {
		Type        string `json:"type"`
		CommandMode string `json:"commandMode"`
		Prompt      string `json:"prompt"`
	} `json:"attachment"`
}

func transcriptMessages(ctx context.Context, path string) ([]turns.TranscriptLine, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	var out []turns.TranscriptLine
	r := bufio.NewReader(f)
	for {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		raw, err := r.ReadBytes('\n')
		if l, ok := message(raw); ok {
			out = append(out, l)
		}
		if errors.Is(err, io.EOF) {
			return out, nil
		}
		if err != nil {
			return nil, err
		}
	}
}

// message is the transcript entry raw as a line a person reads: a prompt
// typed by a person, or the agent's text; false for everything else.
func message(raw []byte) (turns.TranscriptLine, bool) {
	at, typed := typedByPerson(raw)
	var l messageLine
	if json.Unmarshal(raw, &l) != nil {
		return turns.TranscriptLine{}, false
	}
	switch {
	case typed && l.Type == "attachment":
		return turns.TranscriptLine{Role: "user", Text: l.Attachment.Prompt, At: at}, l.Attachment.Prompt != ""
	case typed:
		text := contentText(l.Message.Content)
		return turns.TranscriptLine{Role: "user", Text: text, At: at}, text != ""
	case l.Type == "assistant" && !l.IsSidechain && !l.IsMeta:
		text := contentText(l.Message.Content)
		return turns.TranscriptLine{Role: "assistant", Text: text, At: l.Timestamp}, text != ""
	}
	return turns.TranscriptLine{}, false
}

func contentText(content json.RawMessage) string {
	var s string
	if json.Unmarshal(content, &s) == nil {
		return s
	}
	var blocks []struct {
		Type string `json:"type"`
		Text string `json:"text"`
	}
	if json.Unmarshal(content, &blocks) != nil {
		return ""
	}
	var parts []string
	for _, b := range blocks {
		if b.Type == "text" && b.Text != "" {
			parts = append(parts, b.Text)
		}
	}
	return strings.Join(parts, "\n")
}
