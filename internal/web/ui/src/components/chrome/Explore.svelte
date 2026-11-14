<script lang="ts">
  import { exploreSearch } from "../../lib/catalog";
  import { t } from "../../lib/i18n.svelte";
  import { rowByKey } from "../../lib/lens";
  import { keyOf, labelOf, type J } from "../../lib/util";
  import { EXPLORE_MAX, readHash, writeHash } from "../../state/route.svelte";
  import { busy, drafts, state } from "../../state/store.svelte";
  import ActionButtons from "../actions/ActionButtons.svelte";
  import Candidate from "./Candidate.svelte";
  import { actOnCandidate, candidateURL, copyCandidates, explore, exploreStatus, hasTerm, hasWindow, pickSort, querySort, toggleTerm, toggleWindow } from "./explore";

  // Explore: a search of the service the served search runs on, its presets, filters, orders and recent queries
  // as chips over the text the hash keeps, the candidates it found, and the actions on the one selected with the
  // guidance typed for it.
  let { shown = true }: { shown?: boolean } = $props();

  const ex = $derived(state.explore);
  const x = $derived(readHash().x);
  const search = $derived(exploreSearch());
  const cands = $derived(((ex.result && ex.result.candidates) || []) as J[]);
  const picked = $derived(cands.find((c) => keyOf(c) === ex.sel));
  const pickedKey = $derived(picked ? keyOf(picked) : "");
  const pickedRow = $derived(picked ? rowByKey(pickedKey) : null);
</script>

<section id="explore" class="explore" aria-label={t("Explore pull requests")} hidden={!shown}>
  <form
    id="explore-form"
    class="explore-form"
    role="search"
    onsubmit={(e) => {
      e.preventDefault();
      explore((e.currentTarget.querySelector("#explore-q") as HTMLInputElement).value);
    }}>
    <input
      id="explore-q"
      type="search"
      maxlength="256"
      autocomplete="off"
      spellcheck="false"
      data-focus="explore:q"
      aria-label={t("Pull request URL, owner/repo, a repository or login, @org/team or a search")}
      placeholder={t("PR URL, owner/repo, repo, login, @org/team or a search")}
      value={x}
      oninput={(e) => writeHash({ x: e.currentTarget.value.slice(0, EXPLORE_MAX) }, true)} />
    <button id="explore-go" type="submit" class="act primary" data-focus="explore:go" disabled={ex.busy}>{t("Search")}</button>
  </form>
  <div id="explore-chips" class="chips">
    <span class="chip-head">{t("Run")}</span>
    {#each search.presets || [] as p (p.query)}
      <button type="button" class="chip" title={p.query} disabled={ex.busy} data-focus={`explore:preset:${p.query}`} onclick={() => explore(p.query)}>{p.label}</button>
    {/each}
    <span class="chip-head">{t("Filters")}</span>
    {#each search.filters || [] as f (f.window ? "window" : f.term)}
      {#if f.window}
        <button type="button" class="chip" title="updated:>=" aria-pressed={(hasWindow(x)) ? "true" : "false"} data-focus="explore:filter:window" onclick={() => toggleWindow(f.window)}>{f.label}</button>
      {:else}
        <button type="button" class="chip" aria-pressed={(hasTerm(x, f.term)) ? "true" : "false"} data-focus={`explore:filter:${f.term}`} onclick={() => toggleTerm(f.term)}>{f.label}</button>
      {/if}
    {/each}
    <span class="chip-head">{t("Order")}</span>
    {#each search.sorts || [] as o (o.sort)}
      <button type="button" class="chip" title={`sort:${o.sort}`} aria-pressed={(querySort(x) === o.sort) ? "true" : "false"} data-focus={`explore:sort:${o.sort}`} onclick={() => pickSort(o.sort)}
        >{o.label}</button
      >
    {/each}
    {#if ex.recent.length}<span class="chip-head">{t("Recent")}</span>{/if}
    {#each ex.recent as q (q)}
      <button type="button" class="chip" title={q} data-focus={`explore:recent:${q}`} onclick={() => explore(q)}>{q}</button>
    {/each}
  </div>
  <p id="explore-status" class={"explore-status" + (ex.error && !ex.busy ? " err" : "")} role="status" aria-live="polite">
    {exploreStatus()}{#if cands.length}<button
        type="button"
        class="act"
        data-focus="explore:copy"
        title={t("Copy these candidates as YAML, to paste for an agent")}
        onclick={copyCandidates}>{t("Copy list")}</button
      >{/if}
  </p>
  <div id="explore-act" class="explore-act" hidden={!picked}>
    <div id="explore-act-head" class="meta">
      {#if picked}<a href={candidateURL(picked)} target="_blank" rel="noopener noreferrer" data-focus="explore:gh">{labelOf(picked)}</a>{" " + picked.title}{/if}
    </div>
    <textarea
      id="explore-guidance"
      rows="2"
      aria-label={t("Guidance")}
      data-focus="explore:guidance"
      placeholder={t("Guidance for the action you pick (optional)")}
      value={pickedKey ? drafts.guidance[pickedKey] || "" : ""}
      oninput={(e) => {
        if (pickedKey) drafts.guidance[pickedKey] = e.currentTarget.value;
      }}></textarea>
    <div id="explore-act-buttons" class="d-actions">
      {#if picked}
        <ActionButtons subject={picked} surface="explore" scope="explore" run={(id) => actOnCandidate(picked, id, { guidance: (drafts.guidance[pickedKey] || "").trim() })} />
        {#if pickedRow}
          <button type="button" class="act" disabled={busy.has(pickedKey)} data-focus="explore:detail" onclick={() => writeHash({ pr: pickedKey })}>{t("Detail")}</button>
        {/if}
      {/if}
    </div>
  </div>
  <ol id="explore-results" class="cands" aria-label={t("Candidates")}>
    {#each cands as c (keyOf(c))}<Candidate {c} sel={ex.sel} />{/each}
  </ol>
</section>
