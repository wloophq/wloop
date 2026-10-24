package claude

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"maps"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/wloophq/wloop/internal/api"
	"github.com/wloophq/wloop/internal/backup"
	"github.com/wloophq/wloop/internal/brand"
	"github.com/wloophq/wloop/internal/core/setup"
)

// TurnEndTool is the turn_end tool of the loop's MCP server as Claude Code
// names it in permission rules.
var TurnEndTool = brand.ToolPrefix() + "turn_end"

// SlackSendTool posts as the user, so Claude Code asks before every call.
var SlackSendTool = brand.ToolPrefix() + "slack_send_message"

const (
	orcaHookPort  = "ORCA_AGENT_HOOK_PORT"
	hookTimeout   = 10
	writeAttempts = 5
)

var loopHooks = []struct{ event, sub string }{{"Stop", "hook stop"}, {"SessionStart", "hook session-start"}, {"UserPromptSubmit", "hook prompt"}}

// DefaultHome is the home Claude Code reads without CLAUDE_CONFIG_DIR.
func DefaultHome(userHome string) string { return filepath.Join(userHome, ".claude") }

// FindHomes lists the Claude homes of this machine: the default one,
// CLAUDE_CONFIG_DIR when set, and the homes the workspace manager keeps.
func FindHomes(userHome, configDir string, managerHomes []string) []string {
	out := []string{DefaultHome(userHome)}
	if configDir != "" {
		out = append(out, configDir)
	}
	return append(out, managerHomes...)
}

// Home installs the loop's MCP registration, its hooks and the turn_end
// permission in a Claude home.
type Home struct {
	// Command is the shell word that runs the loop in a hook.
	Command, MCPURL, DefaultHome string
	// MCPKey is the secret the MCP endpoint requires, "" before install writes one.
	MCPKey string
	Sh     ShellFunc
	Now    func() time.Time
}

func (h Home) isDefault(home string) bool { return isDefault(home, h.DefaultHome) }

func isDefault(home, defaultHome string) bool {
	return defaultHome != "" && filepath.Clean(home) == filepath.Clean(defaultHome)
}

// mcpFile is where Claude Code keeps the home's user-scope MCP servers.
func (h Home) mcpFile(home string) string { return claudeJSON(home, h.DefaultHome) }

// claudeJSON is the .claude.json Claude Code reads for home: the default
// home runs without CLAUDE_CONFIG_DIR and reads the one beside it.
func claudeJSON(home, defaultHome string) string {
	if isDefault(home, defaultHome) {
		return filepath.Join(filepath.Dir(filepath.Clean(defaultHome)), ".claude.json")
	}
	return filepath.Join(home, ".claude.json")
}

func settingsFile(home string) string { return filepath.Join(home, "settings.json") }

func (h Home) hookCommand(sub string) string { return h.Command + " " + sub + " || true" }

func (h Home) fix(home string) string { return setup.InstallCommand(h.Command, home) }

// headersHelper names the session to the MCP server: Claude Desktop sends a
// registration's ${VAR} headers unexpanded.
func (h Home) headersHelper() string { return h.Command + " hook mcp-headers" }

func (h Home) registration() map[string]any {
	return map[string]any{"type": "http", "url": h.MCPURL, "headersHelper": h.headersHelper(), "headers": map[string]any{api.MCPKeyHeader: h.MCPKey}}
}

type jsonFile struct {
	path   string
	raw    []byte
	exists bool
	doc    map[string]any
}

func readJSON(path string) (jsonFile, error) {
	f := jsonFile{path: path, doc: map[string]any{}}
	raw, err := os.ReadFile(path)
	switch {
	case errors.Is(err, fs.ErrNotExist):
		return f, nil
	case err != nil:
		return f, err
	}
	f.raw, f.exists = raw, true
	doc, err := decodeObject(raw)
	if err != nil {
		return f, fmt.Errorf("%s: %w", path, err)
	}
	f.doc = doc
	return f, nil
}

func decodeObject(raw []byte) (map[string]any, error) {
	doc := map[string]any{}
	if len(bytes.TrimSpace(raw)) == 0 {
		return doc, nil
	}
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.UseNumber()
	if err := dec.Decode(&doc); err != nil {
		return nil, err
	}
	if doc == nil {
		doc = map[string]any{}
	}
	return doc, nil
}

func encode(v any) ([]byte, error) {
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	enc.SetIndent("", "  ")
	if err := enc.Encode(v); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

func object(parent map[string]any, key string) map[string]any {
	m, _ := parent[key].(map[string]any)
	return m
}

// registrationProblem is "" when the entry is the loop's, right.
func (h Home) registrationProblem(doc map[string]any) string {
	entry, ok := object(doc, "mcpServers")[brand.MCPServer()]
	if !ok {
		return "missing"
	}
	var got struct {
		Type          string            `json:"type"`
		URL           string            `json:"url"`
		HeadersHelper string            `json:"headersHelper"`
		Headers       map[string]string `json:"headers"`
	}
	raw, _ := json.Marshal(entry)
	if err := json.Unmarshal(raw, &got); err != nil {
		return fmt.Sprintf("unreadable entry %s", raw)
	}
	switch {
	case got.Type != "http":
		return fmt.Sprintf("type is %q, want http", got.Type)
	case got.URL != h.MCPURL:
		return fmt.Sprintf("url is %s, want %s", got.URL, h.MCPURL)
	case got.HeadersHelper != h.headersHelper():
		return fmt.Sprintf("headersHelper is %q, want %q", got.HeadersHelper, h.headersHelper())
	case h.MCPKey == "":
		return "no MCP key in " + brand.Name + "'s state"
	}
	key, ok := got.Headers[api.MCPKeyHeader]
	switch {
	case !ok:
		return "header " + api.MCPKeyHeader + " is missing"
	case key != h.MCPKey:
		return "header " + api.MCPKeyHeader + " is stale"
	case len(got.Headers) != 1:
		return fmt.Sprintf("headers are %s, want only %s", strings.Join(slices.Sorted(maps.Keys(got.Headers)), ", "), api.MCPKeyHeader)
	}
	return ""
}

func ours(command, sub string) bool {
	return strings.Contains(command, brand.Name) && strings.Contains(command, sub)
}

func hookCommands(settings map[string]any, event string) []map[string]any {
	var out []map[string]any
	groups, _ := object(settings, "hooks")[event].([]any)
	for _, g := range groups {
		gm, _ := g.(map[string]any)
		hs, _ := gm["hooks"].([]any)
		for _, hk := range hs {
			if hm, ok := hk.(map[string]any); ok {
				out = append(out, hm)
			}
		}
	}
	return out
}

func commandOf(hook map[string]any) string {
	s, _ := hook["command"].(string)
	return s
}

func (h Home) rightHook(hook map[string]any, sub string) bool {
	return hook["type"] == "command" && commandOf(hook) == h.hookCommand(sub) && fmt.Sprint(hook["timeout"]) == fmt.Sprint(hookTimeout)
}

func (h Home) hookProblem(settings map[string]any, event, sub string) string {
	var found []map[string]any
	for _, hk := range hookCommands(settings, event) {
		if ours(commandOf(hk), sub) {
			found = append(found, hk)
		}
	}
	switch {
	case len(found) == 0:
		return "missing"
	case len(found) > 1:
		return fmt.Sprintf("%d "+brand.Name+" entries, want one", len(found))
	case !strings.HasSuffix(strings.TrimSpace(commandOf(found[0])), "|| true"):
		return fmt.Sprintf("%q has no `|| true`, so a failing "+brand.Name+" blocks the agent", commandOf(found[0]))
	case !h.rightHook(found[0], sub):
		return fmt.Sprintf("runs %q with timeout %v, want %q with timeout %d", commandOf(found[0]), found[0]["timeout"], h.hookCommand(sub), hookTimeout)
	}
	return ""
}

func allowList(settings map[string]any) []any { return permissionList(settings, "allow") }

func permissionList(settings map[string]any, key string) []any {
	l, _ := object(settings, "permissions")[key].([]any)
	return l
}

// addPermission appends tool to permissions.key and reports whether it was absent.
func addPermission(doc map[string]any, key, tool string) bool {
	list := permissionList(doc, key)
	if slices.Contains(list, any(tool)) {
		return false
	}
	perms := object(doc, "permissions")
	if perms == nil {
		perms = map[string]any{}
		doc["permissions"] = perms
	}
	perms[key] = append(list, tool)
	return true
}

// dropPermission removes tool from permissions.key, and the emptied containers, and reports whether it was there.
func dropPermission(doc map[string]any, key, tool string) bool {
	list := permissionList(doc, key)
	if !slices.Contains(list, any(tool)) {
		return false
	}
	perms := object(doc, "permissions")
	list = slices.DeleteFunc(slices.Clone(list), func(x any) bool { return x == tool })
	if len(list) == 0 {
		delete(perms, key)
	} else {
		perms[key] = list
	}
	if len(perms) == 0 {
		delete(doc, "permissions")
	}
	return true
}

func orcaHookEvents(settings map[string]any) []string {
	var out []string
	for _, event := range slices.Sorted(maps.Keys(object(settings, "hooks"))) {
		if slices.ContainsFunc(hookCommands(settings, event), func(hk map[string]any) bool { return strings.Contains(commandOf(hk), orcaHookPort) }) {
			out = append(out, event)
		}
	}
	return out
}

// Check reads the home's files and changes nothing.
func (h Home) Check(_ context.Context, home string) ([]setup.Finding, error) {
	mcp, err := readJSON(h.mcpFile(home))
	if err != nil {
		return nil, err
	}
	settings, err := readJSON(settingsFile(home))
	if err != nil {
		return nil, err
	}
	finding := func(entry, problem string) setup.Finding {
		f := setup.Finding{Home: home, Entry: entry, Problem: problem}
		if problem != "" {
			f.Fix = h.fix(home)
		}
		return f
	}
	out := []setup.Finding{finding("MCP server "+brand.MCPServer(), h.registrationProblem(mcp.doc))}
	for _, hk := range loopHooks {
		out = append(out, finding("hook "+hk.event, h.hookProblem(settings.doc, hk.event, hk.sub)))
	}
	perm := ""
	if !slices.Contains(allowList(settings.doc), any(TurnEndTool)) {
		perm = "missing"
	}
	out = append(out, finding("permission "+TurnEndTool, perm))
	ask := ""
	if !slices.Contains(permissionList(settings.doc, "ask"), any(SlackSendTool)) {
		ask = "missing"
	}
	out = append(out, finding("permission ask "+SlackSendTool, ask))
	if slices.Contains(allowList(settings.doc), any(SlackSendTool)) {
		out = append(out, finding("permission allow "+SlackSendTool, "permissions.allow holds it, so Slack posts go out as you without asking"))
	}
	if events := orcaHookEvents(settings.doc); len(events) > 0 {
		out = append(out, setup.Finding{Home: home, Entry: "Orca status hook",
			Problem: fmt.Sprintf("%s run Orca's status hook (%s), which moves the workspace card on every prompt and competes with "+brand.Name+"'s projection of the turn", strings.Join(events, ", "), orcaHookPort)})
	}
	return out, nil
}

// run is one install or remove in one home: it backs each file up once,
// before its first change, and keeps the lines it prints.
type run struct {
	h       Home
	home    string
	dryRun  bool
	lines   []string
	stamp   string
	backups map[string]bool
}

func (h Home) newRun(home string, dryRun bool) *run {
	now := time.Now
	if h.Now != nil {
		now = h.Now
	}
	return &run{h: h, home: home, dryRun: dryRun, stamp: now().Format("20060102-150405"), backups: map[string]bool{}}
}

func (r *run) say(done, would, rest string) {
	verb := done
	if r.dryRun {
		verb = "would " + would
	}
	r.lines = append(r.lines, verb+" "+rest)
}

func (r *run) backup(f jsonFile) error {
	if !f.exists || r.backups[f.path] {
		return nil
	}
	r.backups[f.path] = true
	if r.dryRun {
		r.say("backed up", "back up", f.path)
		return nil
	}
	dest, err := backup.File(f.path, f.raw, r.stamp)
	if err != nil {
		return err
	}
	r.say("backed up", "back up", f.path+" to "+dest)
	return nil
}

func (r *run) claude(ctx context.Context, args ...string) error {
	env := []string{"CLAUDE_CONFIG_DIR=" + r.home}
	if r.h.isDefault(r.home) {
		env = []string{"CLAUDE_CONFIG_DIR"}
	}
	out, err := r.h.Sh(ctx, "", env, "", "claude", args...)
	if err != nil {
		return fmt.Errorf("claude %s: %w: %s", strings.Join(args[:2], " "), err, strings.TrimSpace(string(out)))
	}
	return nil
}

// change edits a settings document and returns a line per entry it touched.
type change func(doc map[string]any) []string

func (h Home) installSettings(doc map[string]any) []string {
	var out []string
	for _, hk := range loopHooks {
		if verb := h.ensureHook(doc, hk.event, hk.sub); verb != "" {
			out = append(out, verb+" hook "+hk.event)
		}
	}
	if addPermission(doc, "allow", TurnEndTool) {
		out = append(out, "add permission "+TurnEndTool)
	}
	if addPermission(doc, "ask", SlackSendTool) {
		out = append(out, "add permission ask "+SlackSendTool)
	}
	if dropPermission(doc, "allow", SlackSendTool) {
		out = append(out, "remove permission allow "+SlackSendTool)
	}
	return out
}

// ensureHook keeps one right entry of the loop for event and every other hook;
// it returns "add", "change" or "" when nothing changed.
func (h Home) ensureHook(doc map[string]any, event, sub string) string {
	hooks := object(doc, "hooks")
	if hooks == nil {
		hooks = map[string]any{}
		doc["hooks"] = hooks
	}
	want := map[string]any{"type": "command", "command": h.hookCommand(sub), "timeout": hookTimeout}
	groups, _ := hooks[event].([]any)
	found, changed := false, false
	kept := []any{}
	for _, g := range groups {
		gm, ok := g.(map[string]any)
		hs, _ := gm["hooks"].([]any)
		if !ok || len(hs) == 0 {
			kept = append(kept, g)
			continue
		}
		var keep []any
		for _, hk := range hs {
			hm, _ := hk.(map[string]any)
			switch {
			case hm == nil || !ours(commandOf(hm), sub):
				keep = append(keep, hk)
			case !found && h.rightHook(hm, sub):
				found = true
				keep = append(keep, hk)
			case !found:
				found, changed = true, true
				keep = append(keep, want)
			default:
				changed = true
			}
		}
		if len(keep) > 0 {
			gm["hooks"] = keep
			kept = append(kept, gm)
		}
	}
	switch {
	case !found:
		hooks[event] = append(kept, map[string]any{"hooks": []any{want}})
		return "add"
	case changed:
		hooks[event] = kept
		return "change"
	}
	return ""
}

func removeSettings(doc map[string]any) []string {
	var out []string
	hooks := object(doc, "hooks")
	for _, hk := range loopHooks {
		groups, _ := hooks[hk.event].([]any)
		removed := false
		kept := []any{}
		for _, g := range groups {
			gm, ok := g.(map[string]any)
			hs, _ := gm["hooks"].([]any)
			if !ok || len(hs) == 0 {
				kept = append(kept, g)
				continue
			}
			keep := slices.DeleteFunc(slices.Clone(hs), func(x any) bool {
				hm, _ := x.(map[string]any)
				return hm != nil && ours(commandOf(hm), hk.sub)
			})
			if len(keep) == len(hs) {
				kept = append(kept, g)
				continue
			}
			removed = true
			if len(keep) > 0 {
				gm["hooks"] = keep
				kept = append(kept, gm)
			}
		}
		if !removed {
			continue
		}
		out = append(out, "remove hook "+hk.event)
		if len(kept) == 0 {
			delete(hooks, hk.event)
		} else {
			hooks[hk.event] = kept
		}
	}
	if hooks != nil && len(hooks) == 0 {
		delete(doc, "hooks")
	}
	if dropPermission(doc, "allow", TurnEndTool) {
		out = append(out, "remove permission "+TurnEndTool)
	}
	if dropPermission(doc, "ask", SlackSendTool) {
		out = append(out, "remove permission ask "+SlackSendTool)
	}
	return out
}

func clone(doc map[string]any) map[string]any {
	raw, _ := json.Marshal(doc)
	out, _ := decodeObject(raw)
	return out
}

func (r *run) entryLines(file string, entries []string) {
	for _, e := range entries {
		verb, rest, _ := strings.Cut(e, " ")
		done := map[string]string{"add": "added", "change": "changed", "remove": "removed"}[verb]
		prep := " in "
		if verb == "remove" {
			prep = " from "
		}
		r.say(done, verb, rest+prep+file)
	}
}

// rewrite applies c to the file at path, whose content was raw, and
// replaces it through any symlink, keeping its mode; it starts over from the
// file's new content when it changed since raw was read. It holds the lock
// Claude Code takes on the file while it compares and replaces.
func rewrite(ctx context.Context, path string, raw []byte, c change) error {
	return rewriteMode(ctx, path, raw, 0o644, c)
}

// rewriteMode is rewrite, creating a missing file with mode.
func rewriteMode(ctx context.Context, path string, raw []byte, mode os.FileMode, c change) error {
	target := path
	if resolved, err := filepath.EvalSymlinks(path); err == nil {
		target = resolved
	}
	if fi, err := os.Stat(target); err == nil {
		mode = fi.Mode().Perm()
	}
	for range writeAttempts {
		doc, err := decodeObject(raw)
		if err != nil {
			return fmt.Errorf("%s: %w", path, err)
		}
		if len(c(doc)) == 0 {
			return nil
		}
		body, err := encode(doc)
		if err != nil {
			return err
		}
		if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
			return err
		}
		tmp, err := writeTemp(target, body, mode)
		if err != nil {
			return err
		}
		replaced, now, err := replaceLocked(ctx, path, target, tmp, raw)
		if err != nil || replaced {
			return err
		}
		raw = now
	}
	return fmt.Errorf("%s %w", path, errKeptChanging)
}

// replaceLocked renames tmp over target when target still holds raw, and
// otherwise returns its new content.
func replaceLocked(ctx context.Context, path, target, tmp string, raw []byte) (bool, []byte, error) {
	unlock, err := lockFile(ctx, path)
	if err != nil {
		os.Remove(tmp)
		return false, nil, err
	}
	defer unlock()
	now, err := os.ReadFile(target)
	if err != nil && !errors.Is(err, fs.ErrNotExist) {
		os.Remove(tmp)
		return false, nil, err
	}
	if !bytes.Equal(now, raw) {
		os.Remove(tmp)
		return false, now, nil
	}
	if err := os.Rename(tmp, target); err != nil {
		os.Remove(tmp)
		return false, nil, err
	}
	return true, nil, nil
}

const (
	lockStale = 10 * time.Second
	lockPoll  = 50 * time.Millisecond
)

var lockWait = 5 * time.Second

// lockFile takes the lock Claude Code (proper-lockfile) takes on path: the
// directory <path>.lock, stale once its mtime is older than lockStale.
func lockFile(ctx context.Context, path string) (func(), error) {
	lock := path + ".lock"
	deadline := time.Now().Add(lockWait)
	for {
		err := os.Mkdir(lock, 0o755)
		if err == nil {
			return releaser(lock), nil
		}
		if !errors.Is(err, fs.ErrExist) {
			return nil, err
		}
		if breakStale(lock) {
			continue
		}
		if time.Now().After(deadline) {
			return nil, fmt.Errorf("%s stays locked by another process (%s)", path, lock)
		}
		select {
		case <-ctx.Done():
			return nil, fmt.Errorf("%s is locked (%s): %w", path, lock, ctx.Err())
		case <-time.After(lockPoll):
		}
	}
}

// breakStale moves a stale lock aside before removing it, so a waiter
// never removes the fresh lock another waiter has just taken.
func breakStale(lock string) bool {
	fi, err := os.Stat(lock)
	if err != nil || time.Since(fi.ModTime()) <= lockStale {
		return false
	}
	aside := fmt.Sprintf("%s.stale-%d-%d", lock, os.Getpid(), time.Now().UnixNano())
	if err := os.Rename(lock, aside); err != nil {
		return false
	}
	os.RemoveAll(aside)
	return true
}

// releaser removes lock only while it is still the directory this process
// created. It holds the directory open until then: Linux hands a removed
// directory's inode to the next mkdir, and an open one is never reused.
func releaser(lock string) func() {
	held, err := os.Open(lock)
	return func() {
		if err != nil {
			return
		}
		defer held.Close()
		mine, err := held.Stat()
		if err != nil {
			return
		}
		if now, err := os.Stat(lock); err == nil && os.SameFile(mine, now) {
			os.Remove(lock)
		}
	}
}

var errKeptChanging = errors.New("kept changing while " + brand.Name + " wrote it")

func writeTemp(target string, body []byte, mode os.FileMode) (string, error) {
	f, err := os.CreateTemp(filepath.Dir(target), "."+filepath.Base(target)+"-*.tmp")
	if err != nil {
		return "", err
	}
	_, werr := f.Write(body)
	if err := errors.Join(werr, f.Close(), os.Chmod(f.Name(), mode)); err != nil {
		os.Remove(f.Name())
		return "", err
	}
	return f.Name(), nil
}

// Install registers the loop's MCP server, adds both hooks and the turn_end
// permission, and backs up every file before it first changes it.
func (h Home) Install(ctx context.Context, home string, dryRun bool) ([]string, error) {
	if h.MCPKey == "" {
		return nil, errors.New("no MCP key to register")
	}
	return h.apply(ctx, home, dryRun, false, h.installSettings, func(r *run, mcp jsonFile) (string, func() error) {
		switch problem := h.registrationProblem(mcp.doc); problem {
		case "":
			return "", nil
		case "missing":
			return "add", func() error { return r.addServer(ctx, mcp) }
		default:
			return "change", func() error { return r.addServer(ctx, mcp) }
		}
	})
}

// Files are the files Install and Remove may change for home.
func (h Home) Files(home string) []string { return []string{settingsFile(home), h.mcpFile(home)} }

// Remove takes out what Install adds and backs up every file it changes.
func (h Home) Remove(ctx context.Context, home string, dryRun bool) ([]string, error) {
	return h.apply(ctx, home, dryRun, true, removeSettings, func(r *run, mcp jsonFile) (string, func() error) {
		if _, ok := object(mcp.doc, "mcpServers")[brand.MCPServer()]; !ok {
			return "", nil
		}
		return "remove", func() error { return r.removeServer(ctx, brand.MCPServer()) }
	})
}

// addServer writes the loop's entry into mcp's mcpServers, the way `claude
// mcp add-json --scope user` does, without the key ever reaching an argv.
func (r *run) addServer(ctx context.Context, mcp jsonFile) error {
	entry := r.h.registration()
	return rewriteMode(ctx, mcp.path, mcp.raw, 0o600, func(doc map[string]any) []string {
		if r.h.registrationProblem(doc) == "" {
			return nil
		}
		servers := object(doc, "mcpServers")
		if servers == nil {
			servers = map[string]any{}
			doc["mcpServers"] = servers
		}
		servers[brand.MCPServer()] = clone(entry)
		return []string{"set " + brand.MCPServer()}
	})
}

func (r *run) removeServer(ctx context.Context, name string) error {
	return r.claude(ctx, "mcp", "remove", "--scope", "user", name)
}

// serverStep names the change to the MCP registration, "" for none, and
// the call that makes it.
type serverStep func(r *run, mcp jsonFile) (string, func() error)

// apply makes the server step, then the settings change; with keepGoing a
// failed server step still lets the settings change run.
func (h Home) apply(ctx context.Context, home string, dryRun, keepGoing bool, c change, step serverStep) ([]string, error) {
	r := h.newRun(home, dryRun)
	settings, err := readJSON(settingsFile(home))
	if err != nil {
		return nil, err
	}
	mcp, err := readJSON(h.mcpFile(home))
	if err != nil {
		return nil, err
	}
	entries := c(clone(settings.doc))
	verb, do := step(r, mcp)
	if verb != "" {
		if err := r.backup(mcp); err != nil {
			return r.lines, err
		}
	}
	if len(entries) > 0 {
		if err := r.backup(settings); err != nil {
			return r.lines, err
		}
	}
	var serverErr error
	if verb != "" {
		if !dryRun {
			serverErr = do()
		}
		switch {
		case serverErr == nil:
			r.entryLines(mcp.path, []string{verb + " MCP server " + brand.MCPServer()})
		case !keepGoing:
			return r.lines, serverErr
		}
	}
	if len(entries) == 0 {
		return r.lines, serverErr
	}
	if !dryRun {
		if err := rewrite(ctx, settings.path, settings.raw, c); err != nil {
			if errors.Is(err, errKeptChanging) {
				err = fmt.Errorf("%w; run install again", err)
			}
			return r.lines, errors.Join(serverErr, err)
		}
	}
	r.entryLines(settings.path, entries)
	return r.lines, serverErr
}
