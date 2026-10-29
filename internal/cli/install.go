package cli

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/xml"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"maps"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"slices"
	"strings"
	"time"

	"github.com/wloophq/wloop/internal/api/client"
	"github.com/wloophq/wloop/internal/brand"
	"github.com/wloophq/wloop/internal/daemon"
	"github.com/wloophq/wloop/internal/domain/shell"
	"github.com/wloophq/wloop/internal/fsx"
	"github.com/wloophq/wloop/internal/plugins/host"
)

var launchAgentLabel = client.DaemonLabel

var goos = runtime.GOOS

// bootstrapRetry paces the bootstrap attempts for a job that outlives
// bootoutWait: bootstrapping before launchd stops it fails with an I/O error.
var bootstrapRetry = time.Second

const bootstrapAttempts = 5

// bootoutWait bounds the wait for launchd to stop the previous job, which
// takes up to the plist's ExitTimeOut.
var bootoutWait, bootoutPoll = 15 * time.Second, 250 * time.Millisecond

// exitTimeout is the plist's ExitTimeOut: launchd kills the daemon this long
// after asking it to stop, which must outlast its own shutdown grace.
const exitTimeout = daemon.ShutdownGrace + 5*time.Second

// forwardedEnv are the variables the daemon must share with the CLI, and
// with the agent sessions it launches, to find the same config, socket and
// state, whether or not the login profile sets them.
var forwardedEnv = []string{"XDG_CONFIG_HOME", "XDG_STATE_HOME"}

type launchAgent struct {
	label, exe, command, keepAlive, logPrefix string
}

var (
	daemonAgent = func(exe string) launchAgent {
		return launchAgent{launchAgentLabel, exe, "serve", "<true/>", "serve"}
	}
	appAgentOf = func(bundleExe string) launchAgent {
		return launchAgent{launchAgentLabel + ".app", bundleExe, "app", keepAliveOnFailure, "app"}
	}
)

var agentCommandRe = regexp.MustCompile(`exec "\$0" ([^<]*)</string>`)

// agentCommand is the command a LaunchAgent plist runs.
func agentCommand(plist []byte) string {
	if m := agentCommandRe.FindSubmatch(plist); m != nil {
		return unescapeXML(string(m[1]))
	}
	return ""
}

func (e *Env) agentPath(label string) string {
	return filepath.Join(e.Home, "Library", "LaunchAgents", label+".plist")
}

func (e *Env) plistPath() string { return e.agentPath(launchAgentLabel) }

// installedInterval is how often the installed daemon sweeps: sweep.fast
// from the config; false when the serve agent is not installed.
func (e *Env) installedInterval() (time.Duration, bool) {
	if _, err := os.Stat(e.plistPath()); err != nil {
		return 0, false
	}
	cfg, err := e.loadConfig()
	if err != nil {
		cfg = e.defaultConfig()
	}
	return time.Duration(cfg.Sweep.Fast), true
}

const launchAgentPlist = `<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0">
<dict>
    <key>Label</key>
    <string>%[1]s</string>

    <key>ProgramArguments</key>
    <array>
        <string>%[5]s</string>
        <string>-lc</string>
        <string>exec "$0" %[7]s</string>
        <string>%[2]s</string>
    </array>
%[4]s
    <key>KeepAlive</key>
    %[8]s
    <key>RunAtLoad</key>
    <true/>
    <key>ThrottleInterval</key>
    <integer>30</integer>
    <key>ExitTimeOut</key>
    <integer>%[6]d</integer>

    <key>StandardOutPath</key>
    <string>%[3]s/%[9]s.out.log</string>
    <key>StandardErrorPath</key>
    <string>%[3]s/%[9]s.err.log</string>
</dict>
</plist>
`

// keepAliveOnFailure restarts the app after a crash but not after Quit.
const keepAliveOnFailure = `<dict>
        <key>SuccessfulExit</key>
        <false/>
    </dict>`

// renderPlist starts the agent's command through the login shell so it gets
// the profile's PATH and environment; exec keeps the loop as the process
// launchd tracks.
func (e *Env) renderPlist(a launchAgent, env map[string]string) string {
	var vars strings.Builder
	if len(env) > 0 {
		vars.WriteString("\n    <key>EnvironmentVariables</key>\n    <dict>\n")
		for _, k := range slices.Sorted(maps.Keys(env)) {
			fmt.Fprintf(&vars, "        <key>%s</key>\n        <string>%s</string>\n", xmlText(k), xmlText(env[k]))
		}
		vars.WriteString("    </dict>\n")
	}
	return fmt.Sprintf(launchAgentPlist, a.label, xmlText(a.exe), xmlText(e.StateDir), vars.String(),
		xmlText(shellPath()), int(exitTimeout/time.Second), a.command, a.keepAlive, a.logPrefix)
}

func xmlText(s string) string {
	var b strings.Builder
	_ = xml.EscapeText(&b, []byte(s))
	return b.String()
}

func daemonEnv() map[string]string {
	env := map[string]string{}
	for _, k := range append(slices.Clone(forwardedEnv), daemon.ReadModelCheckEnv) {
		if v := os.Getenv(k); v != "" {
			env[k] = v
		}
	}
	return env
}

// sessionEnv prefixes an agent session's command with the forwarded
// variables getenv sets to an absolute path, the only ones config honors.
func sessionEnv(getenv func(string) string, command string) string {
	var parts []string
	for _, k := range forwardedEnv {
		if v := getenv(k); v != "" && filepath.IsAbs(v) {
			parts = append(parts, k+"="+shell.Quote(v))
		}
	}
	return strings.Join(append(parts, command), " ")
}

// installAgent writes the LaunchAgent and reloads it.
func (e *Env) installAgent(ctx context.Context, a launchAgent) error {
	if err := os.MkdirAll(e.StateDir, 0o700); err != nil {
		return err
	}
	path := e.agentPath(a.label)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	if err := os.WriteFile(path, []byte(e.renderPlist(a, daemonEnv())), 0o644); err != nil {
		return err
	}

	// Stops the job of a previous install, so the bootstrap loads the new plist.
	e.bootOut(ctx, a.label)
	return e.bootstrap(ctx, path)
}

func launchDomain() string { return fmt.Sprintf("gui/%d", os.Getuid()) }

// bootOut stops the job of label and waits until launchd forgets it.
func (e *Env) bootOut(ctx context.Context, label string) {
	target := launchDomain() + "/" + label
	_, _ = e.Sh(ctx, "launchctl", "bootout", target)
	e.waitJobGone(ctx, target)
}

func (e *Env) bootstrap(ctx context.Context, plist string) error {
	var err error
	for i := range bootstrapAttempts {
		if i > 0 {
			time.Sleep(bootstrapRetry)
		}
		if _, err = e.Sh(ctx, "launchctl", "bootstrap", launchDomain(), plist); err == nil {
			return nil
		}
	}
	return fmt.Errorf("loading the LaunchAgent: %w", err)
}

// waitJobGone waits, up to bootoutWait, until launchd no longer knows the
// target: bootout returns before a running job has stopped.
func (e *Env) waitJobGone(ctx context.Context, target string) {
	deadline := time.Now().Add(bootoutWait)
	for {
		if _, err := e.Sh(ctx, "launchctl", "print", target); err != nil || !time.Now().Before(deadline) {
			return
		}
		select {
		case <-ctx.Done():
			return
		case <-time.After(bootoutPoll):
		}
	}
}

func shellPath() string {
	for _, c := range []string{"/bin/zsh", "/bin/bash"} {
		if _, err := os.Stat(c); err == nil {
			return c
		}
	}
	return "/bin/sh"
}

// shippedSumSuffix names the checksum a release ships beside each plugin
// binary, as sha256sum writes it.
const shippedSumSuffix = ".sha256"

// placeBundledPlugins copies the plugins the release ships beside the
// binary, in <exe dir>/plugins/<id>/, to host.InstalledDir, where the
// daemon finds them whatever path it runs the binary by, and records each
// binary's checksum, which the host requires of a bundled plugin. A binary
// that does not match the checksum its release shipped is refused. With no
// bundled plugin beside the binary, as under go run, it changes nothing.
func (e *Env) placeBundledPlugins(dryRun bool) error {
	exe, err := filepath.EvalSymlinks(e.Executable)
	if err != nil {
		return nil
	}
	from := filepath.Join(filepath.Dir(exe), "plugins")
	to := host.InstalledDir(e.StateDir)
	if fsx.SameFile(from, to) {
		return nil
	}
	found, err := host.Discover(host.Root{Dir: from, Bundled: true})
	if err != nil {
		return fmt.Errorf("the bundled plugins beside %s: %w", exe, err)
	}
	if len(found) == 0 {
		return nil
	}
	sums := host.Checksums{}
	for _, p := range found {
		if p.Binary == "" {
			return fmt.Errorf("the bundled plugin %s: %s holds no binary %s", p.ID(), p.Dir, brand.PluginBinary(p.ID()))
		}
		sum, err := fileSHA256(p.Binary)
		if err != nil {
			return err
		}
		if err := matchShippedSum(p.Binary, sum); err != nil {
			return err
		}
		sums[p.ID()] = sum
		dest := filepath.Join(to, p.ID())
		if dryRun {
			fmt.Fprintf(e.Out, "would place plugin %s (sha256 %x) in %s\n", p.ID(), sum, dest)
			continue
		}
		if err := replaceDir(p.Dir, dest); err != nil {
			return fmt.Errorf("placing plugin %s: %w", p.ID(), err)
		}
		fmt.Fprintf(e.Out, "placed plugin %s (sha256 %x) in %s\n", p.ID(), sum, dest)
	}
	if dryRun {
		return nil
	}
	entries, err := os.ReadDir(to)
	if err != nil {
		return err
	}
	for _, d := range entries {
		if _, ok := sums[d.Name()]; !ok {
			if err := os.RemoveAll(filepath.Join(to, d.Name())); err != nil {
				return err
			}
		}
	}
	return writeChecksums(host.ChecksumsFile(e.StateDir), sums)
}

func fileSHA256(path string) ([]byte, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return nil, err
	}
	return h.Sum(nil), nil
}

// matchShippedSum refuses bin when the checksum its release shipped beside
// it names another sum; a binary shipped without one, as a development
// build, passes.
func matchShippedSum(bin string, sum []byte) error {
	raw, err := os.ReadFile(bin + shippedSumSuffix)
	if errors.Is(err, fs.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	fields := strings.Fields(string(raw))
	if len(fields) == 0 || !strings.EqualFold(fields[0], hex.EncodeToString(sum)) {
		return fmt.Errorf("%s does not match the checksum its release shipped in %s; reinstall the release", bin, bin+shippedSumSuffix)
	}
	return nil
}

// replaceDir copies the tree src to dst through a sibling directory, so dst
// is the old tree or the new one, never a mix.
func replaceDir(src, dst string) error {
	if err := os.MkdirAll(filepath.Dir(dst), 0o700); err != nil {
		return err
	}
	tmp := dst + ".new"
	if err := os.RemoveAll(tmp); err != nil {
		return err
	}
	err := filepath.WalkDir(src, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(src, path)
		if err != nil {
			return err
		}
		out := filepath.Join(tmp, rel)
		if d.IsDir() {
			return os.MkdirAll(out, 0o755)
		}
		fi, err := d.Info()
		if err != nil {
			return err
		}
		if !fi.Mode().IsRegular() {
			return nil
		}
		return copyFileMode(path, out, fi.Mode().Perm())
	})
	if err != nil {
		_ = os.RemoveAll(tmp)
		return err
	}
	if err := os.RemoveAll(dst); err != nil {
		return err
	}
	return os.Rename(tmp, dst)
}

func copyFileMode(src, dst string, mode fs.FileMode) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	out, err := os.OpenFile(dst, os.O_CREATE|os.O_EXCL|os.O_WRONLY, mode)
	if err != nil {
		return err
	}
	_, err = io.Copy(out, in)
	return errors.Join(err, out.Close())
}

// writeChecksums records sums at path as sha256sum writes them, one
// "<hex>  <id>" line per plugin.
func writeChecksums(path string, sums host.Checksums) error {
	var b strings.Builder
	for _, id := range slices.Sorted(maps.Keys(sums)) {
		fmt.Fprintf(&b, "%x  %s\n", sums[id], id)
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	tmp := path + ".new"
	if err := os.WriteFile(tmp, []byte(b.String()), 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}
