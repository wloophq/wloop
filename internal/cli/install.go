package cli

import (
	"context"
	"encoding/xml"
	"fmt"
	"maps"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"slices"
	"strings"
	"time"

	"github.com/wloophq/wloop/internal/api/client"
	"github.com/wloophq/wloop/internal/daemon"
	"github.com/wloophq/wloop/internal/domain/shell"
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
