import { readFileSync } from 'node:fs';
import { visit } from 'unist-util-visit';

const TOKEN = /\{\{(name|command|app|org|repo)\}\}/g;

/**
 * Replaces the product tokens in text, inline code, code blocks and link
 * targets, prefixes root-relative links with the site's base, and fills a
 * code block whose meta says file="<name>" with that file of snippetsDir.
 */
export default function remarkProduct({ product, base, snippetsDir }) {
  const fill = (s) => s.replace(TOKEN, (_, key) => product[key]);
  const prefix = base.replace(/\/$/, '');

  return (tree) => {
    visit(tree, (node) => {
      if (node.type === 'code') {
        const file = /file="([^"]+)"/.exec(node.meta ?? '')?.[1];
        if (file) {
          node.value = readFileSync(new URL(file, snippetsDir), 'utf8').trimEnd();
          node.meta = node.meta.replace(/\s*file="[^"]+"/, '') || null;
        }
      }
      if (typeof node.value === 'string' && ['text', 'inlineCode', 'code', 'html'].includes(node.type)) {
        node.value = fill(node.value);
      }
      if (typeof node.url === 'string') {
        node.url = fill(node.url);
        if (node.url.startsWith('/') && !node.url.startsWith('//') && !node.url.startsWith(prefix + '/')) {
          node.url = prefix + node.url;
        }
      }
    });
  };
}
