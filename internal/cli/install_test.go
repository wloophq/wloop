package cli

import (
	"bytes"
	"context"
	"encoding/xml"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/wloophq/wloop/internal/brand"
	"github.com/wloophq/wloop/internal/brand/brandtest"
	"github.com/wloophq/wloop/internal/config"
	"github.com/wloophq/wloop/internal/daemon"
	"github.com/wloophq/wloop/internal/workspace/fake"
)

func TestInstalledIntervalReadsTheConfig(t *testing.T) {
	te := newTestEnv(t, &fake.Manager{})
	te.cfg.Sweep.Fast = config.Days(5 * time.Minute)
	if _, ok := te.installedInterval(); ok {
		t.Fatal("reported an interval with no plist")
	}
	writePlist(t, te, te.renderPlist(daemonAgent(te.Executable), map[string]string{"XDG_STATE_HOME": "/state"}))
	if got, ok := te.installedInterval(); !ok || got != 5*time.Minute {
		t.Fatalf("installedInterval = %s, %v", got, ok)
	}
}

func TestAppInstallNoMenuBarRunsServe(t *testing.T) {
	te := newTestEnv(t, &fake.Manager{})
	sh := fakeLaunchctl(t, te, fakeShell{})
	te.Executable = "/opt/gh & <" + brand.Name + ">/" + brand.StateDir()
	t.Setenv("XDG_STATE_HOME", "/state & <co>")
	t.Setenv("XDG_CONFIG_HOME", "")
	t.Setenv(daemon.ReadModelCheckEnv, "")
	if code := te.run("app", "install", "--no-menu-bar"); code != 0 {
		t.Fatalf("exit %d: %s", code, te.err)
	}
	raw, err := os.ReadFile(te.plistPath())
	if err != nil {
		t.Fatal(err)
	}
	p := parsePlist(t, raw)
	want := []string{shellPath(), "-lc", `exec "$0" serve`, "/opt/gh & <" + brand.Name + ">/" + brand.StateDir()}
	if got := p["ProgramArguments"]; !slices.Equal(got.([]string), want) {
		t.Errorf("ProgramArguments = %q, want %q", got, want)
	}
	if p["Label"] != launchAgentLabel || p["KeepAlive"] != true || p["RunAtLoad"] != true || p["ThrottleInterval"] != 30 {
		t.Errorf("plist = %v", p)
	}
	if timeout, _ := p["ExitTimeOut"].(int); time.Duration(timeout)*time.Second <= daemon.ShutdownGrace {
		t.Errorf("ExitTimeOut = %v, not above the daemon's shutdown grace", p["ExitTimeOut"])
	}
	if _, ok := p["StartInterval"]; ok {
		t.Error("the serve agent has a StartInterval")
	}
	if p["StandardOutPath"] != te.StateDir+"/serve.out.log" || p["StandardErrorPath"] != te.StateDir+"/serve.err.log" {
		t.Errorf("logs: %v, %v", p["StandardOutPath"], p["StandardErrorPath"])
	}
	env, _ := p["EnvironmentVariables"].(map[string]any)
	if len(env) != 1 || env["XDG_STATE_HOME"] != "/state & <co>" {
		t.Errorf("EnvironmentVariables = %v", env)
	}
	if len(sh.calls) == 0 {
		t.Fatal("launchctl not called")
	}
}

func TestAppInstallWritesTwoAgents(t *testing.T) {
	te, calls := bundleEnv(t)
	oldGOOS, oldWait := goos, bootoutWait
	goos, bootoutWait = "darwin", 0
	t.Cleanup(func() { goos, bootoutWait = oldGOOS, oldWait })
	t.Setenv("HOME", te.Home)
	if code := te.run("app", "install"); code != 0 {
		t.Fatalf("exit %d: %s", code, te.err)
	}
	daemonPlist, appPlist := te.plistPath(), te.agentPath(launchAgentLabel+".app")
	raw, err := os.ReadFile(daemonPlist)
	if err != nil {
		t.Fatal(err)
	}
	p := parsePlist(t, raw)
	if want := []string{shellPath(), "-lc", `exec "$0" serve`, te.Executable}; !slices.Equal(p["ProgramArguments"].([]string), want) {
		t.Errorf("daemon ProgramArguments = %q, want %q", p["ProgramArguments"], want)
	}
	if p["Label"] != launchAgentLabel || p["KeepAlive"] != true || !strings.Contains(string(raw), "<key>KeepAlive</key>\n    <true/>") || p["StandardErrorPath"] != te.StateDir+"/serve.err.log" {
		t.Errorf("daemon plist:\n%s", raw)
	}
	raw, err = os.ReadFile(appPlist)
	if err != nil {
		t.Fatal(err)
	}
	p = parsePlist(t, raw)
	if want := []string{shellPath(), "-lc", `exec "$0" app`, bundleExe(te.Home)}; !slices.Equal(p["ProgramArguments"].([]string), want) {
		t.Errorf("app ProgramArguments = %q, want %q", p["ProgramArguments"], want)
	}
	if ka, ok := p["KeepAlive"].(map[string]any); !ok || ka["SuccessfulExit"] != false {
		t.Errorf("app KeepAlive = %#v, want {SuccessfulExit: false}", p["KeepAlive"])
	}
	if p["Label"] != launchAgentLabel+".app" || p["StandardOutPath"] != te.StateDir+"/app.out.log" || p["StandardErrorPath"] != te.StateDir+"/app.err.log" {
		t.Errorf("app plist:\n%s", raw)
	}
	var bootstraps []string
	for _, c := range *calls {
		if c.name == "launchctl" && strings.HasPrefix(c.args, "bootstrap ") {
			bootstraps = append(bootstraps, c.args)
		}
	}
	domain := fmt.Sprintf("gui/%d", os.Getuid())
	if want := []string{"bootstrap " + domain + " " + daemonPlist, "bootstrap " + domain + " " + appPlist}; !slices.Equal(bootstraps, want) {
		t.Fatalf("bootstraps %q, want %q", bootstraps, want)
	}
}

func TestAppInstallNoMenuBarOmitsUnsetEnvironment(t *testing.T) {
	te := newTestEnv(t, &fake.Manager{})
	fakeLaunchctl(t, te, fakeShell{})
	te.Executable = "/opt/" + brand.StateDir() + "/" + brand.StateDir()
	t.Setenv("XDG_STATE_HOME", "")
	t.Setenv("XDG_CONFIG_HOME", "")
	if code := te.run("app", "install", "--no-menu-bar"); code != 0 {
		t.Fatalf("exit %d: %s", code, te.err)
	}
	raw, _ := os.ReadFile(te.plistPath())
	if p := parsePlist(t, raw); p["EnvironmentVariables"] != nil {
		t.Fatalf("EnvironmentVariables = %v", p["EnvironmentVariables"])
	}
}

func TestAppInstallNoMenuBarReplacesTheInstalledAgent(t *testing.T) {
	te := newTestEnv(t, &fake.Manager{})
	writePlist(t, te, te.renderPlist(daemonAgent("/opt/previous/"+brand.Name), nil))
	sh := fakeLaunchctl(t, te, fakeShell{printsUntilGone: 2})
	if code := te.run("app", "install", "--no-menu-bar"); code != 0 {
		t.Fatalf("exit %d: %s", code, te.err)
	}
	target := fmt.Sprintf("gui/%d/%s", os.Getuid(), launchAgentLabel)
	bootstrap := fmt.Sprintf("launchctl bootstrap gui/%d %s", os.Getuid(), te.plistPath())
	print := "launchctl print " + target
	want := []string{"launchctl bootout " + target, print, print, print, bootstrap}
	if !slices.Equal(sh.launchctl(), want) {
		t.Fatalf("launchctl calls %q, want %q", sh.launchctl(), want)
	}
	if raw, _ := os.ReadFile(te.plistPath()); strings.Contains(string(raw), "/opt/previous/") {
		t.Fatalf("the previous plist is left in place:\n%s", raw)
	}
}

func TestAppInstallNoMenuBarBoundsTheBootoutWait(t *testing.T) {
	te := newTestEnv(t, &fake.Manager{})
	sh := fakeLaunchctl(t, te, fakeShell{printsUntilGone: -1, failBootstraps: 1})
	bootoutWait = 20 * time.Millisecond
	if code := te.run("app", "install", "--no-menu-bar"); code != 0 {
		t.Fatalf("exit %d: %s", code, te.err)
	}
	var prints, bootstraps int
	for _, c := range sh.launchctl() {
		switch {
		case strings.HasPrefix(c, "launchctl print "):
			prints++
		case strings.HasPrefix(c, "launchctl bootstrap "):
			bootstraps++
		}
	}
	if prints == 0 || bootstraps != 2 {
		t.Fatalf("launchctl calls %q", sh.launchctl())
	}
}

func TestAppInstallNoMenuBarWarnsAboutATemporaryBuild(t *testing.T) {
	te := newTestEnv(t, &fake.Manager{})
	fakeLaunchctl(t, te, fakeShell{})
	te.Executable = filepath.Join(os.TempDir(), "go-build123", "exe", brand.StateDir())
	if code := te.run("app", "install", "--no-menu-bar"); code != 0 {
		t.Fatalf("exit %d: %s", code, te.err)
	}
	if !strings.Contains(te.err.String(), "temporary") {
		t.Fatalf("no warning about the temporary build:\n%s", te.err)
	}
}

func writePlist(t *testing.T, te *testEnv, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(te.plistPath()), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(te.plistPath(), []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

type fakeShell struct {
	calls []string
	// printsUntilGone is how many 'launchctl print' calls still find the job
	// after the bootout; -1 keeps it forever.
	printsUntilGone int
	// failBootstraps is how many bootstrap calls fail before one succeeds.
	failBootstraps int
}

func (f *fakeShell) launchctl() []string {
	var out []string
	for _, c := range f.calls {
		if strings.HasPrefix(c, "launchctl ") {
			out = append(out, c)
		}
	}
	return out
}

func fakeLaunchctl(t *testing.T, te *testEnv, f fakeShell) *fakeShell {
	prevOS, prevRetry, prevWait, prevPoll := goos, bootstrapRetry, bootoutWait, bootoutPoll
	goos, bootstrapRetry, bootoutPoll = "darwin", 0, 0
	t.Cleanup(func() { goos, bootstrapRetry, bootoutWait, bootoutPoll = prevOS, prevRetry, prevWait, prevPoll })
	te.Sh = func(_ context.Context, name string, args ...string) ([]byte, error) {
		f.calls = append(f.calls, strings.Join(append([]string{name}, args...), " "))
		switch {
		case name != "launchctl":
		case args[0] == "print" && f.printsUntilGone != 0:
			if f.printsUntilGone > 0 {
				f.printsUntilGone--
			}
			return []byte("state = running\n"), nil
		case args[0] == "print":
			return nil, errors.New("Could not find service")
		case args[0] == "bootstrap" && f.failBootstraps > 0:
			f.failBootstraps--
			return nil, errors.New("Bootstrap failed: 5: Input/output error")
		default:
			return nil, nil
		}
		return nil, fmt.Errorf("unexpected %s %v", name, args)
	}
	return &f
}

// parsePlist reads a flat plist dict: strings, integers, booleans, string
// arrays and one level of nested dict.
func parsePlist(t *testing.T, raw []byte) map[string]any {
	t.Helper()
	d := xml.NewDecoder(bytes.NewReader(raw))
	var root map[string]any
	var stack []map[string]any
	var key string
	var arr []string
	inArray := false
	for {
		tok, err := d.Token()
		if err == io.EOF {
			break
		}
		if err != nil {
			t.Fatal(err)
		}
		se, ok := tok.(xml.StartElement)
		if !ok {
			if ee, ok := tok.(xml.EndElement); ok {
				switch ee.Name.Local {
				case "dict":
					stack = stack[:len(stack)-1]
				case "array":
					inArray = false
					stack[len(stack)-1][key] = arr
				}
			}
			continue
		}
		text := func() string {
			var s string
			if err := d.DecodeElement(&s, &se); err != nil {
				t.Fatal(err)
			}
			return s
		}
		switch se.Name.Local {
		case "dict":
			m := map[string]any{}
			if root == nil {
				root = m
			} else {
				stack[len(stack)-1][key] = m
			}
			stack = append(stack, m)
		case "array":
			inArray, arr = true, []string{}
		case "key":
			key = text()
		case "string":
			if inArray {
				arr = append(arr, text())
			} else {
				stack[len(stack)-1][key] = text()
			}
		case "integer":
			n, err := strconv.Atoi(text())
			if err != nil {
				t.Fatal(err)
			}
			stack[len(stack)-1][key] = n
		case "true", "false":
			stack[len(stack)-1][key] = se.Name.Local == "true"
		}
	}
	return root
}

func TestAppInstallNamesOnlyTheBrand(t *testing.T) {
	if brandtest.IsDefault() {
		t.Skip("built with the default brand")
	}
	te, _ := bundleEnv(t)
	oldGOOS, oldWait := goos, bootoutWait
	goos, bootoutWait = "darwin", 0
	t.Cleanup(func() { goos, bootoutWait = oldGOOS, oldWait })
	t.Setenv("HOME", te.Home)
	if code := te.run("app", "install"); code != 0 {
		t.Fatalf("exit %d: %s", code, te.err)
	}
	found := []string{te.out.String(), te.err.String()}
	filepath.WalkDir(te.Home, func(p string, d fs.DirEntry, err error) error {
		if err == nil && !d.IsDir() {
			raw, _ := os.ReadFile(p)
			found = append(found, p+"\n"+string(raw))
		}
		return nil
	})
	for _, text := range found {
		if brandtest.Spells(text) {
			t.Errorf("app install wrote the default brand:\n%s", text)
		}
	}
}

func TestAppInstallNoMenuBarForwardsTheReadModelCheck(t *testing.T) {
	te := newTestEnv(t, &fake.Manager{})
	fakeLaunchctl(t, te, fakeShell{})
	t.Setenv("XDG_STATE_HOME", "")
	t.Setenv("XDG_CONFIG_HOME", "")
	t.Setenv(daemon.ReadModelCheckEnv, "1")
	if code := te.run("app", "install", "--no-menu-bar"); code != 0 {
		t.Fatalf("exit %d: %s", code, te.err)
	}
	raw, err := os.ReadFile(te.plistPath())
	if err != nil {
		t.Fatal(err)
	}
	env, _ := parsePlist(t, raw)["EnvironmentVariables"].(map[string]any)
	if len(env) != 1 || env[daemon.ReadModelCheckEnv] != "1" {
		t.Errorf("EnvironmentVariables = %v", env)
	}
	if got := sessionEnv(func(k string) string { return map[string]string{daemon.ReadModelCheckEnv: "1"}[k] }, "agent"); got != "agent" {
		t.Errorf("session command = %q, the check is the daemon's only", got)
	}
}
