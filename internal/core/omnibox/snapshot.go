package omnibox

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"

	"github.com/wloophq/wloop/internal/api"
)

// Snapshot is the read model an omnibox call reads: the tasks, the views and the repositories the user works
// in, and the omnibox's vocabulary when it has one.
type Snapshot struct {
	Tasks []api.TaskView
	Views api.ViewsCatalog
	Repos []string
	// Vocabulary is written to vocabulary.json when set: the verbs and the operation each targets.
	Vocabulary any
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
	files := map[string]any{"tasks.json": nonNil(s.Tasks), "views.json": s.Views, "repos.json": nonNil(s.Repos)}
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
