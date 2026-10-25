## Stance

A review comment is a claim to verify, not an order. Check each one in the
code before acting: apply what holds, apply a better fix when the problem is
real and the suggestion is not the best answer, and decline with a technical
reason when the claim does not hold.

## Risky surfaces

Schema migrations, data deletion or backfills, auth and permissions,
billing, and infrastructure. An item that touches one of them and leaves you
unsure goes to the user first.

## Commits

- Follow the repository's conventions: the agent instruction files from the
  root down to each touched directory, the nearest one winning.
- One commit per item or per group of closely related items, in the message
  style the repository uses.
- New or changed behavior gets a test; docs that describe changed code are
  updated in the same round.
- Run the tests and linters that cover what you changed before pushing.
- When GitHub reports conflicts with the base, resolve them in the round:
  fetch the base from the repository the pull request targets (`origin`,
  or the remote that points at it when the branch lives in a fork) and
  merge it into the branch, never rebase it or force-push. Resolve each
  conflict keeping what both sides meant, run the tests that cover the
  touched code, commit the merge and push. Say in the report that the base
  was merged in and which conflicts it resolved. Merge only on a clean
  checkout with no local commits you do not know; otherwise, and for a
  conflict you cannot resolve with confidence, abort the merge
  (`git merge --abort`) and go to the user first.
- Otherwise leave the branch's relation to the base alone: do not merge or
  rebase the base in on your own.

## Replies

One or two sentences in the thread's language: what changed and the commit,
or why not, with the evidence. A question gets its answer and nothing else.

## The comment asking for the next look

A round that only merged the base in posts none: the push speaks for itself.

Short, addressed to the reviewers by `@login`, in the pull request's
language: what was applied, what was declined or left open and why, and the
one thing the reviewer should look at again. Read your earlier top-level
comments on the pull request first, and do not repeat their opening or
their structure.

## Who is asked again

- Do not re-request a reviewer whose approval still stands: the request
  replaces the approval with a pending review, and the pull request loses an
  approval it had. Address them in the comment only when the round answered
  something of theirs, and say no new approval is needed.
- Bots that review on request get their own line after the text, asking them
  to review the new head by its short SHA.
