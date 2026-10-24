package claudedesktop

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

// peerProtocol is the only version of Claude Code's peer messaging this
// manager speaks; an entry on another one is never typed into.
const peerProtocol = 1

const hostPrefix = "local_"

// entry is one process in Claude Code's registry, <SessionsDir>/<pid>.json.
type entry struct {
	PID                 int    `json:"pid"`
	SessionID           string `json:"sessionId"`
	Cwd                 string `json:"cwd"`
	StartedAt           int64  `json:"startedAt"`
	PeerProtocol        int    `json:"peerProtocol"`
	Entrypoint          string `json:"entrypoint"`
	HostSessionID       string `json:"hostSessionId"`
	MessagingSocketPath string `json:"messagingSocketPath"`
	Name                string `json:"name"`
	Status              string `json:"status"`
	StatusUpdatedAt     int64  `json:"statusUpdatedAt"`
}

func (e entry) speaks() error {
	if e.PeerProtocol != peerProtocol {
		return fmt.Errorf("the Claude Code process %d speaks peer protocol %d; only %d is supported", e.PID, e.PeerProtocol, peerProtocol)
	}
	return nil
}

func (e entry) statusSince() time.Time {
	if e.StatusUpdatedAt == 0 {
		return time.Time{}
	}
	return time.UnixMilli(e.StatusUpdatedAt)
}

// registry is the live processes Desktop runs, oldest first.
func (m *Manager) registry() ([]entry, error) {
	files, err := os.ReadDir(m.SessionsDir)
	if errors.Is(err, fs.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("reading the Claude Code process registry: %w", err)
	}
	var out []entry
	for _, f := range files {
		pid, ok := strings.CutSuffix(f.Name(), ".json")
		if !ok {
			continue
		}
		if _, err := strconv.Atoi(pid); err != nil {
			continue
		}
		e, err := readEntry(filepath.Join(m.SessionsDir, f.Name()))
		if err != nil {
			continue
		}
		if e.Entrypoint != entrypoint || !strings.HasPrefix(e.HostSessionID, hostPrefix) || !m.alive(e.PID) {
			continue
		}
		out = append(out, e)
	}
	return out, nil
}

func readEntry(path string) (entry, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return entry{}, err
	}
	var e entry
	err = json.Unmarshal(b, &e)
	return e, err
}

func (m *Manager) alive(pid int) bool {
	if pid <= 0 {
		return false
	}
	if m.Alive == nil {
		return processAlive(pid)
	}
	return m.Alive(pid)
}

// byHost is the newest live process of the Desktop session handle.
func (m *Manager) byHost(handle string) (entry, bool, error) {
	es, err := m.registry()
	if err != nil {
		return entry{}, false, err
	}
	e, ok := newest(es, func(e entry) bool { return e.HostSessionID == handle })
	return e, ok, nil
}

func newest(es []entry, match func(entry) bool) (entry, bool) {
	var best entry
	found := false
	for _, e := range es {
		if match(e) && (!found || e.StartedAt > best.StartedAt) {
			best, found = e, true
		}
	}
	return best, found
}

// token is the peer token Claude Code wrote beside the process's entry.
func (m *Manager) token(pid int) (string, error) {
	keys, err := filepath.Glob(filepath.Join(m.SessionsDir, strconv.Itoa(pid)+".*.key"))
	if err != nil {
		return "", err
	}
	if len(keys) != 1 {
		return "", fmt.Errorf("the Claude Code process %d has %d peer keys, want 1", pid, len(keys))
	}
	b, err := os.ReadFile(keys[0])
	if err != nil {
		return "", err
	}
	var k struct {
		PeerToken string `json:"peerToken"`
	}
	if err := json.Unmarshal(b, &k); err != nil || k.PeerToken == "" {
		return "", fmt.Errorf("the peer key of the Claude Code process %d holds no token", pid)
	}
	return k.PeerToken, nil
}
