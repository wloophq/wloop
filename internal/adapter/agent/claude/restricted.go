package claude

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"sync"

	"github.com/wloophq/wloop/internal/core/turns"
)

const (
	restrictedFlag      = "--restricted"
	noSlashCommandsFlag = "--disable-slash-commands"
)

// hooksOff is the only settings a restricted call loads: --restricted still
// applies --settings, and hooks there would run code.
const hooksOff = `{"disableAllHooks":true}`

// restrictedProbes caches, per resolved binary path, whether that claude
// offers --restricted.
var restrictedProbes sync.Map

func headless(q turns.Ask) bool { return len(q.Tools) > 0 || len(q.Dirs) > 0 }

func checkHeadless(q turns.Ask) error {
	for _, t := range q.Tools {
		if !slices.Contains(turns.ReadOnlyTools, t) {
			return fmt.Errorf("claude: %q: %w", t, turns.ErrToolNotAllowed)
		}
	}
	for _, d := range q.Dirs {
		if !filepath.IsAbs(d) {
			return fmt.Errorf("claude: the call reads %q, which is not an absolute directory", d)
		}
	}
	return nil
}

// helpFlags is what the installed claude's --help offers of the flags a
// restricted call uses.
type helpFlags struct{ restricted, noSlashCommands bool }

// headlessArgs turns today's argv into a restricted one: no settings source,
// the given read-only tools, and each of Dirs as an extra working directory.
func headlessArgs(args []string, q turns.Ask, help helpFlags) []string {
	out := make([]string, 0, len(args)+2*len(q.Dirs)+5)
	for i := 0; i < len(args); i++ {
		switch args[i] {
		case "--setting-sources":
			i++
			out = append(out, restrictedFlag)
			for _, d := range q.Dirs {
				out = append(out, "--add-dir", d)
			}
			out = append(out, "--settings", hooksOff)
			if help.noSlashCommands {
				out = append(out, noSlashCommandsFlag)
			}
		case "--tools":
			i++
			out = append(out, "--tools", strings.Join(q.Tools, ","))
		default:
			out = append(out, args[i])
		}
	}
	return out
}

// probeKey is the binary a.Bin runs, symlinks resolved, so that an in-place
// upgrade through a versioned symlink is probed again.
func (a Asker) probeKey() string {
	p, err := exec.LookPath(a.Bin)
	if err != nil {
		return a.Bin
	}
	if r, err := filepath.EvalSymlinks(p); err == nil {
		return r
	}
	return p
}

func (a Asker) restricted(ctx context.Context) (helpFlags, error) {
	key := a.probeKey()
	help, found := restrictedProbes.Load(key)
	if !found {
		h, err := probeHelp(ctx, a.Sh, a.Dir, a.Bin)
		if err != nil {
			return helpFlags{}, err
		}
		restrictedProbes.Store(key, h)
		help = h
	}
	if h := help.(helpFlags); h.restricted {
		return h, nil
	}
	return helpFlags{}, fmt.Errorf("claude: %w (no %s)", turns.ErrNoRestricted, restrictedFlag)
}

func probeHelp(ctx context.Context, sh ShellFunc, dir, bin string) (helpFlags, error) {
	out, err := sh(ctx, dir, nil, "", bin, "--help")
	if errors.Is(err, exec.ErrNotFound) {
		return helpFlags{}, fmt.Errorf("claude: %w", turns.ErrNoAgent)
	}
	if err != nil {
		return helpFlags{}, fmt.Errorf("claude --help: %w", err)
	}
	var h helpFlags
	for _, f := range strings.Fields(string(out)) {
		h.restricted = h.restricted || f == restrictedFlag
		h.noSlashCommands = h.noSlashCommands || f == noSlashCommandsFlag
	}
	return h, nil
}

// RestrictedWarning is "" when the installed claude offers --restricted, else
// what doctor says about the headless turns it refuses.
func (k Agent) RestrictedWarning(ctx context.Context) (string, error) {
	if k.AskSh == nil {
		return "", errors.New("claude: the agent has no shell for headless calls (AskSh)")
	}
	help, err := probeHelp(ctx, k.AskSh, "", k.Binary())
	if err != nil || help.restricted {
		return "", err
	}
	version := "unknown"
	if out, err := k.AskSh(ctx, "", nil, "", k.Binary(), "--version"); err == nil {
		version = cmp.Or(firstLine(string(out)), version)
	}
	return fmt.Sprintf("claude %s lacks %s; headless turns are refused until it is updated", version, restrictedFlag), nil
}

// Ready says, spawning no call, whether q can run: its tools are read-only, its dirs absolute and, when it
// is restricted, the installed claude names --restricted.
func (a Asker) Ready(ctx context.Context, q turns.Ask) error {
	if err := checkHeadless(q); err != nil || !headless(q) {
		return err
	}
	if err := os.MkdirAll(a.Dir, 0o700); err != nil {
		return err
	}
	_, err := a.restricted(ctx)
	return err
}
