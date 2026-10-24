package echo

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"os"
	"path/filepath"
	"slices"

	"github.com/wloophq/wloop/internal/brand"
	"github.com/wloophq/wloop/internal/core/setup"
)

// homeFile holds, in an echo home, what Install registered there.
var homeFile = brand.Name + ".json"

// HomeKind keeps the echo agent's homes under Root/.echo.
type HomeKind struct{ Root string }

var (
	_ setup.HomeKind  = HomeKind{}
	_ setup.HomeOwner = HomeKind{}
)

func (k HomeKind) New(s setup.HomeSpec) setup.AgentHome {
	return Home{Command: s.Command, Entries: map[string]string{
		"mcp":        s.MCPURL,
		"key":        s.MCPKey,
		"session":    setup.SessionValue(s.SessionVars),
		"hook stop":  s.Command + " hook stop --agent echo",
		"hook start": s.Command + " hook session-start --agent echo",
	}}
}

func (k HomeKind) Find(s setup.HomeSpec) []string {
	out := []string{k.Default(s.UserHome)}
	if s.CallerHome != "" {
		out = append(out, s.CallerHome)
	}
	return append(out, s.ManagerHomes...)
}

func (k HomeKind) Default(string) string { return filepath.Join(k.Root, ".echo") }

func (HomeKind) SkillsRoot(home string) string { return home }

func (HomeKind) Owns(dir string) bool {
	_, err := os.Stat(filepath.Join(dir, homeFile))
	return err == nil
}

// Home writes Entries into a home's registration file.
type Home struct {
	Command string
	Entries map[string]string
}

func (h Home) read(home string) (map[string]string, error) {
	raw, err := os.ReadFile(filepath.Join(home, homeFile))
	if errors.Is(err, os.ErrNotExist) {
		return map[string]string{}, nil
	}
	if err != nil {
		return nil, err
	}
	var got map[string]string
	return got, json.Unmarshal(raw, &got)
}

func (h Home) Check(_ context.Context, home string) ([]setup.Finding, error) {
	got, err := h.read(home)
	if err != nil {
		return nil, err
	}
	var out []setup.Finding
	for _, k := range slices.Sorted(maps.Keys(h.Entries)) {
		f := setup.Finding{Home: home, Entry: k}
		switch v, ok := got[k]; {
		case !ok:
			f.Problem = "missing"
		case v != h.Entries[k]:
			f.Problem = "differs"
		}
		if f.Problem != "" {
			f.Fix = setup.InstallCommand(h.Command, home)
		}
		out = append(out, f)
	}
	return out, nil
}

func (h Home) Install(_ context.Context, home string, dryRun bool) ([]string, error) {
	got, err := h.read(home)
	if err != nil || maps.Equal(got, h.Entries) {
		return nil, err
	}
	path := filepath.Join(home, homeFile)
	if dryRun {
		return []string{"would write " + path}, nil
	}
	raw, err := json.MarshalIndent(h.Entries, "", "  ")
	if err != nil {
		return nil, err
	}
	if err := os.WriteFile(path, append(raw, '\n'), 0o600); err != nil {
		return nil, err
	}
	return []string{"wrote " + path}, nil
}

func (h Home) Remove(_ context.Context, home string, dryRun bool) ([]string, error) {
	path := filepath.Join(home, homeFile)
	if _, err := os.Stat(path); errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if dryRun {
		return []string{"would remove " + path}, nil
	}
	if err := os.Remove(path); err != nil {
		return nil, fmt.Errorf("removing %s: %w", path, err)
	}
	return []string{"removed " + path}, nil
}
