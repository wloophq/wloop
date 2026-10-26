---
title: Threat model
description: What the loop protects against, what it does not, and the settings that limit what reaches an agent.
---

{{name}} runs agents as you, on your machine, with your GitHub and Slack identities. This page says where its checks stop. Report a vulnerability as the repository's [security policy](https://github.com/{{repo}}/security/policy) describes.

## The agent acts as you

A round publishes on GitHub through `gh`, with your login. On Slack it proposes drafts that the Outbox posts with your token, or posts at once through a tool the loop serves to the session, which tells the agent to ask you first when it is unclear whether you want the message posted in your name. {{name}} does not try to keep an agent from an identity that is already yours, on GitHub or on Slack.

The Outbox, the approval policies and the refusals below are workflow controls. They hold against an agent's mistakes, and they keep text from other people from approving a draft: no agent's or thread's text sets a draft's approval (see [What waits for you](/concepts/approval/)). A draft whose policy waits for you is held, whoever steered it. They do not hold against an agent, steered or not, that acts as you by another path: `gh`, a tool that posts or reacts on Slack, the daemon's socket or your Keychain. To keep an agent from your identities, run it where they are not: a virtual machine or another user account that does not mount your home directory or forward your SSH agent or credentials. {{name}} does not set this up for you.

## The same-user boundary

Every protection in {{name}} assumes that the processes running as your user are on your side. An agent session with a shell is such a process.

- The daemon's Unix socket (`<state>/daemon.sock`, mode 0600 in a 0700 directory) serves the CLI with no further authentication. Any process running as you can start rounds, answer questions and approve Outbox drafts through it, and write a reply that goes out at once as you (the `reply` entry verb and `POST /api/drafts` with send now).
- The Slack tokens live in your login Keychain, in items created through `/usr/bin/security`. Any process running as you can read them with the same command, without a prompt, and post to Slack as you, just as it can run `gh` as you on GitHub.
- `{{command}} outbox approve`, `outbox retry` and `outbox new --send` refuse when they detect an agent session's environment, and ask at a terminal unless `--yes`. `slack send` has no such refusal. That is a guard against mistakes, not a security boundary.
- An agent that follows injected instructions has everything you have: your `gh` token, your SSH keys, your Keychain and your files.
- Every session the loop starts is denied the control surface of every enabled workspace manager: the `orca` binary, `open claude://*`, Claude Desktop's session registry (`~/.claude/sessions`) and its peer sockets and keys (`/tmp/cc-socks`). That keeps a round from driving another session through its manager by mistake; like the other denies, it is no boundary against a process that runs as you.

## What the local endpoints authenticate

- **The cockpit** listens on `127.0.0.1` and requires the token in `<state>/http.json` (mode 0600) on every path, static files included. `{{command}} open` hands the token to the browser once; the page then keeps it in an HttpOnly, `SameSite=Strict` cookie. The agent and admin APIs are not served over TCP. The token is kept across restarts.
- **The MCP endpoint** listens on `127.0.0.1:47800` (`mcp.port`) and requires the key in `<state>/mcp.key` (mode 0600) in the `X-Loop-Key` header. It checks the `Host` header and refuses any request with an `Origin`. `X-Loop-Session` carries the process handle from the environment of the session's manager (one variable per enabled manager in `workspace_managers` order, such as `${CLAUDE_CODE_HOST_SESSION_ID:-${ORCA_TERMINAL_HANDLE}}` by default), and the daemon resolves it to the session. The handle is what the caller says it is: any holder of the key can record a turn for another session. When another process already holds the port, the daemon refuses to start and names `mcp.port`, so sessions never send the key to it.

Both endpoints keep other local users and web pages out. Neither keeps out a process running as you, which can read the token and the key from the state directory.

## What automatic rounds do on GitHub

Automatic rounds act under your GitHub identity. The agent publishes reviews, inline comments and replies itself, through `gh` in its session, and an address round commits and pushes to your branch.

The [approval window](/concepts/approval/) comes before a round starts, not before what the round publishes. The rules that a first review round never approves, that an unsure round holds its draft and asks, and what a follow-up may approve are instructions to the agent. What the agent can actually run is decided by your Claude Code permissions, plus the allows {{name}} adds for its rounds. No code checks a review before it goes out. Only drafts go through the Outbox: Slack messages and reactions, and the comments that Act and the GitHub plugin's nudge rule propose.

## What reaches an agent from other people

- **Address rounds read third-party comments.** Reviews and review threads on your pull requests start address rounds, from any GitHub account that can comment on the repository, apart from the logins in `github.address_skip_reviewers`. On a public repository that is anyone. The agent verifies each comment against the code, but the comment's text reaches it, and so does anything the comment asks for.
- **Review rounds open the pull request's checkout.** An automated review round starts Claude Code in a worktree at the pull request's head. Claude Code loads that checkout's project settings, so a pull request that changes `.claude/settings.json` (hooks, permissions), `.mcp.json`, `CLAUDE.md` or `AGENTS.md` can run code or steer the agent as you. Claude Code's folder trust is recorded for the repository and covers every pull request worktree of it; where project settings are off, trusting a blocked round from the cockpit trusts that worktree alone. Automated review rounds skip forks and authors outside the members, collaborators, owners and `trusted_authors`, so the author needs push access to the repository; a round you start by hand has no such filter, but its session never loads the checkout's project settings for such a pull request.

## What agents read from Slack

Every agent session the MCP server serves reads Slack as you, within what your user token can read:

- `slack_read` reads a stored unit, or the thread of any channel message whose permalink the agent holds, private channels and Slack Connect channels included; a direct message or a group DM only when {{name}} tracks it.
- `slack_search` runs Slack's search over every channel you can read, and returns the permalink, author, time and first line of each match. An agent needs no permalink handed to it to find a channel's messages: one query reaches any channel you are in. Direct messages and group DMs are left out unless tracked, the same line `slack_read` draws, and the result says how many it left out.

Reading is as you, like posting: {{name}} keeps no channel from a session, and the line on direct messages is a workflow control, not a barrier, since the agent can reach your token by the paths above. What an agent reads this way is other people's text, and reaches it as data it may follow: a message found by search can ask for anything, and no such text approves a draft. `slack.post_allowlist` bounds where any post lands; turning the Slack plugin off (`slack.enabled = false`) removes these tools.

## The settings that limit it

Two settings control what reaches an agent from other people:

- an instruction to the agent to treat comments by other accounts as untrusted data, and to stop and alert you when one asks for something suspicious;
- two switches you can turn off: automatic address rounds, and loading project settings in review sessions.

Both are on by default. The instruction is always on. The two switches are configuration keys whose value is a predicate over the pull request's facts; where the predicate does not hold, the behavior is off. They read the owner, the repository, the author and the visibility, and `intents.<intent>.project_settings.when` also reads the author association and the labels. For public repositories, keep both to private ones:

```json
{
  "workflows": { "address": { "only_when": "pr.visibility == \"private\"" } },
  "intents": { "review": { "project_settings": { "when": "pr.visibility == \"private\"" } } }
}
```

`pr.visibility` is empty until a pull request has been read in full, so these plan nothing for one not read yet. Narrow by owner, repository or author the same way (`pr.owner != "acme"`, `pr.author != "renovate[bot]"`). Where project settings are off, review sessions start with `--setting-sources user`: your settings, hooks and MCP servers load, the checkout's `.claude/`, `.mcp.json` and `CLAUDE.md` do not, and the agent reads `CLAUDE.md` and `AGENTS.md` as text. Claude Desktop cannot start such a session, so it runs in the next manager of `workspace_managers` that can (Orca), and fails closed when none can. In Claude Desktop, the allows and denies {{name}} adds for a round are written into the worktree's `.claude/settings.local.json`, so they apply only where the checkout's project settings load. See the [configuration reference](/reference/configuration/).

## Distribution

Releases ship unsigned binaries with a `checksums.txt`, without a signature or build provenance. The Homebrew cask removes the `com.apple.quarantine` attribute from the binary after install, which turns off Gatekeeper's check for it. `{{command}} app install` signs the app bundle ad hoc, in your home directory.
