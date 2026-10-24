import { defineConfig } from 'astro/config';
import starlight from '@astrojs/starlight';
import remarkProduct from './src/plugins/remark-product.mjs';
import { product } from './site.config.mjs';

const site = `https://${product.org}.github.io`;
const base = `/${product.name}`;

export default defineConfig({
  site,
  base,
  trailingSlash: 'always',
  markdown: {
    remarkPlugins: [[remarkProduct, { product, base, snippetsDir: new URL('./src/snippets/', import.meta.url) }]],
  },
  integrations: [
    starlight({
      title: product.name,
      description: 'A work loop for GitHub pull requests and Slack, with AI coding agents doing the rounds in local worktrees.',
      social: [{ icon: 'github', label: 'GitHub', href: `https://github.com/${product.repo}` }],
      editLink: { baseUrl: `https://github.com/${product.repo}/edit/main/docs/` },
      lastUpdated: false,
      sidebar: [
        {
          label: 'Getting started',
          items: [
            { label: 'Overview', slug: 'index' },
            'getting-started/requirements',
            'getting-started/install',
            'getting-started/init',
            'getting-started/first-review',
          ],
        },
        {
          label: 'Concepts',
          items: [
            'concepts/turns',
            'concepts/daemon',
            'concepts/surfaces',
            'concepts/workflows',
            'concepts/approval',
            'concepts/security',
            'concepts/brief',
            'concepts/profiles',
            'concepts/gtd',
            'concepts/slack',
            'concepts/board',
          ],
        },
        {
          label: 'Guides',
          items: [
            'guides/review',
            'guides/address',
            'guides/take-over',
            'guides/nudge',
            'guides/review-profile',
            'guides/custom-intent',
            'guides/workflows',
            'guides/slack',
            'guides/upgrade',
            'guides/uninstall',
            'guides/troubleshooting',
          ],
        },
        {
          label: 'Reference',
          items: [
            'reference/commands',
            'reference/configuration',
            'reference/profile-blocks',
            'reference/intent-manifest',
            'reference/workflow-manifest',
            'reference/facts',
            'reference/keys',
            'reference/cockpit-filter',
            'reference/files',
            'reference/slack-eval',
          ],
        },
        {
          label: 'Architecture',
          items: ['architecture/overview', 'architecture/glossary', 'architecture/development'],
        },
      ],
    }),
  ],
});
