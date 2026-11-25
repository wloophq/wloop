<script lang="ts">
  import { fmtNumber, t, tn } from "../../lib/i18n.svelte";
  import type { J } from "../../lib/util";
  import { clock } from "../../state/clock.svelte";
  import { openRoute, taskRoute } from "../../state/float.svelte";
  import { ago } from "../chrome/facts";
  import { duration, progress } from "./float";
  import Ring from "./Ring.svelte";

  // What the agents do: the turns running, each against its intent's usual duration, how many ask the user, and
  // the turns done today, the last two first.
  let { turns, failed = false }: { turns: J; failed?: boolean } = $props();

  const SHOWN_DONE = 2;

  let hidden = $state(false);
  let more = $state(false);

  const running: J[] = $derived((turns && turns.running) || []);
  const done: J[] = $derived((turns && turns.done) || []);
  const asked = $derived((turns && turns.asked) || 0);
  const usual = $derived((turns && turns.usual) || {});
  const summary = $derived(
    [
      running.length ? tn("{n} running", running.length) : "",
      asked ? tn("{n} waiting on you", asked) : "",
      done.length ? tn("{n} done today", done.length) : "",
    ].filter(Boolean).join(" · "),
  );
  const shownDone = $derived(more ? done : done.slice(0, SHOWN_DONE));
  const elapsed = (r: J) => Math.max(0, clock.now - Date.parse(r.started));
  const open = (r: J) => openRoute(taskRoute(r.task));
</script>

{#snippet what(r: J)}
  <span class="fl-what"><b>{r.label || r.intent}</b>{[r.unit, r.title].filter(Boolean).map((s) => " · " + s).join("")}</span>
{/snippet}

{#if failed}
  <section class="fl-agents" aria-label={t("Agents")}><p class="fl-agents-off">{t("Could not read the agents.")}</p></section>
{:else if running.length || done.length || asked}
  <section class="fl-agents" aria-label={t("Agents")}>
    <div class="fl-agents-head">
      <span>{t("Agents")}</span>
      <span class="fl-sum">{summary}</span>
      <span class="fl-sp"></span>
      <button type="button" class="fl-quiet" aria-expanded={hidden ? "false" : "true"} onclick={() => (hidden = !hidden)}>{hidden ? t("Show") : t("Hide")}</button>
    </div>
    {#if !hidden}
      {#each running as r (r.seq)}
        {@const u = usual[r.intent] || 0}
        <button type="button" class="fl-turn" onclick={() => open(r)} disabled={!r.task}>
          <Ring value={progress(elapsed(r), u)} />
          {@render what(r)}
          <span class="fl-took run">{u ? t("{elapsed} / {usual}", { elapsed: duration(elapsed(r)), usual: duration(u) }) : duration(elapsed(r))}</span>
          <span class="fl-bar" aria-hidden="true"><i style:width={`${progress(elapsed(r), u) * 100}%`}></i></span>
        </button>
      {/each}
      {#each shownDone as r (r.seq)}
        <button type="button" class="fl-turn done" onclick={() => open(r)} disabled={!r.task}>
          <span class="fl-check" aria-hidden="true">✓</span>
          {@render what(r)}
          <span class="fl-took">{[duration(r.duration_ms || 0), ago(r.ended)].filter(Boolean).join(" · ")}</span>
        </button>
      {/each}
      {#if done.length > SHOWN_DONE}
        <button type="button" class="fl-link" onclick={() => (more = !more)}>{more ? t("Fewer") : tn("Show {n} more done", done.length - SHOWN_DONE, { n: fmtNumber(done.length - SHOWN_DONE) })}</button>
      {/if}
    {/if}
  </section>
{/if}
