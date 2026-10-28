package cli

import (
	"context"

	slacksource "github.com/wloophq/wloop/internal/adapter/source/slack"
	"github.com/wloophq/wloop/internal/config"
	dslack "github.com/wloophq/wloop/internal/domain/slack"
	"github.com/wloophq/wloop/internal/domain/unit"
	"github.com/wloophq/wloop/internal/slack/send"
	"github.com/wloophq/wloop/internal/source"
)

// slackModule builds the Slack plugin over the login Keychain and slack.com.
func slackModule(cfg config.Config, d source.Deps) (source.Module, error) {
	return slackEnv{}.module(cfg, d)
}

func (s slackEnv) module(cfg config.Config, d source.Deps) (source.Module, error) {
	kit := s.kit(cfg.Slack, d.Log)
	return slacksource.Module(cfg, d, slacksource.Slack{Tokens: kit.Store, Client: kit.Client})
}

// resolvers are the codec capabilities that look an address up, by
// provider.
func (e *Env) resolvers() map[unit.ProviderID]unit.Resolver {
	return map[unit.ProviderID]unit.Resolver{dslack.Provider: slackResolver{e}}
}

// slackResolver resolves a Slack message ref or permalink to its direct
// message segment or thread.
type slackResolver struct{ e *Env }

func (s slackResolver) Resolve(ctx context.Context, address string) (string, bool, error) {
	if !send.IsMessage(address) {
		return "", false, nil
	}
	u, err := s.e.slackMessageUnit(ctx, address)
	return u.Key, err == nil, err
}
