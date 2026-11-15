<script lang="ts">
  import { providerName } from "../../lib/catalog";
  import { t } from "../../lib/i18n.svelte";
  import { fillWord } from "../../lib/lens";
  import { personName, unitLabel } from "../../lib/tasks";
  import { safeURL, type J } from "../../lib/util";
  import { catalog } from "../../state/store.svelte";
  import { stamp, unitRefHref } from "./unit";

  // A unit's stored card; signals false leaves its signals to a grid that shows them.
  let { s, signals = true }: { s: J; signals?: boolean } = $props();

  const c = $derived(s.card || {});
  const u = $derived(c.unit || {});
  const shown = $derived(signals ? c.signals || [] : []);
  const lh = $derived(c.last_human);

  // signalWords are a card signal in its plugin's words (the catalog's card_signals): its name, and its value
  // filled with its params followed by each note in its tone. A code with no words shows as itself, and an empty
  // one as its key.
  function signalWords(g: J): { label: string; value: string; notes: { tone: string; text: string }[] } {
    const spec = (catalog.cardSignals || []).find((x: J) => x.provider === u.provider && x.key === g.key) || {};
    const word = (spec.values || {})[g.value];
    return {
      label: spec.label || g.key,
      value: g.at ? stamp(g.at) : word ? fillWord(word, g.params || {}) : g.value || g.key,
      notes: (g.notes || []).map((n: J) => {
        const w = (spec.notes || {})[n.code];
        return { tone: n.tone || "neutral", text: w ? fillWord(w, n.params || {}) : n.code };
      }),
    };
  }
</script>

<section class={"d-card" + (s.stale ? " stale" : "")} aria-label={t("Card")}>
  <div class="meta">
    <a href={unitRefHref(u)} class="ref">{c.label || unitLabel(c.unit)}</a>{#if c.link}{" "}<a href={safeURL(c.link)} target="_blank" rel="noopener noreferrer" class="ext">{providerName(u.provider)}</a>{/if}{#if c.state}{" "}<span class="pill">{c.state}</span>{/if}{#if c.outcome}{" "}<span class="pill">{c.outcome}</span>{/if}{#if s.stale}{" "}<span class="flag">{t("plugin disabled")}</span>{/if}
  </div>
  {#if c.title}<div class="title">{c.title}</div>{/if}
  {#if c.why}<div class="why">{c.why}</div>{/if}
  {#if (c.waits_on || []).length}<div>{t("Waiting on")} <strong>{c.waits_on.map(personName).join(", ")}</strong></div>{/if}
  {#if shown.length}
    <ul class="signals">
      {#each shown as g (g.key)}
        {@const w = signalWords(g)}
        <li class={"tone-" + (g.tone || "neutral")}><span class="k">{w.label}</span> {w.value}{#each w.notes as n, i (i)}{" · "}<span class={"tone-" + n.tone}>{n.text}</span>{/each}</li>
      {/each}
    </ul>
  {/if}
  {#if lh}<div class="meta">{`${personName(lh.who)}: ${lh.what}`}{lh.at ? ` (${stamp(lh.at)})` : ""}</div>{/if}
</section>
