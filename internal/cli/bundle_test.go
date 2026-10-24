package cli

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/wloophq/wloop/internal/brand"
	"github.com/wloophq/wloop/internal/client/menubar"
	"github.com/wloophq/wloop/internal/workspace"
	"github.com/wloophq/wloop/internal/workspace/fake"
)

type shCall struct{ name, args string }

func bundleEnv(t *testing.T) (*testEnv, *[]shCall) {
	t.Helper()
	te := newTestEnv(t, nil)
	src := filepath.Join(t.TempDir(), brand.Name)
	if err := os.WriteFile(src, []byte("binary v2"), 0o755); err != nil {
		t.Fatal(err)
	}
	te.Executable = src
	var calls []shCall
	te.Sh = func(_ context.Context, name string, args ...string) ([]byte, error) {
		calls = append(calls, shCall{name, strings.Join(args, " ")})
		return nil, nil
	}
	sourceSays(te, "v0.16.0\n")
	return te, &calls
}

// sourceSays makes every binary answer 'version' with out on stdout.
func sourceSays(te *testEnv, out string) {
	te.Stdout = func(_ context.Context, _ string, args ...string) ([]byte, error) {
		if len(args) == 1 && args[0] == "version" {
			return []byte(out), nil
		}
		return nil, errors.New("unexpected command")
	}
}

func mustBuild(t *testing.T, te *testEnv) {
	t.Helper()
	if err := te.buildBundle(context.Background(), te.Executable); err != nil {
		t.Fatal(err)
	}
}

func TestBuildBundleLaysOutSignsAndRegisters(t *testing.T) {
	te, calls := bundleEnv(t)
	if err := te.buildBundle(context.Background(), te.Executable); err != nil {
		t.Fatal(err)
	}
	app := bundlePath(te.Home)
	raw, err := os.ReadFile(bundleExe(te.Home))
	if err != nil || string(raw) != "binary v2" {
		t.Fatalf("executable %q %v", raw, err)
	}
	if fi, _ := os.Stat(bundleExe(te.Home)); fi.Mode().Perm()&0o100 == 0 {
		t.Fatal("bundle executable is not executable")
	}
	plist, err := os.ReadFile(filepath.Join(app, "Contents", "Info.plist"))
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{
		"<key>CFBundleIdentifier</key><string>" + brand.LaunchAgent() + "</string>",
		"<key>CFBundleExecutable</key><string>" + brand.Name + "</string>",
		"<key>LSUIElement</key><true/>",
		"<key>NSHighResolutionCapable</key><true/>",
		"<key>LoopSource</key><string>" + te.Executable + "</string>",
	} {
		if !strings.Contains(string(plist), want) {
			t.Errorf("Info.plist lacks %s", want)
		}
	}
	got := *calls
	if !slices.Contains(got, shCall{"codesign", "-s - --force " + app}) {
		t.Fatalf("calls %v", got)
	}
	if !slices.ContainsFunc(got, func(c shCall) bool { return strings.HasSuffix(c.name, "/lsregister") && c.args == "-f "+app }) {
		t.Fatalf("calls %v", got)
	}
}

func TestBuildBundleReplacesTheExecutableAtomically(t *testing.T) {
	te, _ := bundleEnv(t)
	mustBuild(t, te)
	before, err := os.Stat(bundleExe(te.Home))
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(te.Executable, []byte("binary v3"), 0o755); err != nil {
		t.Fatal(err)
	}
	mustBuild(t, te)
	if raw, _ := os.ReadFile(bundleExe(te.Home)); string(raw) != "binary v3" {
		t.Fatalf("%q", raw)
	}
	after, err := os.Stat(bundleExe(te.Home))
	if err != nil {
		t.Fatal(err)
	}
	if os.SameFile(before, after) {
		t.Fatal("the executable was rewritten in place; a running process would see it change")
	}
	entries, _ := os.ReadDir(filepath.Dir(bundleExe(te.Home)))
	if len(entries) != 1 {
		t.Fatalf("leftovers in MacOS/: %v", entries)
	}
}

func TestSyncBundleRebuildsWhenTheSourceMovedOn(t *testing.T) {
	te, calls := bundleEnv(t)
	mustBuild(t, te)
	te.Executable = bundleExe(te.Home)
	*calls = nil
	sourceSays(te, "v0.17.0\n")
	rebuilt, err := te.syncBundle(context.Background())
	if err != nil || !rebuilt {
		t.Fatalf("rebuilt=%v err=%v", rebuilt, err)
	}
	if !slices.ContainsFunc(*calls, func(c shCall) bool { return c.name == "codesign" }) {
		t.Fatalf("calls %v", *calls)
	}
}

func TestSyncBundleLeavesACurrentBundleAlone(t *testing.T) {
	te, calls := bundleEnv(t)
	mustBuild(t, te)
	te.Executable = bundleExe(te.Home)
	*calls = nil
	Version = "v0.15.0"
	t.Cleanup(func() { Version = "dev" })
	if rebuilt, err := te.syncBundle(context.Background()); err != nil || rebuilt {
		t.Fatalf("rebuilt=%v err=%v; a compiled version that differs from the source's output must not loop", rebuilt, err)
	}
}

func TestSyncBundleIgnoresAnotherBundle(t *testing.T) {
	te, _ := bundleEnv(t)
	mustBuild(t, te)
	te.Executable = "/Applications/" + brand.App + ".app/Contents/MacOS/" + brand.Name
	sourceSays(te, "v0.17.0\n")
	if rebuilt, err := te.syncBundle(context.Background()); err != nil || rebuilt {
		t.Fatalf("rebuilt=%v err=%v", rebuilt, err)
	}
}

func TestSyncBundleRetriesAfterAFailedSignature(t *testing.T) {
	te, _ := bundleEnv(t)
	te.Sh = func(_ context.Context, name string, _ ...string) ([]byte, error) {
		if name == "codesign" {
			return nil, errors.New("codesign: no identity")
		}
		return nil, nil
	}
	if err := te.buildBundle(context.Background(), te.Executable); err == nil {
		t.Fatal("a failed signature built the bundle")
	}
	te.Sh = func(context.Context, string, ...string) ([]byte, error) { return nil, nil }
	te.Executable = bundleExe(te.Home)
	if rebuilt, err := te.syncBundle(context.Background()); err != nil || !rebuilt {
		t.Fatalf("rebuilt=%v err=%v", rebuilt, err)
	}
}

func TestSourceVersionGivesUpOnAHungBinary(t *testing.T) {
	defer func(d time.Duration) { versionTimeout = d }(versionTimeout)
	versionTimeout = 20 * time.Millisecond
	te, _ := bundleEnv(t)
	te.Stdout = func(ctx context.Context, _ string, _ ...string) ([]byte, error) {
		<-ctx.Done()
		return nil, ctx.Err()
	}
	if _, err := te.sourceVersion(context.Background(), te.Executable); err == nil {
		t.Fatal("a hung binary reported a version")
	}
}

func TestInfoPlistVersionsAreNumeric(t *testing.T) {
	cases := map[string]string{"v0.16.0": "0.16.0", "dev": "0.0.0", "v0.16.0-3-gabc123-dirty": "0.16.0", "1.2": "1.2", "": "0.0.0"}
	for raw, want := range cases {
		p := []byte(infoPlist(raw, "/bin/"+brand.Name))
		if got := plistString(p, "CFBundleVersion"); got != want {
			t.Errorf("CFBundleVersion for %q = %q, want %q", raw, got, want)
		}
		if got := plistString(p, "CFBundleShortVersionString"); got != want {
			t.Errorf("CFBundleShortVersionString for %q = %q, want %q", raw, got, want)
		}
		if got := plistString(p, "LoopVersion"); got != raw {
			t.Errorf("LoopVersion = %q, want %q", got, raw)
		}
	}
}

func TestSyncBundleIgnoresAMissingSource(t *testing.T) {
	te, _ := bundleEnv(t)
	mustBuild(t, te)
	os.Remove(te.Executable)
	te.Executable = bundleExe(te.Home)
	if rebuilt, err := te.syncBundle(context.Background()); err != nil || rebuilt {
		t.Fatalf("rebuilt=%v err=%v", rebuilt, err)
	}
}

func TestAppInstallKeepsTheSymlinkAsTheSource(t *testing.T) {
	te, _ := bundleEnv(t)
	oldGOOS, oldWait := goos, bootoutWait
	goos, bootoutWait = "darwin", 0
	t.Cleanup(func() { goos, bootoutWait = oldGOOS, oldWait })
	link := filepath.Join(t.TempDir(), brand.Name)
	if err := os.Symlink(te.Executable, link); err != nil {
		t.Fatal(err)
	}
	te.Executable = link
	if code := te.run("app", "install"); code != 0 {
		t.Fatalf("exit %d: %s", code, te.err)
	}
	plist, _ := os.ReadFile(filepath.Join(bundlePath(te.Home), "Contents", "Info.plist"))
	if !strings.Contains(string(plist), "<key>LoopSource</key><string>"+link+"</string>") {
		t.Fatalf("%s", plist)
	}
	if !strings.Contains(te.out.String(), "To remove: launchctl bootout gui/") || !strings.Contains(te.out.String(), "rm -r "+bundlePath(te.Home)) {
		t.Fatalf("no removal instructions: %s", te.out)
	}
}

func TestDoctorReportsTheBundle(t *testing.T) {
	te, _ := bundleEnv(t)
	te.WS = workspace.NewSet(&fake.Manager{})
	old := goos
	goos = "darwin"
	t.Cleanup(func() { goos = old })
	te.run("doctor")
	if !strings.Contains(te.out.String(), "not installed; '"+brand.Command+" app install' adds the menu bar") {
		t.Fatalf("%s", te.out)
	}
	mustBuild(t, te)
	sourceSays(te, "v0.17.0\n")
	te.out.Reset()
	te.run("doctor")
	if !strings.Contains(te.out.String(), "built from "+te.Executable+" v0.16.0, which is now v0.17.0") || !strings.Contains(te.out.String(), brand.Command+" app install") {
		t.Fatalf("%s", te.out)
	}
}

func TestAppRebuildsAStaleBundleAndRestartsInPlace(t *testing.T) {
	te, _ := bundleEnv(t)
	mustBuild(t, te)
	te.Executable = bundleExe(te.Home)
	te.NativeAvailable = func() bool { return true }
	te.RunNative = func(func(menubar.Tray, menubar.Notifier)) { t.Error("the menu ran from a stale bundle") }
	reexecs := 0
	te.Reexec = func() error { reexecs++; return errors.New("exec refused") }
	sourceSays(te, "v0.17.0\n")
	if code := te.run("app"); code != exitRebuilt {
		t.Fatalf("exit %d: %s", code, te.err)
	}
	if reexecs != 1 {
		t.Fatalf("reexec ran %d times", reexecs)
	}
}

func TestAppInstallRefusesToRunFromTheBundle(t *testing.T) {
	te, _ := bundleEnv(t)
	old := goos
	goos = "darwin"
	t.Cleanup(func() { goos = old })
	mustBuild(t, te)
	link := filepath.Join(t.TempDir(), brand.Name)
	if err := os.Symlink(bundleExe(te.Home), link); err != nil {
		t.Fatal(err)
	}
	for _, exe := range []string{bundleExe(te.Home), link} {
		te.Executable = exe
		te.err.Reset()
		if code := te.run("app", "install"); code == 0 || !strings.Contains(te.err.String(), "not from the bundle") {
			t.Errorf("%s: exit %d: %s", exe, code, te.err)
		}
	}
}

func TestAppInstallOffMacOS(t *testing.T) {
	te, calls := bundleEnv(t)
	old := goos
	goos = "linux"
	t.Cleanup(func() { goos = old })
	if code := te.run("app", "install"); code == 0 || !strings.Contains(te.err.String(), "macOS only") {
		t.Fatalf("exit %d: %s", code, te.err)
	}
	if len(*calls) != 0 {
		t.Fatalf("calls %v", *calls)
	}
}

func TestDoctorFlagsALaunchAgentRunningAnotherCommand(t *testing.T) {
	te := newTestEnv(t, &fake.Manager{})
	fakeLaunchctl(t, te, fakeShell{})
	other := launchAgent{launchAgentLabel, bundleExe(te.Home), "serve --app", keepAliveOnFailure, "serve"}
	writePlist(t, te, te.renderPlist(other, nil))
	te.run("doctor")
	out := te.out.String()
	if !strings.Contains(out, "[missing] LaunchAgent: ") || !strings.Contains(out, "runs 'serve --app'") || !strings.Contains(out, "fix with: "+brand.Command+" app install") {
		t.Fatalf("%s", out)
	}
}

func TestDoctorReportsTheAppAgent(t *testing.T) {
	te := newTestEnv(t, &fake.Manager{})
	fakeLaunchctl(t, te, fakeShell{})
	te.run("doctor")
	if !strings.Contains(te.out.String(), "menu bar LaunchAgent: not installed; '"+brand.Command+" app install' adds it") {
		t.Fatalf("%s", te.out)
	}
	a := appAgentOf(bundleExe(te.Home))
	if err := os.MkdirAll(filepath.Dir(te.agentPath(a.label)), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(te.agentPath(a.label), []byte(te.renderPlist(a, nil)), 0o644); err != nil {
		t.Fatal(err)
	}
	loaded := false
	te.Sh = func(_ context.Context, name string, args ...string) ([]byte, error) {
		if name == "launchctl" && args[0] == "print" && strings.HasSuffix(args[1], ".app") && !loaded {
			return nil, errors.New("Could not find service")
		}
		return []byte("state = running\n"), nil
	}
	te.out.Reset()
	te.run("doctor")
	if out := te.out.String(); !strings.Contains(out, "[missing] menu bar LaunchAgent loaded: launchctl does not know it\n            fix with: "+brand.Command+" app install") {
		t.Fatalf("%s", out)
	}
	loaded = true
	te.out.Reset()
	te.run("doctor")
	if out := te.out.String(); !strings.Contains(out, "[ok     ] menu bar LaunchAgent loaded: loaded") {
		t.Fatalf("%s", out)
	}
}
