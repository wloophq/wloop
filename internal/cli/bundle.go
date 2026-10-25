package cli

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/wloophq/wloop/internal/brand"
	"github.com/wloophq/wloop/internal/client/menubar/macos"
)

var (
	bundleID    = brand.LaunchAgent()
	lsregister  = "/System/Library/Frameworks/CoreServices.framework/Frameworks/LaunchServices.framework/Support/lsregister"
	exitRebuilt = 75
)

var (
	errRebuilt       = errors.New(brand.App + ".app rebuilt from a newer binary; restarting")
	errAppMacOSOnly  = errors.New(brand.App + ".app is macOS only; elsewhere keep '" + brand.Command + " serve' running under your init system")
	versionTimeout   = 5 * time.Second
	bundleVersionRun = regexp.MustCompile(`^\d+(\.\d+)*`)
)

func bundlePath(home string) string { return filepath.Join(home, "Applications", brand.App+".app") }

func bundleExe(home string) string {
	return filepath.Join(bundlePath(home), "Contents", "MacOS", brand.Name)
}

// bundleVersion is version as Apple expects it, dot-separated integers.
func bundleVersion(version string) string {
	if v := bundleVersionRun.FindString(strings.TrimPrefix(version, "v")); v != "" {
		return v
	}
	return "0.0.0"
}

// infoPlist keeps the raw version in LoopVersion, which syncBundle
// compares with what the source reports.
func infoPlist(version, source string) string {
	return `<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0"><dict>
<key>CFBundleIdentifier</key><string>` + bundleID + `</string>
<key>CFBundleName</key><string>` + brand.Name + `</string>
<key>CFBundleExecutable</key><string>` + brand.Name + `</string>
<key>CFBundlePackageType</key><string>APPL</string>
<key>CFBundleShortVersionString</key><string>` + bundleVersion(version) + `</string>
<key>CFBundleVersion</key><string>` + bundleVersion(version) + `</string>
<key>LoopVersion</key><string>` + xmlText(version) + `</string>
<key>LSUIElement</key><true/>
<key>NSHighResolutionCapable</key><true/>
<key>LoopSource</key><string>` + xmlText(source) + `</string>
</dict></plist>
`
}

// buildBundle copies source into the bundle at its stable path, which keeps
// the notification permission across upgrades, and signs it ad hoc.
func (e *Env) buildBundle(ctx context.Context, source string) error {
	version, err := e.sourceVersion(ctx, source)
	if err != nil {
		return err
	}
	app := bundlePath(e.Home)
	macOS := filepath.Dir(bundleExe(e.Home))
	if err := os.MkdirAll(macOS, 0o755); err != nil {
		return err
	}
	if err := copyExecutable(source, bundleExe(e.Home)); err != nil {
		return err
	}
	plistPath := filepath.Join(app, "Contents", "Info.plist")
	if err := os.WriteFile(plistPath, []byte(infoPlist(version, source)), 0o644); err != nil {
		return err
	}
	err = nil
	if _, shErr := e.Sh(ctx, "codesign", "-s", "-", "--force", app); shErr != nil {
		err = fmt.Errorf("signing %s: %w", app, shErr)
	} else if _, shErr := e.Sh(ctx, lsregister, "-f", app); shErr != nil {
		err = fmt.Errorf("registering %s: %w", app, shErr)
	}
	if err != nil {
		// An empty version never matches the source's, so the next start
		// rebuilds.
		_ = os.WriteFile(plistPath, []byte(infoPlist("", source)), 0o644)
	}
	return err
}

// sourceVersion asks source for its version, briefly: a hung binary must not
// hold the menu bar or doctor.
func (e *Env) sourceVersion(ctx context.Context, source string) (string, error) {
	ctx, cancel := context.WithTimeout(ctx, versionTimeout)
	defer cancel()
	out, err := e.Stdout(ctx, source, "version")
	if err != nil {
		return "", fmt.Errorf("reading the version of %s: %w", source, err)
	}
	return strings.TrimSpace(string(out)), nil
}

func copyExecutable(src, dst string) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	tmp := dst + ".new"
	out, err := os.OpenFile(tmp, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0o755)
	if err != nil {
		return err
	}
	if _, err := io.Copy(out, in); err != nil {
		out.Close()
		os.Remove(tmp)
		return err
	}
	if err := out.Close(); err != nil {
		os.Remove(tmp)
		return err
	}
	return os.Rename(tmp, dst)
}

// plistString reads a string key of an Info.plist infoPlist wrote.
func plistString(plist []byte, key string) string {
	m := regexp.MustCompile(`<key>` + regexp.QuoteMeta(key) + `</key><string>([^<]*)</string>`).FindSubmatch(plist)
	if m == nil {
		return ""
	}
	return unescapeXML(string(m[1]))
}

// bundleSource is the binary the app bundle was copied from; empty when this
// process does not run from the bundle or the bundle names none.
func (e *Env) bundleSource() string {
	if e.Executable != bundleExe(e.Home) {
		return ""
	}
	raw, err := os.ReadFile(filepath.Join(bundlePath(e.Home), "Contents", "Info.plist"))
	if err != nil {
		return ""
	}
	_, source := bundleKeys(raw)
	return source
}

// bundleKeys reads the version and source of a bundle's Info.plist.
func bundleKeys(raw []byte) (version, source string) {
	return plistString(raw, "LoopVersion"), plistString(raw, "LoopSource")
}

// syncBundle rebuilds the bundle this process runs from when the binary it
// was copied from now reports another version.
func (e *Env) syncBundle(ctx context.Context) (bool, error) {
	if e.Executable != bundleExe(e.Home) {
		return false, nil
	}
	raw, err := os.ReadFile(filepath.Join(bundlePath(e.Home), "Contents", "Info.plist"))
	if err != nil {
		return false, err
	}
	version, source := bundleKeys(raw)
	if source == "" {
		return false, nil
	}
	if _, err := os.Stat(source); err != nil {
		return false, nil
	}
	now, err := e.sourceVersion(ctx, source)
	if err != nil || now == version {
		return false, nil
	}
	return true, e.buildBundle(ctx, source)
}

func unescapeXML(s string) string {
	return strings.NewReplacer("&lt;", "<", "&gt;", ">", "&quot;", `"`, "&#34;", `"`, "&#39;", "'", "&#x9;", "\t", "&#xA;", "\n", "&#xD;", "\r", "&amp;", "&").Replace(s)
}

// bundleCheck reports the app bundle's version, and whether the binary it was
// built from has moved on.
func (e *Env) bundleCheck(ctx context.Context) check {
	raw, err := os.ReadFile(filepath.Join(bundlePath(e.Home), "Contents", "Info.plist"))
	if err != nil {
		return check{markInfo, brand.App + ".app", "not installed; '" + brand.Command + " app install' adds the menu bar", ""}
	}
	version, source := bundleKeys(raw)
	if source != "" {
		if now, err := e.sourceVersion(ctx, source); err == nil && now != version {
			return check{markMissing, brand.App + ".app", fmt.Sprintf("built from %s %s, which is now %s", source, version, now), brand.Command + " app install"}
		}
	}
	return check{markOK, brand.App + ".app", version, ""}
}

func sameFile(a, b string) bool {
	fa, errA := os.Stat(a)
	fb, errB := os.Stat(b)
	return errA == nil && errB == nil && os.SameFile(fa, fb)
}

func runApp(ctx context.Context, e *Env, args []string) error {
	switch {
	case len(args) == 0:
		return runAppClient(ctx, e)
	case args[0] == "install":
		return runAppInstall(ctx, e, args[1:])
	}
	return errors.New("usage: " + brand.Command + " app [install [--no-menu-bar]]")
}

// runAppInstall builds the app bundle and installs the daemon's LaunchAgent,
// then the menu bar's; with --no-menu-bar, the daemon's alone.
func runAppInstall(ctx context.Context, e *Env, args []string) error {
	fs := flag.NewFlagSet("app install", flag.ContinueOnError)
	fs.SetOutput(e.Err)
	noMenuBar := fs.Bool("no-menu-bar", false, "install only the daemon's LaunchAgent, which keeps 'serve' running")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if fs.NArg() > 0 {
		return errors.New("usage: " + brand.Command + " app install [--no-menu-bar]")
	}
	if goos != "darwin" {
		if *noMenuBar {
			return errors.New("automatic installation only exists on macOS.\n" +
				"  Elsewhere, keep `" + brand.Command + " serve` running with a systemd user service or your init system")
		}
		return errAppMacOSOnly
	}
	if tmp := filepath.Clean(os.TempDir()) + string(filepath.Separator); strings.HasPrefix(e.Executable, tmp) {
		fmt.Fprintf(e.Err, "warning: %s is a temporary build that will disappear; run '"+brand.Command+" app install' from the installed binary.\n", e.Executable)
	}
	if *noMenuBar {
		return e.installDaemonAgent(ctx)
	}
	// The bundle remembers the path as called, not its symlink target:
	// Homebrew's /opt/homebrew/bin link survives upgrades, the versioned
	// Cellar path it points to does not.
	source := e.Executable
	if sameFile(source, bundleExe(e.Home)) || macos.InBundle(source) {
		return errors.New("run '" + brand.Command + " app install' from the installed binary, not from the bundle")
	}
	if err := e.buildBundle(ctx, source); err != nil {
		return err
	}
	agents := []launchAgent{daemonAgent(e.Executable), appAgentOf(bundleExe(e.Home))}
	var remove []string
	for _, a := range agents {
		if err := e.installAgent(ctx, a); err != nil {
			return err
		}
		remove = append(remove, fmt.Sprintf("launchctl bootout gui/%d/%s && rm %s", os.Getuid(), a.label, e.agentPath(a.label)))
	}
	fmt.Fprintf(e.Out, "Installed %s from %s, and the LaunchAgents that run '%s serve' and the menu bar at login.\n", bundlePath(e.Home), source, source)
	fmt.Fprintln(e.Out, "The menu bar item appears in a few seconds; allow notifications when macOS asks.")
	fmt.Fprintf(e.Out, "To remove: %s && rm -r %s\n", strings.Join(remove, " && "), bundlePath(e.Home))
	e.reportStore(ctx)
	return nil
}

// installDaemonAgent installs the daemon's LaunchAgent alone.
func (e *Env) installDaemonAgent(ctx context.Context) error {
	if err := e.installAgent(ctx, daemonAgent(e.Executable)); err != nil {
		return err
	}
	path := e.plistPath()
	fmt.Fprintf(e.Out, "Daemon installed: %s\n", path)
	fmt.Fprintf(e.Out, "Runs '%s serve' at login and keeps it running. Logs in %s.\n", e.Executable, e.StateDir)
	fmt.Fprintf(e.Out, "To remove: %s uninstall\n", brand.Command)
	e.reportStore(ctx)
	return nil
}
