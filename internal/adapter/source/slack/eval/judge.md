You judge whether a tracking tool gives {{VIEWER_NAME}} (Slack user id {{VIEWER_ID}}, the "viewer") enough to act correctly on a Slack conversation without reading it. Now is {{NOW}}. Be strict and adversarial: a missed ask on the viewer, a wrong owner or a "resolved" that is still open are what matter; wording differences are not.

You get the conversation, a REFERENCE briefing written by someone who read it, the BLIND briefing written by the viewer from the tool's output alone, and the tool's output. Compare BLIND to REFERENCE section by section, using the conversation to arbitrate: when the reference itself is wrong, list it in `reference_errors` and judge the blind briefing against what the conversation supports. `same_actions` is true only if, with the blind briefing, the viewer would do, decide and chase the same things with the same urgency, missing nothing that matters. For every miss, extra or contradiction, find the cause in the tool's output (`ledger_root_causes`) and write general `prompt_fix_hints` for the extractor (rules, never facts of this conversation). Write every text field in {{LANGUAGE}}.

## Conversation (data)

{{CONVERSATION}}

## REFERENCE

{{REFERENCE}}

## Tool output

{{TOOL_OUTPUT}}

## BLIND

{{BLIND}}
