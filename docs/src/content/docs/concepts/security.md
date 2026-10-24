---
title: Threat model
description: What the loop protects against, what it does not, and the settings that limit what reaches an agent.
---

{{name}} runs agents as you, on your machine, with your GitHub and Slack identities. This page says where its checks stop. Report a vulnerability as the repository's [security policy](https://github.com/{{repo}}/security/policy) describes.

## The same-user boundary

Every protection in {{name}} assumes that the processes running as your user are on your side. The daemon, the Outbox and the permission prompts protect against an agent's mistakes. They do not protect against a process running as you, and an agent session with a shell is such a process.

- The daemon's Unix socket (`<state>/daemon.sock`, mode 0600 in a 0700 directory) serves the CLI with no further authentication. Any process running as you can start rounds, answer questions and approve Outbox drafts through it, and write a reply that goes out at once as you (the `reply` entry verb and `POST /api/drafts` with send now).
- The Slack tokens live in your login Keychain, in items created through `/usr/bin/security`. Any process running as you can read them with the same command, without a prompt, and post to Slack as you.
- `{{command}} outbox approve`, `outbox retry` and `outbox new --send` refuse when they detect an agent session's environment, and ask at a terminal unless `--yes`. That is a guard against mistakes, not a security boundary.
- An agent that follows injected instructions has everything you have: your `gh` token, your SSH keys, your Keychain and your files.
- Every session the loop starts is denied the control surface of every enabled workspace manager: the `orca` binary, `open claude://*`, Claude Desktop's session registry (`~/.claude/sessions`) and its peer sockets and keys (`/tmp/cc-socks`). That keeps a round from driving another session through its manager by mistake; like the other denies, it is no boundary against a process that runs as you.

## What the local endpoints authenticate

- **The cockpit** listens on `127.0.0.1` and requires the token in `<state>/http.json` (mode 0600) on every path, static files included. `{{command}} open` hands the token to the browser once; the page then keeps it in an HttpOnly, `SameSite=Strict` cookie. The agent and admin APIs are not served over TCP. The token is kept across restarts.
- **The MCP endpoint** listens on `127.0.0.1:47800` (`mcp.port`) and requires the key in `<state>/mcp.key` (mode 0600) in the `X-Loop-Key` header. It checks the `Host` header and refuses any request with an `Origin`. `X-Loop-Session` carries the process handle from the environment of the session's manager (one variable per enabled manager in `workspace_managers` order, such as `${CLAUDE_CODE_HOST_SESSION_ID:-${ORCA_TERMINAL_HANDLE}}` by default), and the daemon resolves it to the session. The handle is what the caller says it is: any holder of the key can record a turn for another session. When another process already holds the port, the daemon refuses to start and names `mcp.port`, so sessions never send the key to it.

Both endpoints keep other local users and web pages out. Neither keeps out a process running as you, which can read the token and the key from the state directory.

## What automatic rounds do on GitHub

Automatic rounds act under your GitHub identity. The agent publishes reviews, inline comments and replies itself, through `gh` in its session, and an address round commits and pushes to your branch.

The [approval window](/concepts/approval/) comes before a round starts, not before what the round publishes. The rules that a first review round never approves, that an unsure round holds its draft and asks, and what a follow-up may approve are instructions to the agent. What the agent can actually run is decided by your Claude Code permissions, plus the allows {{name}} adds for its rounds. No code checks a review before it goes out. Only Slack drafts and proposals go through the Outbox.

## What reaches an agent from other people

- **Address rounds read third-party comments.** Reviews and review threads on your pull requests start address rounds, from any GitHub account that can comment on the repository, apart from the logins in `address_skip_reviewers`. On a public repository that is anyone. The agent verifies each comment against the code, but the comment's text reaches it, and so does anything the comment asks for.
- **Review rounds open the pull request's checkout.** An automated review round starts Claude Code in a worktree at the pull request's head. Claude Code loads that checkout's project settings, so a pull request that changes `.claude/settings.json` (hooks, permissions), `.mcp.json`, `CLAUDE.md` or `AGENTS.md` can run code or steer the agent as you. Claude Code's folder trust is recorded for the repository and covers every pull request worktree of it; where project settings are off, trusting a blocked round from the cockpit trusts that worktree alone. Automated review rounds skip forks and authors outside the members, collaborators, owners and `trusted_authors`, so the author needs push access to the repository; a round you start by hand has no such filter, but its session never loads the checkout's project settings for such a pull request.

## The settings that limit it

Two settings control what reaches an agent from other people:

- an instruction to the agent to treat comments by other accounts as untrusted data, and to stop and alert you when one asks for something suspicious;
- two switches you can turn off: automatic address rounds, and loading project settings in review sessions.

Both are on by default. The instruction is always on. The two switches are configuration keys whose value is a predicate over the pull request's facts; where the predicate does not hold, the behavior is off. They read the owner, the repository, the author and the visibility, and `project_settings` also reads the author association and the labels. For public repositories, keep both to private ones:

```json
{
  "workflows": { "address": { "only_when": "pr.visibility == \"private\"" } },
  "project_settings": { "review": "pr.visibility == \"private\"" }
}
```

`pr.visibility` is empty until a pull request has been read in full, so these plan nothing for one not read yet. Narrow by owner, repository or author the same way (`pr.owner != "acme"`, `pr.author != "renovate[bot]"`). Where project settings are off, review sessions start with `--setting-sources user`: your settings, hooks and MCP servers load, the checkout's `.claude/`, `.mcp.json` and `CLAUDE.md` do not, and the agent reads `CLAUDE.md` and `AGENTS.md` as text. Claude Desktop cannot start such a session, so it runs in the next manager of `workspace_managers` that can (Orca), and fails closed when none can. In Claude Desktop, the allows and denies {{name}} adds for a round are written into the worktree's `.claude/settings.local.json`, so they apply only where the checkout's project settings load. See the [configuration reference](/reference/configuration/).

## Distribution

Releases ship unsigned binaries with a `checksums.txt`, without a signature or build provenance. The Homebrew cask removes the `com.apple.quarantine` attribute from the binary after install, which turns off Gatekeeper's check for it. `{{command}} app install` signs the app bundle ad hoc, in your home directory.
