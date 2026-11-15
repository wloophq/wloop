<script lang="ts">
import { taskCatalog } from "../../state/verbs";
  import { fmtDate } from "../../lib/i18n.svelte";
  import { ageText, lensWords, rowParts, type Piece } from "../../lib/lens";
  import type { J } from "../../lib/util";
  import { clock } from "../../state/clock.svelte";
  import { stamp } from "./task";

  // Whom the task waits on and since when, as its row says it.
  let { e }: { e: J } = $props();

  const words = $derived(lensWords());
  const specs = $derived(taskCatalog().facets || []);
  const parts = $derived(rowParts({ facets: e.facets || {} }, specs, words, clock.now));
  const who = $derived(parts.who || []);
  const age = $derived(parts.age || []);

  function label(p: Piece): string {
    return p.template ? p.label.replaceAll("{{.at}}", fmtDate(p.raw, "datetime")) : p.label;
  }

  function ageTitle(p: Piece): string | undefined {
    const at = Date.parse(p.raw);
    return Number.isNaN(at) ? undefined : `${ageText(words, clock.now - at)} (${stamp(p.raw)})`;
  }

  // restOf are the words of the values of field the task holds that pieces leave out past its show_max.
  function restOf(pieces: Piece[], field: string): string | undefined {
    const shown = new Set(pieces.filter((p) => p.field === field && p.value).map((p) => p.raw));
    const labels = (specs.find((s: J) => s.id === field) || {}).labels || {};
    return ((e.facets || {})[field] || []).filter((v: string) => !shown.has(v)).map((v: string) => labels[v] || v).join(", ") || undefined;
  }
</script>

{#snippet piece(p: Piece, part: string, pieces: Piece[])}
  {#if !p.value}<span class={part} data-field={p.field} data-more="" title={restOf(pieces, p.field)}>{p.label}</span>{:else}<span class={part} data-field={p.field} data-value={p.value} title={part === "age" ? ageTitle(p) : undefined}>{label(p)}</span>{/if}
{/snippet}

{#if who.length || age.length}
  <div class="d-when"><span class="when"><span class="who">{#each who as p, i (p.field + ":" + p.value + ":" + i)}{#if i}{", "}{/if}{@render piece(p, "who", who)}{/each}</span>{#each age as p, i (p.field + ":" + i)}{@render piece(p, "age", age)}{/each}</span></div>
{/if}
