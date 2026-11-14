<script lang="ts" module>
  import { SvelteSet } from "svelte/reactivity";

  // opened are the keys of the texts the reader opened, so a text stays open wherever and whenever it is drawn
  // again under its key.
  const opened = new SvelteSet<string>();

  // foldTexts folds every text the reader opened, as a page starts.
  export function foldTexts(): void {
    opened.clear();
  }
</script>

<script lang="ts">
  import { t } from "../../lib/i18n.svelte";
  import { foldedBlocks, markdownBlocks, markdownInline, type Block, type Inline, type Names } from "../../lib/markdown";

  // Agent prose drawn from markdown.ts's tree with elements and text nodes only. A text longer than fold blocks
  // shows its first fold blocks and a button that opens the rest; the reader's open stays with key, which names
  // the text.
  let { text, fold = 0, names = null, key = "" }: { text: unknown; fold?: number; key?: string; names?: Names } = $props();

  let openedHere = $state(false);
  const open = $derived(key ? opened.has(key) : openedHere);
  const view = $derived(foldedBlocks(markdownBlocks(text), open ? 0 : fold));
</script>

{#snippet inline(nodes: Inline[])}{#each nodes as n, i (i)}{#if typeof n === "string"}{n}{:else if n.tag === "br"}<br />{:else if n.tag === "code"}<code>{n.text}</code>{:else if n.tag === "strong"}<strong>{@render inline(n.children)}</strong>{:else if n.tag === "em"}<em>{@render inline(n.children)}</em>{:else if n.tag === "a"}<a href={n.href} target="_blank" rel="noopener noreferrer">{@render inline(n.children)}</a>{:else if n.tag === "mention"}<span class="mention" title={n.key}>{n.text}</span>{/if}{/each}{/snippet}

{#snippet block(b: Block)}
  {#if b.kind === "code"}
    <pre><code>{b.text}</code></pre>
  {:else if b.kind === "heading"}
    <h4>{@render inline(markdownInline(b.text, names))}</h4>
  {:else if b.kind === "quote"}
    <blockquote>{#each b.blocks as x, i (i)}{@render block(x)}{/each}</blockquote>
  {:else if b.kind === "ul"}
    <ul>{#each b.items as it, i (i)}<li>{@render inline(markdownInline(it, names))}</li>{/each}</ul>
  {:else if b.kind === "ol"}
    <ol>{#each b.items as it, i (i)}<li>{@render inline(markdownInline(it, names))}</li>{/each}</ol>
  {:else if b.kind === "p"}
    <p>{@render inline(markdownInline(b.text, names))}</p>
  {/if}
{/snippet}

<div class="md">{#each view.shown as b, i (i)}{@render block(b)}{/each}{#if view.folded}<button type="button" class="md-more" onclick={() => (key ? opened.add(key) : (openedHere = true))}>{t("Read all")}</button>{/if}</div>
