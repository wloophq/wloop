<script lang="ts">
  import { t, tn } from "../../lib/i18n.svelte";
  import type { J } from "../../lib/util";
  import { drafts, homeState } from "../../state/store.svelte";
  import ScreenRow from "./ScreenRow.svelte";
  import { capture } from "./screen";

  // The home: the briefing the daemon words, its counters as links to their queues or views, the first rows of its
  // sections and the capture field. The daemon decides every word and count; this only draws them.
  let { hidden = false }: { hidden?: boolean } = $props();

  const home = $derived(homeState.home);

  // counterHref is where a counter leads: its decision kind's queue, else its view.
  function counterHref(c: J): string {
    return c.kind ? "#queue=" + encodeURIComponent(c.kind) : "#view=" + encodeURIComponent(c.view);
  }

  function counterOf(b: J, p: J): J {
    return p.counter === undefined || p.counter === null ? null : (b.counters || [])[p.counter];
  }

  async function onCapture(ev: KeyboardEvent) {
    if (ev.key !== "Enter" || ev.isComposing) return;
    ev.preventDefault();
    const title = String(drafts.capture || "").trim();
    if (!title) return;
    if ((await capture(title)) !== false) drafts.capture = "";
  }
</script>

<section id="home" class="home-screen" aria-label={t("Home")} {hidden}>
  {#if !hidden}
    {#if homeState.error}
      <p class="err">{homeState.error}</p>
    {:else if !home}
      <p class="loading">{t("Loading the home...")}</p>
    {:else}
      {@const b = home.briefing || {}}
      {@const c = home.capture || {}}
      <div class="home">
        {#if (b.sentences || []).length}
          <p class="briefing">
            {#each b.sentences as parts, i (i)}<span class="sentence"
                >{#each parts as p, j (j)}{@const counter = counterOf(b, p)}{#if counter}<a class="counter" href={counterHref(counter)} data-focus={"home:counter:" + counter.id}
                      >{counter.label}</a
                    >{:else}{p.text}{/if}{/each}{" "}</span
              >{/each}
          </p>
        {:else}
          <p class="briefing empty">{b.empty || ""}</p>
        {/if}
        <form class="capture" onsubmit={(ev) => ev.preventDefault()}>
          <input
            type="text"
            class="capture-input"
            aria-label={t("Capture a task")}
            placeholder={c.hint || t("Capture a task")}
            autocomplete="off"
            aria-keyshortcuts={c.key || undefined}
            data-focus="home:capture"
            bind:value={drafts.capture}
            onkeydown={onCapture} />{#if c.key}<kbd class="capture-key">{c.key}</kbd>{/if}
        </form>
        {#each home.sections || [] as s (s.id)}
          {@const rows = s.rows || []}
          <section class="home-section" data-section={s.id} aria-label={s.label}>
            <header class="home-head">
              <h2>{s.label}</h2>
              {#if s.total > rows.length}<a class="see-all" href={"#view=" + encodeURIComponent(s.view)} data-focus={"home:all:" + s.id}>{tn("See all {n}", s.total)}</a>{/if}
            </header>
            {#if rows.length}
              <div class="rows">
                {#each rows as e (e.id)}<ScreenRow task={e} view={s.view} />{/each}
              </div>
            {:else}
              <p class="empty">{t("Nothing here right now.")}</p>
            {/if}
          </section>
        {/each}
      </div>
    {/if}
  {/if}
</section>
