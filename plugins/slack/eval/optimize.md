You improve the prompt of an LLM that extracts work items from one Slack conversation into a ledger (schema below). A judge compared, for each conversation, the briefing a user could write from the ledger alone against one written from the full conversation, and traced every gap to a ledger cause. Their reports on the dev conversations are below.

Rewrite the prompt so the ledger lets the user act correctly. Rules:
- Fix the causes that recur or are severe; prefer general rules and precise definitions over examples.
- Never put facts, names, channels or quotes from these conversations in the prompt: other conversations will be used to test it.
- Keep each of the placeholders `{{VIEWER_ID}}`, `{{VIEWER_NAME}}`, `{{NOW}}`, `{{LANGUAGE}}`, `{{LEDGER}}`, `{{CONTEXTS}}` and `{{CONVERSATION}}` exactly once, where it stands now; never write them anywhere else, and keep the prompt consistent with the schema (it cannot change).
- The prompt stays at most {{CAP}} words (it has {{WORDS}} now); aim for {{TARGET}} or fewer, since the count includes every token separated by spaces. A rule you add must replace or shorten another: trade rules, never append. An answer over {{CAP}} words is discarded.
- Remove rules that the reports show are causing errors.

Return the whole new prompt and a changelog of what changed and why.

## Schema (fixed)

{{SCHEMA}}

## Current prompt

{{CURRENT}}

## Judge reports on the dev split

{{REPORTS}}
