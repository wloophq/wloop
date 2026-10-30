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

// askSlack walks the user through creating the Slack app and returns its
// client id, "" when they skip Slack, and whether to log in again.
func (w *initWizard) askSlack(ctx context.Context, cfg config.Config) (string, bool) {
	e := w.e
	cur := cfg.Slack
	if cur.Configured() {
		if status, err := e.slackStatusText(ctx, cfg); err != nil {
			fmt.Fprintf(e.Out, "Slack: %v\n", err)
		} else {
			fmt.Fprintln(e.Out, status)
		}
		if !w.p.yes("Set Slack up again?", false) {
			return cur.ClientID, false
		}
	} else {
		fmt.Fprintln(e.Out, "Slack (optional) brings the threads and DMs that ask something of you into the Inbox.")
		if !w.p.yes("Set Slack up now?", false) {
			fmt.Fprintf(e.Out, "Skipped; '%s init' offers it again.\n", brand.Command)
			return "", false
		}
	}
	if !cur.Configured() || w.p.yes("Show the steps that create the Slack app?", false) {
		w.printSlackSteps()
	}
	for {
		id := strings.TrimSpace(w.p.line("Client ID (Basic Information > App Credentials)", cur.ClientID))
		if id == "" {
			fmt.Fprintf(e.Out, "Skipped Slack; '%s init' offers it again.\n", brand.Command)
			return "", false
		}
		if !strings.ContainsAny(id, " \t") {
			return id, true
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
	if err := w.e.slackLogin(ctx, cfg, []string{"--app-token"}); err != nil {
		return err
	}
	return w.e.slackLogin(ctx, cfg, nil)
}

// slackNextSteps are the commands that finish Slack off a terminal.
func slackNextSteps() []string {
	return []string{
		brand.Command + " slack login --app-token < <file with the app-level token>",
		brand.Command + " slack login",
	}
}
