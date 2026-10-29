# wloop

[![CI](https://github.com/wloophq/wloop/actions/workflows/ci.yml/badge.svg?branch=main)](https://github.com/wloophq/wloop/actions/workflows/ci.yml)
[![OpenSSF Scorecard](https://api.scorecard.dev/projects/github.com/wloophq/wloop/badge)](https://scorecard.dev/viewer/?uri=github.com/wloophq/wloop)

A work loop for what reaches you on GitHub and Slack: AI coding agents run your reviews, follow-ups and replies in local worktrees, and every behavior and text lives in a profile you can read and change.

Handing a review to an agent is one command. Everything after it is manual: noticing that the author answered, finding the worktree again, moving it to the new head, asking the agent for the next round, keeping track of which of your own pull requests and Slack threads wait on you and which wait on someone else. With a dozen reviews, a few pull requests of your own and the threads around them in flight, that bookkeeping is most of the work. This tool does the bookkeeping and leaves you the decisions.

**Documentation: https://wloophq.github.io/wloop/**

## Features

- **Review rounds in their own worktree.** `wloop review <url>` opens a worktree at the pull request's head and starts an agent on a review judged by your review profile. When the author pushes, replies or re-requests you, the next round goes to the same worktree on its own.
- **Your own pull requests, addressed.** When reviews or failing checks arrive on a pull request you develop, an address round triages them: it verifies each comment in the code, applies what holds, replies, and asks for the next look.
- **Questions come back to you.** Every round ends with a record: done, a question for you, or what broke. The agent is instructed to hold its draft and ask when it is unsure, touches a risky surface or would contradict another reviewer; otherwise it publishes on GitHub itself, as you. You answer from the cockpit, the terminal client or a notification, and the answer reaches the agent's session. A Slack draft waits on its gate for your approval by default.
- **One list of what you owe.** Review requests, changes requested on your pull requests and, optionally, Slack threads that ask something of you become tasks, listed in views such as Inbox, Needs me, Waiting and Someday.
- **Your judgment, written down.** A review profile is a directory of Markdown blocks: what to look for, labels, voice, per organization, repository or author. The bundled profile is a neutral starting point.
- **Automatic behavior as rules you can read.** What runs on its own (follow-up rounds, nudges, closing a settled thread, asking who owns an item, cleanup of finished sessions and worktrees) is a set of workflows over pull requests, Slack threads and your tasks. `wloop plan` shows what would run now and why.
- **Drafts wait for you.** A reply, a nudge or a closing message the loop or an agent proposes waits on its gate until you approve it, unless a rule of yours lets that kind of draft go on its own or after a delay. A comment on a pull request goes out under your name only when a rule names comments, and an agent never chooses its own approval.
- **Yours to change, without code.** Intents (what an agent does, where, with which instructions), workflows (when it runs), their texts and the labels and summaries the clients show all live in the profile. A file of yours replaces a bundled one, and an intent you add is a command: `wloop <intent> <url>`. The [language reference](https://wloophq.github.io/wloop/reference/language/) lists every word the profile files and the configuration take.

## Requirements

- macOS (the menu bar, notifications and background service) or Linux (run `wloop serve` under your init system).
- A workspace manager: [Claude Desktop](https://claude.ai/download), [Orca](https://github.com/stablyai/orca) (running), or both. It hosts the worktrees and the agent sessions; Orca also provides the board. wloop serves both at once.
- [Claude Code](https://docs.claude.com/en/docs/claude-code).
- [`gh`](https://cli.github.com), authenticated, and `git`.

## Install

```bash
brew install wloophq/tap/wloop
wloop init
```

`init` checks the requirements, asks where your projects live, what to follow and which profile to use, offers to connect Slack, installs the agent hooks and skills and, on macOS, the menu bar app and its background service. Run it again at any time to change an answer.

After `brew upgrade`, run `wloop install` and, on macOS, `wloop app install` again.

`wloop uninstall` takes out the app, the background service and the agent hooks and skills, and keeps your config and data; `--purge` deletes those too.

## Commands

| Command | What it does |
|---|---|
| `wloop review <url> [guidance]` | Start or continue a review of someone else's pull request |
| `wloop address <url> [guidance]` | Triage the reviews and failing checks of a pull request you develop |
| `wloop take-over <url>` | Make a pull request someone else opened yours to develop |
| `wloop nudge <url> [guidance]` | Ask whoever a pull request waits on for the next step, in one comment |
| `wloop <intent> <target> [guidance]` | Run any intent of your profile on a pull request, a Slack thread or another unit it serves |
| `wloop outbox` | The drafts that wait for your approval |
| `wloop status` | The pull requests the loop follows, by view: those that need you, then the stalled ones, yours and those in the loop |
| `wloop tui` | The same views in the terminal, with one-key actions |
| `wloop open` | The web cockpit |
| `wloop plan` | What the workflows would do now, and why |
| `wloop profile try <url>` | What a review would publish with your profile, without publishing |
| `wloop doctor` | Diagnose the setup without changing anything |

`wloop --help` lists every command. The [command reference](https://wloophq.github.io/wloop/) has the rest.

## Development

```bash
make test    # go test -race ./...
make lint    # gofmt and golangci-lint
make build   # binary in ./bin
```

## License

Apache 2.0. See [LICENSE](LICENSE).
