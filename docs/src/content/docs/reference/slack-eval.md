---
title: Slack extraction eval
description: Measure how well the Slack extraction turns your conversations into the items you would act on.
---

`{{command}} slack eval` asks one question of every conversation: would a reader who sees only the extracted items take the same actions as one who read the whole conversation?

## Method

Each fixture is one conversation as {{name}} stores it. A run puts each through four model calls:

1. the **oracle** reads the conversation and writes your briefing: what you must do, decide or chase;
2. the **extractor**, the shipped extraction prompt (or another given with `--prompt`), produces the items;
3. a **blind reader** writes the same briefing from the items alone;
4. a **judge** compares the two briefings and gives a severity (`none`, `minor`, `major`, `critical`) and the causes of each gap (`missing_item`, `spurious_item`, `wrong_assignee`, `wrong_waiting_on`, `wrong_state`, `wrong_resolution`, `missing_link`, `vague_summary`, `missing_context`, `wrong_verdict`, `other`).

Each command runs in the Slack plugin through the running daemon, which reads the plugin's store and asks its agent; its output reaches the terminal as the plugin writes it. The extractor runs on the fast tier; the oracle, the blind reader and the judge on the frontier tier. Every stage's answer is cached under the output directory by the key of its inputs, so a rerun pays only for what changed.

## Commands

```sh
{{command}} slack eval export --since 2026-10-01 --until 2026-10-21 --out <dir> [--exclude <dir>]...
{{command}} slack eval run --fixtures <dir> --out <dir> [--split dev|holdout|all] [--label shipped] [--prompt <file>] \
  [--extractor-tier fast] [--escalate-over-chars 0] [--judge-tier frontier] [--max-usd 20] [--jobs 4] [--language English] [--bar 0.8]
{{command}} slack eval score --out <dir> --label <name> [--bar 0.8]
{{command}} slack eval optimize --out <dir> --label <name> --prompt <file> --new <file>
```

- `export` writes fixtures from your own stored Slack messages. They are your real conversations: `--out` (and `optimize`'s `--new`) must be outside any git checkout, and the command refuses a path inside one.
- `run` runs the stages on a split and writes `<out>/<label>/manifest.json` and `score.json`. It stops starting calls before passing `--max-usd`; each extractor call is capped at $0.50 and each judge-stage call at $1.00.
- `score` prints the score of a finished run again and checks the bar.
- `optimize` reads the dev judgments of a run and writes one candidate prompt to `--new`, with a changelog. The call is capped at $5.

## Splits and the bar

A fixture is in the `dev` or the `holdout` split; `export` marks every fixture holdout. Change prompts from dev judgments only, so the holdout stays a measure.

Per split, the score counts the fixtures, the share judged "same actions", the critical or major judgments, the recall of your actions, decisions and chases, contradictions, extras, the causes and the cost, and the extractor's own spend: its cost and the tokens the agent reports for a call, one total, in total and per conversation. Each stage file's `.key.json` keeps the call's cost and tokens. With `--bar <share>`, the run fails when there is no holdout, the holdout is incomplete, any holdout judgment is critical or major, or the holdout's "same actions" share is below the bar. `--bar` defaults to 0, which checks nothing.

The shipped prompt is held to 0.8 on the holdout:

```sh
{{command}} slack eval run --fixtures <dir> --out <dir> --split holdout --extractor-tier fast --judge-tier frontier --bar 0.8 --max-usd 40
```
