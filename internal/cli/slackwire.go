package cli

import (
	"context"
	"errors"
	"sync"
	"time"

	slacksource "github.com/wloophq/wloop/internal/adapter/source/slack"
	"github.com/wloophq/wloop/internal/adapter/store/sqlite"
	"github.com/wloophq/wloop/internal/config"
	"github.com/wloophq/wloop/internal/daemon"
	"github.com/wloophq/wloop/internal/domain/unit"
	"github.com/wloophq/wloop/internal/slack/auth"
	"github.com/wloophq/wloop/internal/slack/send"
	"github.com/wloophq/wloop/internal/transport/mcp"
)

// slackLoginCheck bounds how often the daemon reads the Keychain to answer LoggedIn.
const slackLoginCheck = 30 * time.Second

// wireSlack gives srv the Slack tools when Slack is configured and its plugin enabled.
func (m mcpEndpoint) wireSlack(srv *mcp.Server, d daemon.Deps, cfg config.Config) {
	if !slackEnabled(cfg, d.PluginEnabled) {
		return
	}
	sender := &slackSender{kit: m.slack.kit(cfg.Slack, m.log)}
	srv.Slack = sender
	if db, ok := d.Snapshot.(*sqlite.DB); ok {
		srv.SlackRead = &slackReading{sender: sender, read: slacksource.ReaderOver(db, sender.kit.Client)}
		if d.Outbox == nil {
			return
		}
		pub := slacksource.PublisherOver(db, sender.kit.Send, sender.kit.Store.Load, d.Now, m.log)
		var books sessionBooks = sqlite.New(db)
		if d.Turns != nil && d.Turns.Store != nil {
			books = d.Turns.Store
		}
		srv.SlackDraft = &slackDrafts{sender: sender, books: books, items: d.Items, outbox: d.Outbox, pub: pub, intents: m.intents}
		sender.record = sessionSends{token: sender.kit.Store.Load, landing: slacksource.LandingOver(db), outbox: d.Outbox, log: m.log}.record
	}
}

func slackEnabled(cfg config.Config, enabled func(unit.ProviderID) bool) bool {
	return cfg.Slack.Configured() && cfg.PluginEnabled(string(slackPlugin)) && (enabled == nil || enabled(slackPlugin))
}

// slackSender is the daemon's mcp.Slack over its token store and sender.
type slackSender struct {
	kit *slackKit
	// record keeps a post that landed in a tracked unit as a sent draft; nil records nothing.
	record func(ctx context.Context, r send.Request, sent send.Sent)

	mu        sync.Mutex
	checkedAt time.Time
	loggedIn  bool
}

// LoggedIn treats a Keychain failure other than an absent token as logged in,
// so the call that follows reports the failure itself.
func (s *slackSender) LoggedIn() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	if !s.checkedAt.IsZero() && time.Since(s.checkedAt) < slackLoginCheck {
		return s.loggedIn
	}
	_, err := s.kit.Store.Load()
	s.loggedIn, s.checkedAt = !errors.Is(err, auth.ErrNotLoggedIn), time.Now()
	return s.loggedIn
}

func (s *slackSender) Send(ctx context.Context, r send.Request) (send.Sent, error) {
	sent, err := s.kit.Send.Send(ctx, r)
	if errors.Is(err, send.ErrNotLoggedIn) {
		s.loggedOut()
	}
	if err == nil && s.record != nil {
		s.record(ctx, r, sent)
	}
	return sent, err
}

func (s *slackSender) loggedOut() {
	s.mu.Lock()
	s.loggedIn, s.checkedAt = false, time.Now()
	s.mu.Unlock()
}

// slackReading is the daemon's mcp.SlackReader: it reads nothing logged out.
type slackReading struct {
	sender *slackSender
	read   *slacksource.Reader
}

func (r *slackReading) Read(ctx context.Context, target string, limit int) (string, error) {
	if !r.sender.LoggedIn() {
		return "", send.ErrNotLoggedIn
	}
	out, err := r.read.Read(ctx, target, limit)
	if errors.Is(err, auth.ErrNotLoggedIn) {
		r.sender.mu.Lock()
		r.sender.loggedIn, r.sender.checkedAt = false, time.Now()
		r.sender.mu.Unlock()
		return "", send.ErrNotLoggedIn
	}
	return out, err
}
