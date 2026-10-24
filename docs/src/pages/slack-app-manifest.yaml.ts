import template from '../snippets/slack-app-manifest.yaml?raw';
import { product } from '../../site.config.mjs';

export function GET() {
  const body = template.replace(/\{\{(name|command|app|org|repo)\}\}/g, (_: string, key: keyof typeof product) => product[key]);
  return new Response(body, { headers: { 'Content-Type': 'text/yaml; charset=utf-8' } });
}
