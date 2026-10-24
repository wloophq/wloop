---
title: Take over a pull request
description: Make a pull request someone else opened yours to finish.
---

When a pull request someone else opened becomes yours to finish (its author is away, say), take it over:

```sh
{{command}} take-over https://github.com/acme/api/pull/4511
```

- An existing review workspace `REVIEW <branch>` is renamed to `<branch>` and keeps its link; without one, a workspace is created and linked.
- From then on the pull request is followed as yours: the author rules place its card, it gets address rounds when reviews arrive, and it no longer gets automatic review rounds.
- The take-over writes no card itself.

To take it over and start an address round at once:

```sh
{{command}} address https://github.com/acme/api/pull/4511 --take-over finish the pagination and answer Ana's thread
```

From the cockpit, Take over (`A`) on the row, which asks for a `y` naming the pull request first. From Explore, Take over on a result adds the pull request to the loop first, and with guidance runs an address round.

The `address` intent is `owner_only`: on a pull request that is not yours and not taken over, the cockpit shows Address disabled with "Not yours: take it over first".
