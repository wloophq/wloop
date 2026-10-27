---
title: Turns and rounds
description: How each round is kept, how it ends, and how your answers reach the agent.
---

A **turn** is one intent (review, address, nudge, act, the probe, or one your profile defines) on one unit, usually a pull request, from the moment {{name}} sends it until a record closes it. A turn on a pull request is also called a **round**: the first review round, a follow-up round, an address round.

The daemon keeps every turn in its store. What GitHub does not keep (your answers, the guidance of each round, what each session recorded, whether you stood behind a verdict) lives there and comes back in every later turn's instructions, so a new session knows what the last one did.

## States

| State | Means |
|---|---|
| `sent` | {{name}} stored the turn and sent it; no agent has started it yet |
| `started` | an agent ran `{{command}} instructions <intent> --turn <id>`, or you started a turn by hand |
| `asked` | the agent stopped on a question for you |
| `done` | the work is finished; also set when a review of yours lands on GitHub while a review turn asks |
| `settled` | a review round with nothing new to publish |
| `failed` | the turn broke; nothing went out |
| `unrecorded` | the session stopped without a record, or never started the turn |
| `dismissed` | you closed the question or the stopped turn without answering |
| `replaced` | a newer turn of the same intent took over while this one was open |

## Instructions

A turn's instructions are stored text, rendered when the turn is created and read by hash, so editing your profile never changes a round already sent. They hold:

- the **procedure**: the mechanics of the turn (reading the pull request, the ambiguity gate, publishing, the record that ends it), from the intent's instructions and the shared partials;
- the **judgment**: the blocks of your review profile that match this pull request (see [Profiles and intents](/concepts/profiles/));
- the **brief**, when briefs are on (see [The brief](/concepts/brief/));
- your **guidance** for this round, verbatim;
- **Earlier turns**: up to 10 earlier turns of the pull request, newest first, with their state, verdict, the head they covered, what the session wrote and what you said.

The first line names the turn id. The agent reads them with `{{command}} instructions <intent> --turn <id>`.

## The record

Every turn ends with a record: its state, a one-line summary, the outcome (verdict, URL, counts) and, for a question, its text, its options (marking those that publish), a recommendation, the draft to read and the notes a new session needs to finish.

The agent sends it with the `turn_end` tool of {{name}}'s MCP server, which the daemon serves at `http://127.0.0.1:<mcp.port>/mcp`. The daemon stores a record only for the turn it sent to, or that was started by, the calling session. A malformed record comes back with every problem listed, and the agent fixes it and calls again. A session without the tool writes the record to a file and runs `{{command}} turn end <url> --turn <id> --file <path>`.

## The hooks

`{{command}} install` adds three hooks to each Claude Code home:

- **SessionStart** registers the session: its manager and process handle (an Orca terminal, or a Claude Desktop session), worktree, agent session id and home.
- **Stop** asks the daemon whether the session's turn owes a record. When it does, the hook blocks the stop once, telling the agent how to record. A second stop without one marks the turn `unrecorded` with the agent's last message.
- **UserPromptSubmit** records when you type in a session, so cleanup never closes a session under your hands. It prints nothing to the agent.

All three exit at once outside a session of an enabled workspace manager, give the daemon 2 seconds, and allow the stop on any error, so a broken {{name}} never traps an agent.

## Answers

An `asked` turn shows its question in the cockpit's detail, in a notification (macOS) and in the terminal client (`W`). You can pick an option, answer Yes or No to a confirmation, or write your own answer; text given with an option reaches the agent as a note after the choice.

The question is an Inbox message, as is every note an agent posts mid-turn with `inbox_post`, and every answer names the message it answers. The agent never receives the answer as text typed into its session. {{name}} tells the session that message N was answered, and the agent reads the answer with the `inbox_read` tool, or `{{command}} turn answer <unit> --message N` without the tool:

```
agent ──turn_end asked / inbox_post──▶ message N (Inbox) ◀── you answer N
  ▲                                         │
  │ notice: "message N answered"            │ answer stored with message N
  └──── inbox_read ──▶ the answers of N, marked read
```

The notice goes:

- **into the session that asked**, typed, when that session is active, loaded {{name}}'s MCP server, is idle and was quiet for less than an hour;
- **through the Stop hook** while that session is working or waiting on a permission: the answer waits, and the agent's next stop is blocked with the notice as its reason. A newer answer to the question replaces one still waiting;
- otherwise, for a question, as a **resume turn**: a new turn of the same intent, in a new session, for the pull request as it is now, with the question, the draft, the notes, the head it worked at and your answer. When the head moved since, it reviews the new commits before publishing an approval. An answer to a post whose session is gone stays stored.

`inbox_read` with a message returns that message, its options and every answer to it; without one, every answer the agent has not read, oldest first. Reading marks an answer read: the message's card in the cockpit says whether the agent read each answer, a resume turn's answer counts as read, and the `answer-unread` workflow notifies you when a session leaves an answer unread for 15 minutes. The texts of the notices live in the profile's `notices.toml`.

**Continue** sends a resume turn with no answer to an `asked`, `failed` or `unrecorded` turn. **Dismiss** closes it as `dismissed`. An answer typed directly in the agent's session still works.

## Sessions

A session is an agent's conversation in one worktree, under an id {{name}} mints and starts the agent with (`claude --session-id <id>`). It is **active** while it has a process, **dormant** when its manager no longer lists a process for it, and **closed** when its worktree or transcript is gone, you closed it in its manager, or `session.close` closed it. A session missing from its manager's census turns dormant, never closed, and a manager that quits or restarts never touches another manager's sessions. See the [glossary](/architecture/glossary/).

A round you start by hand continues in the session of its workspace. An automatic round keeps that session only if it printed within the last hour, while its prompt cache is warm; otherwise it starts a fresh one, which costs less than rereading a long context. A warm dormant session is woken in its own manager: Orca opens a new terminal with `claude --resume`, Claude Desktop reopens the session. {{name}} types a turn only into a session that loaded its MCP server since it last started.

In Claude Desktop, {{name}} creates the session headlessly, imports it into the app and types each prompt through the app's per-session socket. Prompts typed that way are not slash-expanded, so a slash command reaches the agent as an explicit instruction to run that skill. {{name}} creates Desktop worktrees under its own state directory (`worktrees/<repo>/<name>`), never under `.claude/worktrees`, which the app collects on its own. A worktree the app made there for one of its sessions is still Claude Desktop's while that session is not archived, even when Orca lists it too: a round on the pull request developed in it runs its session there, in Claude Desktop, and moves to the next manager only when Claude Desktop cannot launch the session. Once the app's session is archived, the app may collect the worktree, and Claude Desktop no longer lists it. While Claude Desktop is not running, its worktrees stay listed but their processes cannot be read, so their sessions keep their last state. Importing a session may bring Claude Desktop to the front.

## Headless turns

A turn of an intent whose manifest's `runners` include `headless` runs as one read-only call of the agent, with no session and no workspace. It may read only the unit's checkout and its dossier, and proposes up to 3 drafts, which wait in the [Outbox](/concepts/gtd/#the-outbox). The bundled `act` intent runs this way. `headless.tier`, `headless.per_call_usd` (0.5) and `headless.per_day_usd` (5) cap these calls.

## Dossiers

A dossier is the working memory of a unit or a project, a local git repository under `<state>/dossiers/` that the daemon commits after every turn. A turn's instructions name its path; a session may read it and write under `log/`, `sources/` and `drafts/`. A resolved dossier is archived after `dossiers.archive_after` (30 days) and purged `dossiers.purge_after` (400 days) later. See [Files on disk](/reference/files/).

## Inspecting turns

```sh
{{command}} turn show https://github.com/acme/api/pull/7          # every turn, record and answer
{{command}} turn show https://github.com/acme/api/pull/7 --json   # with each turn's instructions
```
