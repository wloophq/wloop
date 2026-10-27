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
		sender.kit.Send.Stored = slacksource.StoredOver(db, m.log)
		reading := &slackReading{sender: sender, read: slacksource.ReaderOver(db, sender.kit.Client, m.log)}
		srv.SlackRead, srv.SlackSearch = reading, reading
		if d.Outbox == nil {
			return
		}
		pub := slacksource.PublisherOver(db, sender.kit.Send, sender.kit.Store.Load, d.Now, m.log)
		var books sessionBooks = sqlite.New(db)
		if d.Turns != nil && d.Turns.Store != nil {
			books = d.Turns.Store
		}
		sender.landing = slacksource.LandingOver(db, m.log)
		srv.SlackDraft = &slackDrafts{sender: sender, books: books, items: d.Items, outbox: d.Outbox, pub: pub, intents: m.intents}
		sender.records = &sessionSends{token: sender.kit.Store.Load, client: sender.kit.Client, landing: sender.landing, books: books, outbox: d.Outbox, log: m.log}
	}
}

func slackEnabled(cfg config.Config, enabled func(unit.ProviderID) bool) bool {
	return cfg.Slack.Configured() && cfg.PluginEnabled(string(slackPlugin)) && (enabled == nil || enabled(slackPlugin))
}

// slackSender is the daemon's mcp.Slack over its token store and sender.
type slackSender struct {
	kit     *slackKit
	landing slacksource.Landing
	// records keeps each post as a sent draft; nil records nothing.
	records *sessionSends

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

func (s *slackSender) Send(ctx context.Context, session string, r send.Request) (send.Sent, error) {
	sent, err := s.kit.Send.Send(ctx, r)
	if errors.Is(err, send.ErrNotLoggedIn) {
		s.loggedOut()
	}
	if err == nil && s.records != nil {
		s.records.post(ctx, session, r, sent)
	}
	return sent, err
}

func (s *slackSender) React(ctx context.Context, session string, r send.Reaction) (send.Reacted, error) {
	got, err := s.kit.Send.React(ctx, r)
	if errors.Is(err, send.ErrNotLoggedIn) {
		s.loggedOut()
	}
	if err == nil && !got.Unchanged && s.records != nil {
		s.records.reaction(ctx, session, r, got)
	}
	return got, err
}

// Edit replaces the text of one of the user's own messages.
func (s *slackSender) Edit(ctx context.Context, session string, e send.Edit) (send.Edited, error) {
	tok, err := s.kit.Store.Load()
	if errors.Is(err, auth.ErrNotLoggedIn) {
		s.loggedOut()
		return send.Edited{}, send.ErrNotLoggedIn
	}
	if err != nil {
		return send.Edited{}, err
	}
	e.User = tok.User
	got, err := s.kit.Send.Edit(ctx, e)
	if errors.Is(err, send.ErrNotLoggedIn) {
		s.loggedOut()
	}
	if err == nil && s.records != nil {
		s.records.edit(ctx, session, e, got)
	}
	return got, err
}

func (s *slackSender) messageUnit(ctx context.Context, message string) (unit.Unit, error) {
	tok, err := s.kit.Store.Load()
	if errors.Is(err, auth.ErrNotLoggedIn) {
		return unit.Unit{}, send.ErrNotLoggedIn
	}
	if err != nil {
		return unit.Unit{}, err
	}
	return s.landing.MessageUnit(ctx, s.kit.Client, tok.Team, message)
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
	return r.loggedIn(func() (string, error) { return r.read.Read(ctx, target, limit) })
}

func (r *slackReading) Search(ctx context.Context, a mcp.SearchArgs) (string, error) {
	return r.loggedIn(func() (string, error) {
		return r.read.Search(ctx, slacksource.SearchQuery{Query: a.Query, Channel: a.Channel, From: a.From, After: a.After, Before: a.Before, Limit: a.Limit})
	})
}

func (r *slackReading) loggedIn(read func() (string, error)) (string, error) {
	if !r.sender.LoggedIn() {
		return "", send.ErrNotLoggedIn
	}
	out, err := read()
	if errors.Is(err, auth.ErrNotLoggedIn) {
		r.sender.loggedOut()
		return "", send.ErrNotLoggedIn
	}
	return out, err
}
