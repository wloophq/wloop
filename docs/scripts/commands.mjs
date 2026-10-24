// Writes the commands reference page from the CLI's own help: the main
// help's groups, then each command's help. Takes the built binary and an
// empty directory for its config and state, so no profile of yours adds its
// intents:
//   make docs-ref
import { execFileSync } from 'node:child_process';
import { existsSync, readFileSync } from 'node:fs';

const [binary, home] = process.argv.slice(2);
if (!binary || !home) throw new Error('usage: node commands.mjs <binary> <empty-dir>');
const env = {
  ...process.env,
  XDG_CONFIG_HOME: `${home}/config`,
  XDG_STATE_HOME: `${home}/state`,
  COLUMNS: '1000',
  NO_COLOR: '1',
  GH_FORCE_TTY: '',
  CLICOLOR_FORCE: '',
};
const help = (...args) => execFileSync(binary, ['help', ...args], { env, encoding: 'utf8' });

const isHeading = (l) => /^[A-Z][A-Z ]+$/.test(l);
// blocks splits a help into its sections: an upper-case heading and its
// lines, or a paragraph with no heading.
const blocks = (text) => {
  const out = [];
  for (const chunk of text.trim().split(/\n\s*\n/)) {
    const lines = chunk.split('\n');
    if (isHeading(lines[0])) out.push({ title: lines[0], lines: lines.slice(1) });
    else out.push({ title: '', lines });
  }
  return out;
};
// rows reads a two-column list; a line indented past the labels goes on
// with the text of the row above.
const rows = (lines) => {
  const out = [];
  for (const l of lines) {
    const m = /^ {2}(\S.*?)(?: {2,}(\S.*))?$/.exec(l);
    if (m) out.push({ label: m[1], text: m[2] ?? '' });
    else out.at(-1).text = (out.at(-1).text + ' ' + l.trim()).trim();
  }
  return out;
};

const main = blocks(help());
const usage = main.find((b) => b.title === 'USAGE').lines[0].trim();
const command = usage.slice(0, usage.indexOf(' <command>'));
const docsLine = main.at(-1).lines.find((l) => l.startsWith('Docs: '));
const docsRoot = docsLine.slice('Docs: '.length).trim();

const escapeCommand = (s) => s.split(command).join('{{command}}');
const prose = (s) =>
  escapeCommand(s)
    .replace(/(^|[\s(])(--[a-z][a-z-]*(?:=\w+)?)/g, '$1`$2`')
    .replace(/</g, '&lt;')
    .replace(/>/g, '&gt;')
    .replace(/\*/g, '\\*')
    .replace(/\{\{command\}\}/g, '{{name}}');
const cell = (s) => '`' + escapeCommand(s).replace(/\|/g, '\\|') + '`';
const title = (heading) => heading.charAt(0) + heading.slice(1).toLowerCase();

// pageTitle reads the title of a docs page the help links.
const pageTitle = (path) => {
  const file = new URL(`../src/content/docs/${path}.md`, import.meta.url);
  if (!existsSync(file)) throw new Error(`the help links ${path}, which is no page`);
  return /^title:\s*(.+)$/m.exec(readFileSync(file, 'utf8'))[1].trim();
};

const out = [
  '---',
  'title: Commands',
  'description: Every command of the CLI, generated from its help.',
  '---',
  '',
  "Every command but `doctor`'s local checks, `install`, `uninstall`, `config get` and `app install` talks to the daemon. Run `{{command}} --help` for the same list, including the commands of the intents your profile adds, and `{{command}} <command> --help` for one command's help.",
];

for (const group of main.filter((b) => b.title && b.title !== 'USAGE')) {
  out.push('', `## ${title(group.title)}`);
  for (const entry of rows(group.lines)) {
    const words = entry.label.split('/')[0].split(' ');
    out.push('', `### ${entry.label}`, '', prose(entry.text) + '.');
    for (const b of blocks(help(...words))) {
      switch (b.title) {
        case 'USAGE':
          out.push('', '```sh', ...b.lines.map((l) => escapeCommand(l.trim())), '```');
          break;
        case '':
          out.push('', prose(b.lines.join(' ')));
          break;
        case 'FLAGS':
          out.push('', '| Flag | Description |', '| --- | --- |');
          for (const f of rows(b.lines)) out.push(`| ${cell(f.label)} | ${prose(f.text).replace(/\|/g, '\\|')} |`);
          break;
        case 'EXAMPLES':
          out.push('', 'Examples:', '', '```sh', ...b.lines.map((l) => escapeCommand(l.trim())), '```');
          break;
        case 'DOCS': {
          const path = b.lines[0].trim().slice(docsRoot.length).replace(/\/$/, '');
          out.push('', `See [${pageTitle(path)}](/${path}/).`);
          break;
        }
        default:
          throw new Error(`unknown section ${b.title} in the help of ${entry.label}`);
      }
    }
  }
}
out.push('', 'This page is generated from `{{command}} help` by `make docs-ref`.');
process.stdout.write(out.join('\n') + '\n');
