package slack

import (
	"context"
	"time"

	src "github.com/wloophq/wloop/pkg/plugin/source"
	"github.com/wloophq/wloop/plugins/slack/auth"
	"github.com/wloophq/wloop/plugins/slack/config"
	"github.com/wloophq/wloop/plugins/slack/send"
	slackapi "github.com/wloophq/wloop/plugins/slack/slackapi"
)

// Env is how the plugin reaches the Keychain, Slack and the browser; tests
// replace each.
type Env struct {
	// Keychain nil is the login Keychain through security.
	Keychain auth.Keychain
	// BaseURL "" is slack.com.
	BaseURL string
	// LockPath "" is the token store's default.
	LockPath string
	// LoginPort nil is the config's plugins.slack.redirect_port.
	LoginPort *int
	// Open nil opens the URL in the default browser.
	Open func(ctx context.Context, url string) error
	// Now nil is the wall clock.
	Now func() time.Time
}

// Kit is one process's Slack client, token store and sender over one
// configuration.
type Kit struct {
	Store  *auth.Store
	Client *slackapi.Client
	Send   *send.Service
}

// Kit builds the kit of cfg; meter hears of each Keychain spawn, nil none.
func (e Env) Kit(cfg config.Config, logf func(string, ...any), meter func(src.Sample)) *Kit {
	kc := e.Keychain
	if kc == nil {
		sk := auth.SecurityKeychain{}
		if meter != nil {
			sk.Meter = func(op string, failed bool, took time.Duration) {
				meter(src.Sample{Resource: "work", Op: op, Spawn: true, Failed: failed, Duration: took})
			}
		}
		kc = sk
	}
	client := &slackapi.Client{BaseURL: e.BaseURL}
	if cfg.Slack.RequestLog && logf != nil {
		client.RequestLog = func(method string, status int) { logf("slack_call method=%s status=%d", method, status) }
	}
	store := &auth.Store{Keychain: kc, Service: cfg.Service(), LockPath: e.LockPath, Client: client, ClientID: cfg.Slack.ClientID, Now: e.Now,
		Log: logf}
	client.Tokens = store
	return &Kit{Store: store, Client: client, Send: &send.Service{Client: client, Log: logf, PostAllowlist: cfg.Slack.PostAllowlist}}
}

// withToken is a client that calls Slack with tok as is, never refreshing it.
func (k *Kit) withToken(tok string) *slackapi.Client {
	return &slackapi.Client{BaseURL: k.Client.BaseURL, HTTP: k.Client.HTTP, RequestLog: k.Client.RequestLog, Tokens: fixedToken(tok)}
}

type fixedToken string

func (t fixedToken) Token(context.Context) (string, error) { return string(t), nil }
