// Writes the facts reference page from the factsdoc tables, read on standard input:
//   make docs-ref
import { readFileSync } from 'node:fs';

const tables = readFileSync(0, 'utf8').trim().split(/\n\n+/);
if (tables.length < 2) throw new Error('expected the factsdoc tables on standard input');

const intro = {
  '| Fact |': [
    '## Intent facts',
    '',
    'The predicates of an intent manifest read these. The **action** slot (`[action]` predicates) reads the row the daemon serves; the **turn** slot (`[record] when`, headline cases, `also_profile[].when`) reads the turn being built.',
  ],
  '| Approval fact |': [
    '## Draft approval facts',
    '',
    'A `[drafts.approval]` rule\'s `when` reads these, the facts of the draft, plus the facts of the unit the draft is on (a workflow\'s: its provider\'s), never `brief.*`, `trigger.*`, `config.*` or a fact marked **Untrusted**, whose value a third party\'s text, read by the extraction, or the proposing turn itself sets. A rule over the facts of another provider than the draft\'s unit\'s does not apply to it. A rule that releases a draft on its own (`send` or `delay`) applies to a comment draft only when its `when` has `draft.delivery == "comment"` as a term of its top-level `&&`.',
  ],
  '| Step |': [
    '## Workflow steps',
    '',
    'The steps a `[[step]]` may name with `use`. `{{command}} workflows steps` prints them with their arguments.',
  ],
  '| Effect |': [
    '## Draft effects',
    '',
    'What a sent draft applies, once: the names a `draft.propose` step lists in `effects` and an intent lists in `[drafts] allowed_effects`. A turn\'s own `effects` are on the [language](/reference/language/) page.',
  ],
};

const out = [
  '---',
  'title: Facts and steps',
  'description: The facts that intent and workflow predicates read, and the steps a workflow can run.',
  '---',
  '',
  'Predicates are CEL expressions; their syntax is on the [intent manifest](/reference/intent-manifest/#predicates) page. A missing fact reads as its type\'s zero value.',
];
let workflowHeading = false;
for (const table of tables) {
  const key = Object.keys(intro).find((k) => table.startsWith(k));
  if (key) {
    out.push('', ...intro[key]);
  } else if (!workflowHeading) {
    out.push(
      '',
      '## Workflow facts',
      '',
      'A workflow\'s `when` reads the facts of its provider\'s slot, plus `trigger.*`, `config.*` and `now`; a provider whose units have a brief also reads `brief.*`, and `[drafts.withdraw] when` also reads `draft.*`. `{{command}} workflows facts [entry|github|session|slack|workspace]` prints them. In a workflow, `pr.repo` is `owner/repo`; an intent reads the repository name as `pr.repo_name`.',
    );
    workflowHeading = true;
  }
  out.push('', table);
}
out.push('', 'This page is generated from the binary\'s fact registry by `make docs-ref`.');
process.stdout.write(out.join('\n') + '\n');
