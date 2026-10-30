package archtest

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/wloophq/wloop/internal/api"
	"github.com/wloophq/wloop/internal/brand"
	"github.com/wloophq/wloop/internal/brand/brandtest"
	"github.com/wloophq/wloop/pkg/plugin/manifest"
)

// standinDir holds the stand-in source plugin: its code, its manifest and
// its profile layer.
const standinDir = "internal/archtest/testdata/standin"

// standinTest is this file, which builds and runs the stand-in.
const standinTest = "internal/archtest/standin_test.go"

// ruleNamedOnlyAtHome flags a file outside home, other than the files of
// own, that spells the plugin id, alone or as the head of a name such as
// <id>.<fact> or <id>_<tool>, other than in the path of home: a plugin that needs a line
// outside its own directory to run is not external.
func ruleNamedOnlyAtHome(files map[string][]byte, id, home string, own ...string) []string {
	word := regexp.MustCompile(`(^|[^A-Za-z0-9])` + regexp.QuoteMeta(id) + `([^A-Za-z0-9]|$)`)
	var out []string
	for _, f := range slices.Sorted(func(yield func(string) bool) {
		for f := range files {
			if !yield(f) {
				return
			}
		}
	}) {
		if under(f, home) || slices.Contains(own, f) {
			continue
		}
		if word.Match(bytes.ReplaceAll(files[f], []byte(home), nil)) {
			out = append(out, fmt.Sprintf("%s names the %s plugin, whose files all live in %s; a plugin runs with no edit outside its directory (C4)", f, id, home))
		}
	}
	return out
}

// TestAStandInSourcePluginRunsWithNoEditOutsideItsDirectory checks that a
// source plugin whose code, manifest and profile layer all live in
// internal/archtest/testdata/standin, and which no other file of the
// repository names, runs in the daemon from the user's plugins directory
// through the real launcher: its manifest's fact, temporal fact, delivery,
// task kind and signal merge into the host's tables, its profile layer's
// workflow matches the unit it reports, its own store opens, and its MCP tool
// answers in the words of its layer's tools.toml.
func TestAStandInSourcePluginRunsWithNoEditOutsideItsDirectory(t *testing.T) {
	t.Parallel()
	root := filepath.Join("..", "..")
	m, err := manifest.Load(filepath.Join(root, standinDir))
	if err != nil {
		t.Fatal(err)
	}
	for _, b := range ruleNamedOnlyAtHome(repositoryFiles(t, root), m.ID, standinDir, standinTest) {
		t.Error(b)
	}

	dir, err := os.MkdirTemp("/tmp", "standin")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.RemoveAll(dir) })
	bin := filepath.Join(dir, "build", brand.Name)
	goBuild(t, root, bin, "./cmd/"+brandtest.DefaultName, "-ldflags", brandLDFlags())
	iso := filepath.Join(dir, "iso")
	isolatedScript(t, root, "setup", iso, bin, "D000000TEST", "9")
	t.Cleanup(func() { isolatedQuiet(root, "stop", iso) })

	plugins := filepath.Join(iso, "config", brand.StateDir(), "plugins", m.ID)
	goBuild(t, root, filepath.Join(plugins, brand.PluginBinary(m.ID)), "./"+standinDir)
	if out, err := exec.Command("cp", "-R", filepath.Join(root, standinDir, manifest.File), filepath.Join(root, standinDir, "profile"), plugins).CombinedOutput(); err != nil {
		t.Fatalf("placing the stand-in: %v\n%s", err, out)
	}
	mcpPort := useFreePorts(t, filepath.Join(iso, "config", brand.StateDir(), "config.toml"), m.ID)
	isolatedScript(t, root, "start", iso)

	state := filepath.Join(iso, "state", brand.StateDir())
	want := []string{"plugin " + m.ID + ": runs " + filepath.Join(plugins, brand.PluginBinary(m.ID)) + ", trusted by its path", "plugin " + m.ID + ": loaded"}
	logged := waitForLog(t, filepath.Join(iso, "daemon.log"), want)
	for _, w := range want {
		if !strings.Contains(logged, w) {
			t.Fatalf("the daemon's log lacks %q:\n%s", w, logged)
		}
	}
	if _, err := os.Stat(filepath.Join(state, "plugins", m.ID, "plugin.db")); err != nil {
		t.Errorf("the stand-in opened no store of its own: %v", err)
	}

	plan := isolatedScript(t, root, "run", iso, filepath.Join(iso, "bin", brand.Name), "plan", "--unit", m.ID+":attic/1")
	if !strings.Contains(plan, "standin-shelved  "+m.ID+":attic/1") {
		t.Errorf("the plan of the stand-in's unit lacks its layer's workflow over its reported facts:\n%s", plan)
	}
	refused := isolatedRun(root, "run", iso, filepath.Join(iso, "bin", brand.Name), "task", "new", "--title", "Sort the shelf", "--prop", "kind=nonsense")
	if !strings.Contains(refused, ", note") {
		t.Errorf("the task kinds lack the stand-in's kind:\n%s", refused)
	}

	key, err := os.ReadFile(filepath.Join(state, "mcp.key"))
	if err != nil {
		t.Fatal(err)
	}
	c := &mcpClient{t: t, url: "http://127.0.0.1:" + strconv.Itoa(mcpPort) + "/mcp", key: strings.TrimSpace(string(key))}
	c.initialize()
	tools := c.call("tools/list", nil)
	var listed struct{ Tools []listedTool }
	if err := json.Unmarshal(tools, &listed); err != nil {
		t.Fatal(err)
	}
	i := slices.IndexFunc(listed.Tools, func(tl listedTool) bool { return tl.Name == "standin_note" })
	if i < 0 {
		t.Fatalf("the MCP server lists no standin_note: %s", tools)
	}
	if tl := listed.Tools[i]; tl.Description != "Keep a short note on the stand-in's shelf." || tl.InputSchema.Properties["text"].Description != "the note" {
		t.Errorf("standin_note is not described by its layer's tools.toml: %+v", tl)
	}
	for _, step := range []struct {
		args, text string
		isError    bool
	}{
		{`{"text":"Water the ferns"}`, `Kept "Water the ferns"; the shelf holds 1 notes.`, false},
		{`{"text":" "}`, "text: required, not empty", true},
		{`{"text":"Dust the frames"}`, `Kept "Dust the frames"; the shelf holds 2 notes.`, false},
	} {
		var res struct {
			Content []struct{ Text string }
			IsError bool
		}
		raw := c.call("tools/call", map[string]any{"name": "standin_note", "arguments": json.RawMessage(step.args)})
		if err := json.Unmarshal(raw, &res); err != nil || len(res.Content) != 1 {
			t.Fatalf("tools/call %s: %v %s", step.args, err, raw)
		}
		if res.Content[0].Text != step.text || res.IsError != step.isError {
			t.Errorf("tools/call %s: %q (error %v), want %q (error %v)", step.args, res.Content[0].Text, res.IsError, step.text, step.isError)
		}
	}
}

// listedTool is a tool as the MCP server lists it.
type listedTool struct {
	Name        string
	Description string
	InputSchema struct {
		Properties map[string]struct{ Description string }
	}
}

// repositoryFiles are the repository's files git tracks or would track, by
// path from root.
func repositoryFiles(t *testing.T, root string) map[string][]byte {
	t.Helper()
	cmd := exec.Command("git", "ls-files", "-z", "--cached", "--others", "--exclude-standard")
	cmd.Dir = root
	out, err := cmd.Output()
	if err != nil {
		t.Fatal(err)
	}
	files := map[string][]byte{}
	for _, f := range strings.Split(strings.TrimRight(string(out), "\x00"), "\x00") {
		raw, err := os.ReadFile(filepath.Join(root, f))
		if err != nil {
			if os.IsNotExist(err) {
				continue
			}
			t.Fatal(err)
		}
		files[f] = raw
	}
	return files
}

func goBuild(t *testing.T, root, out, pkg string, flags ...string) {
	t.Helper()
	cmd := exec.Command("go", slices.Concat([]string{"build"}, flags, []string{"-o", out, pkg})...)
	cmd.Dir = root
	if raw, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("go build %s: %v\n%s", pkg, err, raw)
	}
}

// brandLDFlags build a binary with this test binary's brand.
func brandLDFlags() string {
	const pkg = "github.com/wloophq/wloop/internal/brand."
	return fmt.Sprintf(`-X %sName=%s -X "%sCommand=%s" -X %sApp=%s -X %sOrg=%s`, pkg, brand.Name, pkg, brand.Command, pkg, brand.App, pkg, brand.Org)
}

func isolatedRun(root string, args ...string) string {
	cmd := exec.Command("bash", append([]string{filepath.Join(root, "scripts", "isolated-daemon.sh")}, args...)...)
	out, _ := cmd.CombinedOutput()
	return string(out)
}

func isolatedScript(t *testing.T, root string, args ...string) string {
	t.Helper()
	cmd := exec.Command("bash", append([]string{filepath.Join(root, "scripts", "isolated-daemon.sh")}, args...)...)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("isolated-daemon.sh %s: %v\n%s", strings.Join(args, " "), err, out)
	}
	return string(out)
}

func isolatedQuiet(root string, args ...string) { isolatedRun(root, args...) }

// useFreePorts points the isolated config's HTTP and MCP ports at ports
// free now, and turns plugin id on, trusted by its path; it returns the MCP
// port.
func useFreePorts(t *testing.T, config, id string) int {
	t.Helper()
	raw, err := os.ReadFile(config)
	if err != nil {
		t.Fatal(err)
	}
	ports := regexp.MustCompile(`(?m)^port = \d+$`)
	var free []int
	src := ports.ReplaceAllStringFunc(string(raw), func(string) string {
		p := freePort(t)
		free = append(free, p)
		return "port = " + strconv.Itoa(p)
	})
	if len(free) != 2 {
		t.Fatalf("the isolated config has %d ports, want the HTTP and the MCP one:\n%s", len(free), raw)
	}
	src += "\n[plugins." + id + "]\nenabled = true\ntrust = \"path\"\n"
	if err := os.WriteFile(config, []byte(src), 0o600); err != nil {
		t.Fatal(err)
	}
	return free[1]
}

func freePort(t *testing.T) int {
	t.Helper()
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer l.Close()
	return l.Addr().(*net.TCPAddr).Port
}

// waitForLog polls log until it holds every line of want; the deadline only
// guards a hang.
func waitForLog(t *testing.T, log string, want []string) string {
	t.Helper()
	var got string
	for deadline := time.Now().Add(90 * time.Second); time.Now().Before(deadline); time.Sleep(100 * time.Millisecond) {
		raw, _ := os.ReadFile(log)
		got = string(raw)
		if !slices.ContainsFunc(want, func(w string) bool { return !strings.Contains(got, w) }) {
			break
		}
	}
	return got
}

// mcpClient speaks JSON-RPC to the daemon's MCP endpoint, as an agent's
// session does.
type mcpClient struct {
	t        *testing.T
	url, key string
	session  string
	id       int
}

func (c *mcpClient) initialize() {
	c.t.Helper()
	h, _ := c.post("initialize", map[string]any{"protocolVersion": "2025-06-18", "capabilities": map[string]any{}, "clientInfo": map[string]any{"name": "standin", "version": "1"}})
	if c.session = h.Get("Mcp-Session-Id"); c.session == "" {
		c.t.Fatal("initialize: no session")
	}
}

func (c *mcpClient) call(method string, params any) json.RawMessage {
	c.t.Helper()
	_, raw := c.post(method, params)
	var body struct {
		Result json.RawMessage
		Error  *struct{ Message string }
	}
	if err := json.Unmarshal(raw, &body); err != nil || body.Error != nil {
		c.t.Fatalf("%s: %v %+v", method, err, body.Error)
	}
	return body.Result
}

// post sends one request and returns the response's header and body.
func (c *mcpClient) post(method string, params any) (http.Header, []byte) {
	c.t.Helper()
	c.id++
	raw, err := json.Marshal(map[string]any{"jsonrpc": "2.0", "id": c.id, "method": method, "params": params})
	if err != nil {
		c.t.Fatal(err)
	}
	req, err := http.NewRequest(http.MethodPost, c.url, bytes.NewReader(raw))
	if err != nil {
		c.t.Fatal(err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json, text/event-stream")
	req.Header.Set(api.MCPKeyHeader, c.key)
	if c.session != "" {
		req.Header.Set("Mcp-Session-Id", c.session)
	}
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		c.t.Fatal(err)
	}
	body, err := io.ReadAll(res.Body)
	res.Body.Close()
	if err != nil || res.StatusCode != http.StatusOK {
		c.t.Fatalf("%s: %v %s %s", method, err, res.Status, body)
	}
	return res.Header, body
}
