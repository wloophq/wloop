package cli

import (
	"fmt"
	"maps"
	"slices"
	"strings"

	"github.com/wloophq/wloop/internal/config"
	"github.com/wloophq/wloop/internal/core/turns"
)

func (e *Env) agent(name string) (turns.Agent, error) {
	if a, ok := e.Agents[name]; ok {
		return a, nil
	}
	return nil, fmt.Errorf("agent %q is not supported; supported: %s", name, strings.Join(slices.Sorted(maps.Keys(e.Agents)), ", "))
}

// asker is the headless caller of the config's agent, running in dir.
func (e *Env) asker(cfg config.Config, dir string) (turns.Asker, error) {
	a, err := e.agent(cfg.Agent)
	if err != nil {
		return nil, err
	}
	k, ok := a.(turns.AskingAgent)
	if !ok {
		return nil, fmt.Errorf("agent %q cannot answer headless calls", cfg.Agent)
	}
	return k.Asker(dir, cfg.AgentModels[cfg.Agent])
}

func (e *Env) headlessAgent(name string) (turns.HeadlessAgent, error) {
	a, err := e.agent(name)
	if err != nil {
		return nil, err
	}
	h, ok := a.(turns.HeadlessAgent)
	if !ok {
		return nil, fmt.Errorf("agent %q cannot run headless", name)
	}
	return h, nil
}
