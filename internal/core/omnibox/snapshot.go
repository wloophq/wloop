package omnibox

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/wloophq/wloop/internal/domain/unit"
)

// Snapshot is the read model an omnibox call reads: the tasks, the views and the repositories the user works
// in, and the omnibox's vocabulary when it has one.
type Snapshot struct {
	Tasks []SnapshotTask
	Views []SnapshotView
	Repos []string
	// Vocabulary is written to vocabulary.json when set: the verbs and the operation each targets.
	Vocabulary any
}

// SnapshotTask is a task as the call reads it in tasks.json.
type SnapshotTask struct {
	ID         string                     `json:"id"`
	Title      string                     `json:"title"`
	State      string                     `json:"state"`
	Category   string                     `json:"category,omitempty"`
	Notes      string                     `json:"notes,omitempty"`
	Parent     string                     `json:"parent,omitempty"`
	Props      map[string]json.RawMessage `json:"props,omitempty"`
	Units      []unit.Unit                `json:"units"`
	UnitLabels []string                   `json:"unit_labels,omitempty"`
	Created    time.Time                  `json:"created"`
	Updated    time.Time                  `json:"updated"`
}

// SnapshotView is a view of the profile as the call reads it in views.json: When is the predicate that
// fills it.
type SnapshotView struct {
	ID    string `json:"id"`
	Label string `json:"label"`
	When  string `json:"when,omitempty"`
}

// write puts s's files in a new directory under parent; the caller removes it.
func (s Snapshot) write(parent string) (string, error) {
	if err := os.MkdirAll(parent, 0o700); err != nil {
		return "", err
	}
	dir, err := os.MkdirTemp(parent, "ask-")
	if err != nil {
		return "", err
	}
	files := map[string]any{"tasks.json": nonNil(s.Tasks), "views.json": nonNil(s.Views), "repos.json": nonNil(s.Repos)}
	if s.Vocabulary != nil {
		files["vocabulary.json"] = s.Vocabulary
	}
	for name, v := range files {
		b, err := json.MarshalIndent(v, "", " ")
		if err != nil {
			return dir, fmt.Errorf("writing %s: %w", name, err)
		}
		if err := os.WriteFile(filepath.Join(dir, name), b, 0o600); err != nil {
			return dir, err
		}
	}
	return dir, nil
}

func nonNil[T any](s []T) []T {
	if s == nil {
		return []T{}
	}
	return s
}
