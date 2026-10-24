package claudedesktop

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strings"
)

const localSettings = ".claude/settings.local.json"

// mergeSettings adds the settings to the worktree's local settings: a key
// already there keeps its value, and permissions.allow and permissions.deny
// take the union of both lists.
// loopRules is where the rules the loop added to the local settings are
// kept, so the next session's settings replace them instead of piling up.
const loopRules = ".claude/.loop-rules.json"

var ruleKeys = []string{"allow", "deny", "additionalDirectories"}

// mergeSettings writes raw's permission rules into the worktree's local
// settings: the rules the loop wrote for an earlier session go, the user's
// own rules and every other key stay, and raw's rules are added.
func mergeSettings(worktree, raw string) error {
	if strings.TrimSpace(raw) == "" {
		return nil
	}
	var add map[string]any
	if err := json.Unmarshal([]byte(raw), &add); err != nil {
		return fmt.Errorf("the settings are not a JSON object: %w", err)
	}
	path := filepath.Join(worktree, localSettings)
	have := map[string]any{}
	if err := readSettings(path, &have); err != nil {
		return err
	}
	prev := map[string][]any{}
	if err := readSettings(filepath.Join(worktree, loopRules), &prev); err != nil {
		return err
	}
	perms, _ := have["permissions"].(map[string]any)
	if perms == nil {
		perms = map[string]any{}
	}
	addPerms, _ := add["permissions"].(map[string]any)
	added := map[string][]any{}
	for _, k := range ruleKeys {
		cur, _ := perms[k].([]any)
		cur = slices.DeleteFunc(cur, func(r any) bool { return slices.Contains(prev[k], r) })
		want, _ := addPerms[k].([]any)
		for _, r := range want {
			if !slices.Contains(cur, r) {
				cur = append(cur, r)
				added[k] = append(added[k], r)
			}
		}
		if len(cur) > 0 {
			perms[k] = cur
		} else {
			delete(perms, k)
		}
	}
	for k, v := range addPerms {
		if _, ok := perms[k]; !ok && !slices.Contains(ruleKeys, k) {
			perms[k] = v
		}
	}
	have["permissions"] = perms
	for k, v := range add {
		if _, ok := have[k]; !ok && k != "permissions" {
			have[k] = v
		}
	}
	if err := writeSettings(path, have); err != nil {
		return err
	}
	if err := writeSettings(filepath.Join(worktree, loopRules), added); err != nil {
		return err
	}
	return excludeLocalSettings(worktree)
}

func readSettings(path string, v any) error {
	b, err := os.ReadFile(path)
	switch {
	case errors.Is(err, fs.ErrNotExist):
		return nil
	case err != nil:
		return err
	}
	if err := json.Unmarshal(b, v); err != nil {
		return fmt.Errorf("reading %s: %w", path, err)
	}
	return nil
}

func writeSettings(path string, v any) error {
	out, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	return os.WriteFile(path, append(out, '\n'), 0o600)
}

// excludeLocalSettings keeps the local settings out of the worktree's
// changes, so writing them never reads as work that removing it would lose.
func excludeLocalSettings(worktree string) error {
	gitDir, ok := linkedGitDir(worktree)
	if !ok {
		return nil
	}
	path := filepath.Join(commonDir(gitDir), "info", "exclude")
	b, err := os.ReadFile(path)
	if err != nil && !errors.Is(err, fs.ErrNotExist) {
		return err
	}
	lines := strings.Split(string(b), "\n")
	var missing []string
	for _, l := range []string{"/" + localSettings, "/" + loopRules} {
		if !slices.Contains(lines, l) {
			missing = append(missing, l)
		}
	}
	if len(missing) == 0 {
		return nil
	}
	if len(b) > 0 && !bytes.HasSuffix(b, []byte("\n")) {
		b = append(b, '\n')
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	return os.WriteFile(path, append(b, strings.Join(missing, "\n")+"\n"...), 0o644)
}
