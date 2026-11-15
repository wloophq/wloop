<script lang="ts">
  import { onMount } from "svelte";
  import { t } from "../../lib/i18n.svelte";
  import { inView, matches, tagKeys, type Lens } from "../../lib/lens";
  import type { J } from "../../lib/util";
  import { minute } from "../../state/clock.svelte";
  import { readHash, writeHash } from "../../state/route.svelte";
  import { state as store } from "../../state/store.svelte";
  import { chart, charts } from "./chart";
  import { DASH_PANELS, dashClickQuery, dashKpis, dashPalette, dashTheme } from "./dash";

  // The backlog dashboard: the counts the flags filter by, then a chart per panel over the rows the lens keeps; a
  // click on a datum filters by it, Alt+click leaves it out.
  let { lens, shown = true }: { lens: Lens; shown?: boolean } = $props();

  let dark = $state(charts.dark());
  onMount(() => {
    if (typeof matchMedia !== "function") return;
    const mq = matchMedia("(prefers-color-scheme: dark)");
    const on = () => (dark = mq.matches);
    mq.addEventListener("change", on);
    return () => mq.removeEventListener("change", on);
  });

  const loaded = $derived(!!charts.lib());
  const theme = $derived(dark ? "loop-dark" : "loop-light");
  const pal = $derived.by(() => {
    if (!shown || !loaded) return null;
    const read = (name: string) => getComputedStyle(document.documentElement).getPropertyValue(name);
    const p = dashPalette(read);
    charts.lib().registerTheme(theme, dashTheme(p, read("--sans").trim()));
    return p;
  });
  const rows = $derived(store.rows.filter((r) => inView(r, "dash", lens.done) && matches(r, lens.ts)));
  const kpis = $derived(dashKpis(rows, lens));
  const keys = $derived(tagKeys(rows));
  const ctx = $derived({ rows, lens, pal, now: minute(), health: store.health, tk: lens.tk, stalledHours: (store.health && store.health.stalled_review_hours) || 0 });
  const options = $derived(pal ? DASH_PANELS.map((p) => p.build(ctx)) : []);

  function clicked(p: J) {
    const d = p.data || {};
    if (d.pr) {
      writeHash({ pr: d.pr });
      return;
    }
    if (!d.q) return;
    const e = p.event && p.event.event;
    writeHash({ q: dashClickQuery(readHash().q, d.q, !!(e && e.altKey)) });
  }
</script>

<section id="dash" class="dash" aria-label={t("Backlog dashboard")} hidden={!shown}>
  {#if shown && !loaded}
    <p class="empty">{t("The charts library did not load; reload the page.")}</p>
  {:else if shown && pal}
    <div class="dash-head">
      <ul id="dash-kpis" class="kpis" aria-label={t("Counts")}>
        {#each kpis as k (k.id)}
          <li>
            {#if k.flag}
              <button
                type="button"
                class={"kpi" + (k.id === "stalled" ? " late" : "")}
                aria-pressed={k.on ? "true" : "false"}
                data-focus={`dash:kpi:${k.id}`}
                title={t("Filter is:{flag}; Alt+click excludes", { flag: k.flag })}
                onclick={(e) => writeHash({ q: dashClickQuery(readHash().q, [["is", k.flag as string]], e.altKey) })}
                ><span class="n">{k.n}</span><span class="label">{k.label}</span></button
              >
            {:else}
              <span class="kpi"><span class="n">{k.n}</span><span class="label">{k.label}</span></span>
            {/if}
          </li>
        {/each}
      </ul>
      <button id="dash-done" type="button" class="act" data-focus="dash:done" aria-pressed={lens.done ? "true" : "false"} onclick={() => writeHash({ done: !readHash().done })}
        >{t("Include done")}</button
      >
      <span class="dash-hint">{t("Click a chart to filter; Alt+click to exclude.")}</span>
    </div>
    <div class="dash-grid">
      {#each DASH_PANELS as p, i (p.id)}
        <section class={"panel" + (p.wide ? " wide" : "")} aria-label={p.title}>
          <h3 class="panel-head">
            <span>{p.title}</span>
            {#if p.tags}
              <select id="dash-tk" data-focus="dash:tk" aria-label={t("Tag key")} value={lens.tk} onchange={(e) => writeHash({ tk: e.currentTarget.value })}>
                <option value="">{t("All keys")}</option>
                {#each keys as k (k)}<option value={k}>{k}</option>{/each}
              </select>
            {/if}
          </h3>
          <div id={`dash-${p.id}`} class="chart" role="img" aria-label={p.title} hidden={!options[i]} use:chart={{ option: options[i], theme, click: clicked }}></div>
          <p id={`dash-${p.id}-none`} class="nodata" hidden={!!options[i]}>{t("No data for this filter yet.")}</p>
        </section>
      {/each}
    </div>
  {/if}
</section>
