# MessageRow

*Proposed.* An Inbox entry that needs a decision: an agent's question, an `inbox_post`, or a proposal to change a confirmed item.

The `sg-message` glyph on an `erow msg`, the question as a title that wraps, who asks (the turn's intent, from the profile) and the source, then the options inline: the recommended one `primary`, the rest default, and "Answer…" opening the inspector. A proposal shows Accept and Reject.

## Consumer provides
The question and its options as the daemon serves them; option labels are the record's own words.
