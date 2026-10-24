package claude

import (
	"bufio"
	"bytes"
	"cmp"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/wloophq/wloop/internal/core/usage"
	dusage "github.com/wloophq/wloop/internal/domain/usage"
)

// Usage reads a turn's tokens from the transcripts Claude Code writes.
type Usage struct{ UserHome string }

type usageLine struct {
	Type      string    `json:"type"`
	Timestamp time.Time `json:"timestamp"`
	Message   struct {
		ID    string `json:"id"`
		Model string `json:"model"`
		Usage struct {
			Input      int64 `json:"input_tokens"`
			Output     int64 `json:"output_tokens"`
			CacheRead  int64 `json:"cache_read_input_tokens"`
			CacheWrite int64 `json:"cache_creation_input_tokens"`
		} `json:"usage"`
	} `json:"message"`
}

// Usage sums, per model, the assistant messages of the turn's transcript and
// its subagents' that fall in the turn's window. Streaming repeats a message
// over several lines with a growing output count; the largest one is kept.
func (u Usage) Usage(ctx context.Context, t usage.Turn) ([]dusage.Model, error) {
	path, err := u.transcript(t)
	if err != nil {
		return nil, err
	}
	subagents, err := filepath.Glob(filepath.Join(strings.TrimSuffix(path, ".jsonl"), "subagents", "*.jsonl"))
	if err != nil {
		return nil, err
	}
	kept := map[string]usageLine{}
	for i, p := range append([]string{path}, subagents...) {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		if err := readUsage(p, t.From, t.To, fmt.Sprint(i), kept); err != nil {
			return nil, err
		}
	}
	return sumUsage(kept), nil
}

func (u Usage) transcript(t usage.Turn) (string, error) {
	var paths []string
	if t.Transcript != "" {
		paths = append(paths, t.Transcript)
	}
	if t.AgentSession != "" {
		home := cmp.Or(t.Home, DefaultHome(u.UserHome))
		paths = append(paths, filepath.Join(home, "projects", projectSlug(t.Worktree), t.AgentSession+".jsonl"))
	}
	for _, p := range paths {
		if _, err := os.Stat(p); !errors.Is(err, fs.ErrNotExist) {
			return p, err
		}
	}
	return "", fmt.Errorf("%s: %w", strings.Join(paths, ", "), usage.ErrNoTranscript)
}

func projectSlug(dir string) string {
	return strings.Map(func(r rune) rune {
		if 'a' <= r && r <= 'z' || 'A' <= r && r <= 'Z' || '0' <= r && r <= '9' {
			return r
		}
		return '-'
	}, dir)
}

// syntheticModel marks the entries Claude Code writes itself, which no model
// produced and which carry no usage.
const syntheticModel = "<synthetic>"

var assistantType = []byte(`"assistant"`)

func readUsage(path string, from, to time.Time, file string, kept map[string]usageLine) error {
	f, err := os.Open(path)
	if err != nil {
		return err
	}
	defer f.Close()
	r := bufio.NewReader(f)
	for n := 0; ; n++ {
		raw, err := r.ReadBytes('\n')
		if len(raw) > 0 && bytes.Contains(raw, assistantType) {
			keepUsage(raw, from, to, fmt.Sprintf("%s:%d", file, n), kept)
		}
		if errors.Is(err, io.EOF) {
			return nil
		}
		if err != nil {
			return err
		}
	}
}

func keepUsage(raw []byte, from, to time.Time, at string, kept map[string]usageLine) {
	var l usageLine
	if json.Unmarshal(raw, &l) != nil || l.Type != "assistant" || l.Message.Model == "" || l.Message.Model == syntheticModel {
		return
	}
	if l.Timestamp.Before(from) || l.Timestamp.After(to) {
		return
	}
	id := cmp.Or(l.Message.ID, at)
	if old, ok := kept[id]; !ok || l.Message.Usage.Output > old.Message.Usage.Output {
		kept[id] = l
	}
}

func sumUsage(kept map[string]usageLine) []dusage.Model {
	by := map[string]*dusage.Model{}
	for _, l := range kept {
		m := by[l.Message.Model]
		if m == nil {
			m = &dusage.Model{Model: l.Message.Model, First: l.Timestamp, Last: l.Timestamp}
			by[l.Message.Model] = m
		}
		u := l.Message.Usage
		m.Messages++
		m.Tokens = m.Tokens.Plus(dusage.Tokens{Input: u.Input, Output: u.Output, CacheRead: u.CacheRead, CacheWrite: u.CacheWrite})
		if l.Timestamp.Before(m.First) {
			m.First = l.Timestamp
		}
		if l.Timestamp.After(m.Last) {
			m.Last = l.Timestamp
		}
	}
	out := make([]dusage.Model, 0, len(by))
	for _, m := range by {
		out = append(out, *m)
	}
	slices.SortFunc(out, func(a, b dusage.Model) int { return cmp.Compare(a.Model, b.Model) })
	return out
}
