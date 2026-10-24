// Writes the facts reference page from the factsdoc tables, read on standard input:
//   make docs-ref
import { readFileSync } from 'node:fs';

const tables = readFileSync(0, 'utf8').trim().split(/\n\n+/);
if (tables.length < 2) throw new Error('expected the factsdoc tables on standard input');

const intro = {
  '| Fact |': [
    '## Intent facts',
    '',
    'The predicates of an intent manifest read these. The **action** slot (`[action]` predicates) reads the row the daemon serves; the **turn** slot (`records`, headline cases, `also_profiles.when`) reads the turn being built.',
  ],
  '| Step |': [
    '## Workflow steps',
    '',
    'The steps a `[[step]]` may name with `use`. `{{command}} workflows steps` prints them with their arguments.',
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
      'A workflow\'s `when` reads the facts of its subject\'s slot, plus `trigger.*`, `config.*` and `now`. `{{command}} workflows facts [github|session|workspace]` prints them. In a workflow, `pr.repo` is `owner/repo`; in an intent it is the repository name.',
    );
    workflowHeading = true;
  }
  out.push('', table);
}
out.push('', 'This page is generated from the binary\'s fact registry by `make docs-ref`.');
process.stdout.write(out.join('\n') + '\n');
