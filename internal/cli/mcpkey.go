package cli

import (
	"crypto/rand"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
)

// mcpKeyFile holds the secret the MCP endpoint requires, which install writes
// into every agent home's registration.
const mcpKeyFile = "mcp.key"

func (e *Env) mcpKeyPath() string { return filepath.Join(e.StateDir, mcpKeyFile) }

// mcpKey is the stored secret, "" when install has not written one.
func (e *Env) mcpKey() string {
	raw, err := os.ReadFile(e.mcpKeyPath())
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(raw))
}

// ensureMCPKey returns the stored secret, writing a new one when there is
// none; a dry run writes nothing and returns a key it does not keep.
func (e *Env) ensureMCPKey(dryRun bool) (string, error) {
	if k := e.mcpKey(); k != "" {
		return k, nil
	}
	key := rand.Text()
	if dryRun {
		return key, nil
	}
	if err := os.MkdirAll(e.StateDir, 0o700); err != nil {
		return "", err
	}
	f, err := os.CreateTemp(e.StateDir, "."+mcpKeyFile+"-*.tmp")
	if err != nil {
		return "", err
	}
	_, werr := f.WriteString(key + "\n")
	if err := errors.Join(werr, f.Chmod(0o600), f.Sync(), f.Close()); err != nil {
		os.Remove(f.Name())
		return "", err
	}
	if err := os.Link(f.Name(), e.mcpKeyPath()); err != nil {
		os.Remove(f.Name())
		if errors.Is(err, fs.ErrExist) {
			if k := e.mcpKey(); k != "" {
				return k, nil
			}
		}
		return "", err
	}
	os.Remove(f.Name())
	return key, nil
}
