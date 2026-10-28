package host

import (
	"errors"
	"fmt"

	"github.com/hashicorp/go-hclog"

	"github.com/wloophq/wloop/pkg/plugin"
)

// Settings are what the configuration says of each plugin.
type Settings interface {
	PluginEnabled(id string) bool
	PluginTrust(id string) string
}

// Runs are the plugins of ps the host runs: those enabled, with a binary
// it trusts. A plugin with no binary, compiled into the daemon, is not one.
// An untrusted binary is left out and named in the error, as ErrUntrusted;
// why says how each that runs is trusted, by id.
func Runs(ps []Plugin, s Settings, recorded Checksums, env []string, logger hclog.Logger) (runs []Run, why map[string]string, err error) {
	why = map[string]string{}
	var errs []error
	for _, p := range ps {
		if p.Binary == "" || !s.PluginEnabled(p.ID()) {
			continue
		}
		sum, w, err := Trusted(p, s.PluginTrust(p.ID()), recorded)
		if err != nil {
			errs = append(errs, err)
			continue
		}
		why[p.ID()] = w
		runs = append(runs, Run{Manifest: p.Manifest, Spec: plugin.Spec{ID: p.ID(), Path: p.Binary, Kinds: p.Manifest.Kinds,
			Env: env, Checksum: sum, Logger: logger}})
	}
	if err := errors.Join(errs...); err != nil {
		return runs, why, fmt.Errorf("plugins: %w", err)
	}
	return runs, why, nil
}
