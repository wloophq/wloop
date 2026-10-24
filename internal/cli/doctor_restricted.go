package cli

import (
	"context"

	"github.com/wloophq/wloop/internal/config"
)

// restrictedChecks warns about each agent whose installed binary cannot run a
// headless turn restricted.
func (e *Env) restrictedChecks(ctx context.Context, cfg config.Config) []check {
	var out []check
	for _, name := range agentNames(cfg) {
		k, ok := e.Agents[name].(interface {
			RestrictedWarning(context.Context) (string, error)
		})
		if !ok {
			continue
		}
		if warn, err := k.RestrictedWarning(ctx); err == nil && warn != "" {
			out = append(out, check{markInfo, name + " restricted mode", warn, name + " update"})
		}
	}
	return out
}
