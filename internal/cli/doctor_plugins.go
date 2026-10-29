package cli

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"strings"
	"time"

	"github.com/hashicorp/go-hclog"

	"github.com/wloophq/wloop/internal/api"
	"github.com/wloophq/wloop/internal/brand"
	"github.com/wloophq/wloop/internal/config"
	"github.com/wloophq/wloop/internal/plugins/host"
	"github.com/wloophq/wloop/pkg/plugin"
)

// pluginHandshakeWait bounds doctor's start of one plugin binary.
const pluginHandshakeWait = 10 * time.Second

// pluginChecks are a line per plugin found: its binary's path and hash, how
// the host trusts it, the protocol it speaks, which doctor learns by
// starting it without its start call, and how the running daemon finds it;
// and a line naming the manifests that do not load.
func (e *Env) pluginChecks(ctx context.Context, cfg config.Config, health *api.Health) []check {
	var out []check
	found, err := host.Discover(host.Roots(filepath.Dir(e.ConfigPath), e.StateDir, e.Executable)...)
	if err != nil {
		out = append(out, check{markMissing, "plugin manifests", err.Error(), "fix or remove the plugin directory it names"})
	}
	sums, err := host.ReadChecksums(host.ChecksumsFile(e.StateDir))
	if err != nil {
		out = append(out, check{markMissing, "plugin checksums", err.Error(), brand.Command + " install"})
	}
	running := map[string]api.PluginHealth{}
	if health != nil {
		for _, p := range health.Plugins {
			running[p.ID] = p
		}
	}
	for _, p := range found {
		out = append(out, e.pluginCheck(ctx, cfg, p, sums, health != nil, running))
	}
	return out
}

func (e *Env) pluginCheck(ctx context.Context, cfg config.Config, p host.Plugin, sums host.Checksums, daemonUp bool, running map[string]api.PluginHealth) check {
	name := "plugin " + p.ID()
	if _, err := p.Manifest.Source(); err != nil {
		return check{markMissing, name, fmt.Sprintf("%s: %v", p.Dir, err), "fix or remove " + p.Dir}
	}
	if p.Binary == "" {
		return check{markMissing, name, p.Dir + " holds no binary " + brand.PluginBinary(p.ID()), brand.Command + " install"}
	}
	sum, err := fileSHA256(p.Binary)
	if err != nil {
		return check{markMissing, name, err.Error(), brand.Command + " install"}
	}
	where := fmt.Sprintf("%s sha256 %x", p.Binary, sum[:6])
	if !cfg.PluginEnabled(p.ID()) {
		return check{markInfo, name, where + ", off", ""}
	}
	want, why, err := host.Trusted(p, cfg.PluginTrust(p.ID()), sums)
	if err != nil {
		fix := fmt.Sprintf("set [plugins.%s] trust = %q in %s", p.ID(), host.TrustPath, e.ConfigPath)
		if p.Bundled {
			fix = brand.Command + " install"
		}
		return check{markMissing, name, where + ": " + err.Error(), fix}
	}
	proto, err := e.pluginProtocol(ctx, p, want)
	if err != nil {
		return check{markMissing, name, fmt.Sprintf("%s, %s: %v", where, why, err), "install the " + p.ID() + " plugin built for this release: " + brand.Command + " install"}
	}
	info := fmt.Sprintf("%s, %s, protocol %d", where, why, proto)
	h, ok := running[p.ID()]
	switch {
	case !daemonUp:
		return check{markOK, name, info + ", the daemon is not running", ""}
	case !ok:
		return check{markMissing, name, info + ", the daemon does not run it", "restart the daemon: " + brand.Command + " app install"}
	case h.Status == "down":
		return check{markMissing, name, fmt.Sprintf("%s, down in the daemon: %s", info, h.Reason), "the daemon's log names why"}
	case h.Status == "degraded":
		return check{markInfo, name, fmt.Sprintf("%s, degraded: %s", info, h.Reason), ""}
	case h.Reason != "":
		return check{markOK, name, fmt.Sprintf("%s, %s: %s", info, h.Status, h.Reason), ""}
	}
	return check{markOK, name, info + ", " + h.Status, ""}
}

// pluginProtocol starts p's binary, checking it has checksum when set, and
// returns the protocol version it negotiated; the plugin is never started.
func (e *Env) pluginProtocol(ctx context.Context, p host.Plugin, checksum []byte) (int, error) {
	var launcher plugin.Launcher = host.ExecLauncher{}
	if e.pluginLauncher != nil {
		launcher = e.pluginLauncher
	}
	ctx, cancel := context.WithTimeout(ctx, pluginHandshakeWait)
	defer cancel()
	proc, err := launcher.Launch(ctx, plugin.Spec{ID: p.ID(), Path: p.Binary, Kinds: p.Manifest.Kinds,
		Env: []string{plugin.BrandEnv + "=" + brand.Name}, Checksum: checksum, Logger: hclog.NewNullLogger()})
	if err != nil {
		if strings.Contains(strings.ToLower(err.Error()), "incompatible api version") {
			return 0, fmt.Errorf("it speaks another plugin protocol than this release's %d: %w", plugin.Protocol, err)
		}
		return 0, err
	}
	defer proc.Kill()
	if proc.Protocol() != p.Manifest.Protocol {
		return proc.Protocol(), errors.New("it speaks protocol " + fmt.Sprint(proc.Protocol()) + ", its manifest says " + fmt.Sprint(p.Manifest.Protocol))
	}
	return proc.Protocol(), nil
}
