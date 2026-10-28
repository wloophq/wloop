package cli

import (
	"context"
	"fmt"
	"io"
	"path/filepath"
	"strings"

	"github.com/hashicorp/go-hclog"

	"github.com/wloophq/wloop/internal/brand"
	"github.com/wloophq/wloop/internal/config"
	"github.com/wloophq/wloop/internal/plugins/host"
	psource "github.com/wloophq/wloop/internal/plugins/source"
	"github.com/wloophq/wloop/pkg/plugin"
	src "github.com/wloophq/wloop/pkg/plugin/source"
)

// runPluginBinary runs the command args of plugin id's binary in CLI mode,
// with stdin as its standard input, as serve would trust it; a non-zero
// exit is an exitCode.
func (e *Env) runPluginBinary(ctx context.Context, cfg config.Config, id string, args []string, stdin []byte) error {
	return e.runPluginBinaryTo(ctx, cfg, id, args, stdin, e.Out)
}

// runPluginBinaryTo is runPluginBinary writing the command's output to out.
func (e *Env) runPluginBinaryTo(ctx context.Context, cfg config.Config, id string, args []string, stdin []byte, out io.Writer) error {
	p, err := e.pluginBinary(id)
	if err != nil {
		return err
	}
	sums, err := host.ReadChecksums(filepath.Join(e.StateDir, "plugins", "checksums"))
	if err != nil {
		return err
	}
	sum, _, err := host.Trusted(p, cfg.PluginTrust(id), sums)
	if err != nil {
		return err
	}
	s, err := p.Manifest.Source()
	if err != nil {
		return err
	}
	conf, err := (&psource.Broker{ID: id, Manifest: s, Config: func() config.Config { return cfg }}).Host().Services.Config(ctx)
	if err != nil {
		return err
	}
	var launcher plugin.Launcher = host.ExecLauncher{}
	if e.pluginLauncher != nil {
		launcher = e.pluginLauncher
	}
	logger := hclog.New(&hclog.LoggerOptions{Output: e.Err, Level: hclog.Warn})
	proc, err := launcher.Launch(ctx, plugin.Spec{ID: id, Path: p.Binary, Kinds: p.Manifest.Kinds,
		Env: []string{plugin.BrandEnv + "=" + brand.Name}, Checksum: sum, Logger: logger})
	if err != nil {
		return err
	}
	defer proc.Kill()
	c, err := proc.Kind(src.Kind)
	if err != nil {
		return err
	}
	exit, err := src.NewClient(c).RunCLI(ctx, src.CLIRequest{Args: args, Stdin: stdin, Config: conf}, out, e.Err)
	if err != nil {
		return err
	}
	if exit != 0 {
		return exitCode(exit)
	}
	return nil
}

// pluginBinary is the discovered plugin id with a binary.
func (e *Env) pluginBinary(id string) (host.Plugin, error) {
	for _, p := range e.plugins {
		if p.ID() == id && p.Binary != "" {
			return p, nil
		}
	}
	var dirs []string
	for _, r := range host.Roots(filepath.Dir(e.ConfigPath), e.Executable) {
		dirs = append(dirs, filepath.Join(r.Dir, id))
	}
	return host.Plugin{}, fmt.Errorf("the %s plugin's binary is not installed: looked in %s", id, strings.Join(dirs, " and "))
}
