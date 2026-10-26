---
title: Nudge whoever a pull request waits on
description: Ask a reviewer or an author for the next step, in one comment an agent writes under your name.
---

```sh
{{command}} nudge https://github.com/acme/api/pull/4377
{{command}} nudge https://github.com/acme/api/pull/4377 the question in the migration thread
```

An agent writes one comment and posts it under your name. It mentions exactly the people the pull request waits on, says what it waits for and since when, asks for one next step and uses no pressure language. Guidance is what to ask about: a topic, a thread.

## Who gets asked

- the requested reviewers, when the pull request waits on them;
- the author, when it waits on the author;
- on your own pull request with no reviewer requested, the repository's maintainers, mentioning nobody, or the one person CODEOWNERS names for the changed files, never a team;
- a team only when no person is requested.

The daemon refuses when the pull request waits on you, on the loop, on a bot or on nobody, and when the same wait was nudged in the last 24 hours. The cockpit shows Nudge (`N`) disabled with that reason.

The agent starts in a new session of the pull request's review or author workspace, else of the repository's main checkout when it sits on the default branch, re-reads the pull request and posts nothing when the situation moved.

## Tone and form

The bundled `intent=nudge.md` sets the tone (neutral and friendly), the length (one to three sentences), the language (the one the pull request's conversation uses), no sign-off, and to hold back when someone asked the same people for the same thing in the last working day. Change any of it with `intent=nudge.md` blocks:

```sh
{{command}} profile add nudge --organization acme -- 'Write nudges in Portuguese and sign them with "Abs".'
```

## Automatic nudges

The `nudge-silent-30d` workflow plans a nudge on someone else's open pull request after `github.nudge.silent_days` (30) calendar days of silence. It never nudges a pull request you develop. Each one waits for you as a pending item: apply it with `{{command}} plan apply <id>` or the cockpit. To let them go out when their window ends, see [Tune workflows](/guides/workflows/#let-nudges-go-out).
