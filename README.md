# wloop

A work loop for the reviews and pull requests that reach you, with AI coding agents doing the rounds in local worktrees.

Handing a review to an agent is one command. Everything after it is manual: noticing that the author answered, finding the worktree again, moving it to the new head, asking the agent for the next round, keeping track of which of your own pull requests wait on you and which wait on someone else. With a dozen reviews and a few pull requests of your own in flight, that bookkeeping is most of the work. This tool does the bookkeeping and leaves you the decisions.

**Documentation: https://wloophq.github.io/wloop/**

## Features

- **Review rounds in their own worktree.** `wloop review <url>` opens a worktree at the pull request's head and starts an agent on a review judged by your review profile. When the author pushes, replies or re-requests you, the next round goes to the same worktree on its own.
- **Your own pull requests, addressed.** When reviews or failing checks arrive on a pull request you develop, an address round triages them: it verifies each comment in the code, applies what holds, replies, and asks for the next look.
- **Questions come back to you.** Every round ends with a record: done, a question for you, or what broke. The agent is instructed to hold its draft and ask when it is unsure, touches a risky surface or would contradict another reviewer; otherwise it publishes on GitHub itself, as you. You answer from the cockpit, the terminal client or a notification, and the answer reaches the agent's session. Slack drafts wait in the Outbox for your approval by default.
- **One inbox for what you owe.** Review requests, changes requested on your pull requests and, optionally, Slack threads that ask something of you become items with Inbox, Next, Waiting and Someday lists.
- **Your judgment, written down.** A review profile is a directory of Markdown blocks: what to look for, labels, voice, per organization, repository or author. The bundled profile is a neutral starting point.
- **Automatic behavior as rules you can read.** What runs on its own (follow-up rounds, cleanup of finished sessions and worktrees, nudges) is a set of workflows. `wloop plan` shows what would run now and why.

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
| `wloop status` | What waits on you, then what is stalled, yours and in the loop |
| `wloop tui` | The same views in the terminal, with one-key actions |
| `wloop open` | The web cockpit |
| `wloop plan` | What the workflows would do now, and why |
| `wloop profile try <url>` | What a review would publish with your profile, without publishing |
| `wloop doctor` | Diagnose the setup without changing anything |

`wloop --help` lists every command. The [command reference](https://wloophq.github.io/wloop/) has the rest.

## Development

```bash
make test    # go test -race ./...
make lint    # gofmt and go vet
make build   # binary in ./bin
```

## License

Apache 2.0. See [LICENSE](LICENSE).
