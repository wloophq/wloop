// Package auth keeps the Slack tokens: login, refresh and the Keychain.
package auth

import (
	"errors"
	"sync"
	"time"

	"github.com/wloophq/wloop/plugins/slack/slackapi"
)

type Token struct {
	Team, User      string // ids
	TeamName        string
	Access, Refresh string
	Expires         time.Time
	Granted         time.Time // when the user approved the grant (login); zero is unknown
	Scopes          []string  // granted user scopes, from the exchange or X-OAuth-Scopes
}

// UserScopes is the scope list Login asks for.
const UserScopes = "search:read,channels:history,groups:history,im:history,mpim:history," +
	"channels:read,groups:read,im:read,mpim:read,users:read,users:read.email," +
	"chat:write,im:write,reactions:read,reactions:write,files:read"

var (
	// ErrNotFound is a Keychain item that is absent.
	ErrNotFound = errors.New("no such keychain item")
	// ErrNotLoggedIn is a Store with no token.
	ErrNotLoggedIn = errors.New("not logged in to Slack")
)

type Keychain interface {
	Get(service, account string) ([]byte, error) // ErrNotFound when absent
	Set(service, account string, secret []byte) error
	Delete(service, account string) error
}

// SecurityKeychain shells out to Bin (default "/usr/bin/security"). Set writes through
// `security -i` with `add-generic-password -U -s <svc> -a <acct> -X <hex secret>` on stdin;
// the secret never reaches argv.
type SecurityKeychain struct {
	Bin string
	// Meter hears of each security spawn; nil meters none.
	Meter func(op string, failed bool, took time.Duration)
}

type Store struct {
	Keychain Keychain
	Service  string
	LockPath string // default <os.UserCacheDir()>/<Service>.lock, so every process using one service shares the lock
	Client   *slack.Client
	ClientID string
	Now      func() time.Time
	Log      func(format string, args ...any) // names the service, never a token

	mu       sync.Mutex
	cached   Token
	cachedAt time.Time
	// unsaved marks cached as a rotated pair the Keychain refused; Slack revoked
	// the pair the Keychain holds, whose refresh token is staleRefresh.
	unsaved      bool
	staleRefresh string
	lastRetry    time.Time
}

type Login struct {
	ClientID, RedirectHost string
	Port                   int
	Scopes                 string
	Open                   func(url string) error
	Client                 *slack.Client
	Now                    func() time.Time
}
