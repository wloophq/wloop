<script lang="ts">
  import type { Snippet } from "svelte";
  import { fieldById, fillWord, groupField, lensWords, normValue, toggledQualifier, type Lens } from "../../lib/lens";
  import { setGroup } from "../../lib/view";
  import { readHash, writeHash } from "../../state/route.svelte";
  import { catalog } from "../../state/store.svelte";

  // A value on screen that filters by itself: a click filters by it, Shift groups by its field and Alt leaves it
  // out. A field the lens does not know, or a lens that does not filter, shows its content alone. words, noun and
  // shown word it; rove makes it a stop of a row's roving focus.
  let {
    lens,
    field,
    raw,
    slot,
    words = undefined,
    noun = undefined,
    shown = undefined,
    rove = false,
    children,
  }: {
    lens: Lens;
    field: string;
    raw: string;
    slot: string;
    words?: Record<string, string>;
    noun?: string;
    shown?: string;
    rove?: boolean;
    children: Snippet;
  } = $props();

  const fields = $derived(lens.fields || catalog.fields);
  const f = $derived(fieldById(field, fields));
  const by = $derived(f ? (f.groupBy ? f.groupBy(raw) : f.group ? field : "") : "");
  const group = $derived(by ? groupField(by, fields) : null);
  const w = $derived(words || lens.words || lensWords());
  const vars = $derived(f ? { label: noun || f.label.toLowerCase(), value: shown || (f.show ? f.show(raw) : raw), group: group ? group.label.toLowerCase() : "" } : {});
  const pressed = $derived(lens.quals.some((x) => x.field === field && x.value === normValue(field, raw) && !x.neg));

  function pick(e: MouseEvent | KeyboardEvent) {
    e.stopPropagation();
    if (e.shiftKey && by) return setGroup(by);
    writeHash({ q: toggledQualifier(readHash().q, field, raw, e.altKey, fields) });
  }
</script>

{#if !f || lens.static}
  {@render children()}
{:else}
  <button
    type="button"
    class="facet"
    data-focus={slot}
    tabindex={rove ? -1 : undefined}
    data-rove={rove ? "" : undefined}
    aria-pressed={pressed ? "true" : "false"}
    aria-label={fillWord(w.filter_label, vars)}
    title={fillWord(group ? w.filter_title_group : w.filter_title, vars)}
    onclick={pick}
    onkeydown={(e) => {
      if (e.key === "Enter" && (e.shiftKey || e.altKey)) {
        e.preventDefault();
        pick(e);
      }
    }}>{@render children()}</button>
{/if}
