package cli

import (
	"context"
	"fmt"
	"strings"

	"github.com/wloophq/wloop/internal/brand"
	"github.com/wloophq/wloop/internal/config"
)

// slackManifest is the documentation's slack-app-manifest.yaml snippet; {{name}} is the brand's name.
const slackManifest = `display_information:
  name: {{name}} (Your Name)
  description: Brings the Slack threads and DMs that ask something of you into your {{name}} Inbox, and posts as you only after your approval.
oauth_config:
  redirect_urls:
    - http://127.0.0.1:53682/slack/callback
  scopes:
    user:
      - search:read
      - channels:history
      - groups:history
      - im:history
      - mpim:history
      - channels:read
      - groups:read
      - im:read
      - mpim:read
      - users:read
      - users:read.email
      - chat:write
      - im:write
      - reactions:read
      - reactions:write
      - files:read
settings:
  event_subscriptions:
    user_events:
      - message.channels
      - message.groups
      - message.im
      - message.mpim
      - reaction_added
      - reaction_removed
  org_deploy_enabled: false
  socket_mode_enabled: true
  token_rotation_enabled: true
`

func renderSlackManifest() string { return strings.ReplaceAll(slackManifest, "{{name}}", brand.Name) }

// slackState is what the Keychain holds for the config's Slack app.
type slackState struct {
	appToken bool
	// login is the logged-in user and team, "" when logged out.
	login string
}

func (e *Env) slackState(cfg config.Slack) slackState {
	kit := e.slack.kit(cfg, nil)
	var s slackState
	if _, err := kit.Store.AppToken(); err == nil {
		s.appToken = true
	}
	if tok, err := kit.Store.Load(); err == nil {
		s.login = tok.User + " in " + teamLabel(tok)
	}
	return s
}

// askSlack walks the user through creating the Slack app and returns its
// client id, "" when they skip Slack.
func (w *initWizard) askSlack(cur config.Slack) string {
	e := w.e
	if cur.Configured() {
		st := e.slackState(cur)
		if st.login != "" && st.appToken {
			fmt.Fprintf(e.Out, "Slack: logged in as %s.\n", st.login)
			if !w.p.yes("Set Slack up again?", false) {
				return cur.ClientID
			}
		} else if !w.p.yes("Slack has a client id but no login yet. Finish the Slack setup now?", true) {
			return cur.ClientID
		}
	} else {
		fmt.Fprintln(e.Out, "Slack (optional) brings the threads and DMs that ask something of you into the Inbox.")
		if !w.p.yes("Set Slack up now?", false) {
			fmt.Fprintf(e.Out, "Skipped; '%s init' offers it again.\n", brand.Command)
			return ""
		}
	}
	if !cur.Configured() || w.p.yes("Show the steps that create the Slack app?", false) {
		w.printSlackSteps()
	}
	for {
		id := strings.TrimSpace(w.p.line("Client ID (Basic Information > App Credentials)", cur.ClientID))
		if id == "" {
			fmt.Fprintf(e.Out, "Skipped Slack; '%s init' offers it again.\n", brand.Command)
			return ""
		}
		if !strings.ContainsAny(id, " \t") {
			return id
		}
		fmt.Fprintln(e.Out, "A client id has no spaces, such as 000000000000.000000000000.")
	}
}

func (w *initWizard) printSlackSteps() {
	t := w.t
	fmt.Fprintln(w.e.Out, t.Bold("Create your own Slack app:"))
	fmt.Fprintln(w.e.Out, `  1. Open https://api.slack.com/apps, choose Create New App, then From a manifest, and pick your workspace.
  2. Paste this manifest, replacing "Your Name" so the workspace admin can tell the apps apart:`)
	for _, l := range strings.Split(strings.TrimSuffix(renderSlackManifest(), "\n"), "\n") {
		fmt.Fprintln(w.e.Out, "       "+t.Dim(l))
	}
	fmt.Fprintln(w.e.Out, `  3. Create the app. On OAuth & Permissions, turn on PKCE.
  4. On Basic Information, under App-Level Tokens, choose Generate Token and Scopes, add
     connections:write and generate it; keep the app-level token for the next step.
  5. Copy the Client ID from App Credentials on the same page.`)
}

// finishSlack saves the app token and logs in, as slack login --app-token
// and slack login do, for the config just written.
func (w *initWizard) finishSlack(ctx context.Context, cfg config.Config) error {
	e := w.e
	kit := e.slack.kit(cfg.Slack, func(format string, a ...any) { fmt.Fprintf(e.Err, format+"\n", a...) })
	st := e.slackState(cfg.Slack)
	if !st.appToken || w.p.yes("Replace the stored Slack app token?", false) {
		if err := e.saveAppToken(kit); err != nil {
			return err
		}
	}
	if st.login != "" && !w.p.yes("Logged in to Slack as "+st.login+". Log in again?", false) {
		return nil
	}
	return e.slackLogin(ctx, cfg, kit, nil)
}

// slackNextSteps are the commands that finish Slack off a terminal.
func slackNextSteps() []string {
	return []string{
		brand.Command + " slack login --app-token < <file with the app-level token>",
		brand.Command + " slack login",
	}
}
