package claudedesktop

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
)

// stored is one session in Desktop's store.
type stored struct {
	SessionID      string `json:"sessionId"`
	CliSessionID   string `json:"cliSessionId"`
	Cwd            string `json:"cwd"`
	WorktreePath   string `json:"worktreePath"`
	IsArchived     bool   `json:"isArchived"`
	LastActivityAt int64  `json:"lastActivityAt"`
}

func (m *Manager) storeFiles(pattern string) ([]string, error) {
	return filepath.Glob(filepath.Join(m.StoreDir, "*", "*", pattern))
}

// Closed reports whether the user closed the Desktop session of uuid: its
// store file is gone or archived.
func (m *Manager) Closed(ctx context.Context, session string) (bool, error) {
	uuid := uuidOf(session, session)
	if !validUUID(uuid) {
		return false, fmt.Errorf("%q is not a session uuid", uuid)
	}
	files, err := m.storeFiles(hostPrefix + uuid + ".json")
	if err != nil {
		return false, err
	}
	if len(files) == 0 {
		return true, nil
	}
	for _, f := range files {
		s, err := readStored(f)
		if errors.Is(err, os.ErrNotExist) {
			continue
		}
		if err != nil {
			return false, err
		}
		if !s.IsArchived {
			return false, nil
		}
	}
	return true, nil
}

func readStored(path string) (stored, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return stored{}, err
	}
	var s stored
	if err := json.Unmarshal(b, &s); err != nil {
		return stored{}, fmt.Errorf("reading %s: %w", path, err)
	}
	return s, nil
}

// lastStored is the handle of the open stored session in cwd with the latest activity.
func (m *Manager) lastStored(cwd string) (string, bool, error) {
	files, err := m.storeFiles(hostPrefix + "*.json")
	if err != nil {
		return "", false, err
	}
	var best stored
	for _, f := range files {
		s, err := readStored(f)
		if err != nil || s.IsArchived || filepath.Clean(s.Cwd) != cwd {
			continue
		}
		if best.SessionID == "" || s.LastActivityAt > best.LastActivityAt {
			best = s
		}
	}
	return best.SessionID, best.SessionID != "", nil
}
