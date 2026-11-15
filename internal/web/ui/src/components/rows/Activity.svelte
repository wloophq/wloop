<script lang="ts">
  import { eventData, eventLabel, eventTone, providerName } from "../../lib/catalog";
  import { fmtDate, t } from "../../lib/i18n.svelte";
  import type { Lens } from "../../lib/lens";
  import { keyOf, safeURL } from "../../lib/util";
  import { catalog } from "../../state/store.svelte";
  import { minute } from "../../state/clock.svelte";
  import { readHash, writeHash } from "../../state/route.svelte";
  import EmptyNote from "./EmptyNote.svelte";
  import { activityEmptyNote, activityView, actorLabel, eventRef, feed, requestedFrom, setLoopOnly } from "./feed.svelte";
  import { stamp } from "./model";

  // The recent activity: the newest events of every unit the loop follows, under a head per day, each opening its
  // unit; a toggle keeps the loop's own.
  let { lens, hidden = false }: { lens: Lens; hidden?: boolean } = $props();

  const now = $derived(minute());
  const entries = $derived(hidden ? [] : feed(lens, now));
  const route = $derived(readHash());

  function open(id: string, key: string) {
    activityView.selected = id;
    writeHash({ pr: key });
  }
</script>

<ol id="activity" class="activity" aria-label={t("Recent activity")} {hidden}>
  {#if !hidden}
    <li class="a-tools">
      <label
        ><input type="checkbox" checked={activityView.loopOnly} data-focus="activity:loop-only" onchange={(e) => setLoopOnly(e.currentTarget.checked)} />
        {t("Loop only")}</label
      >
    </li>
    {#each entries as en (en.id)}
      {#if en.kind === "day"}
        <li class="a-day">{en.label}</li>
      {:else}
        {@const e = en.it.event}
        {@const key = keyOf(e)}
        {@const data = eventData(e)}
        {@const requested = catalog.eventData.get(e.kind) === "requested" && e.data}
        <!-- svelte-ignore a11y_no_noninteractive_tabindex, a11y_no_noninteractive_element_interactions -->
        <li
          class={["a-ev", "ev-" + e.kind, eventTone(e.kind), key === route.pr && en.id === activityView.selected ? "sel" : ""].filter(Boolean).join(" ")}
          tabindex="0"
          data-focus={en.id}
          onclick={() => open(en.id, key)}
          onkeydown={(ev) => {
            if ((ev.key === "Enter" || ev.key === " ") && ev.target === ev.currentTarget) {
              ev.preventDefault();
              open(en.id, key);
            }
          }}>
          <time datetime={e.at} title={stamp(e.at)}>{fmtDate(e.at, "time")}</time>
          <div>
            <span class="kind">{eventLabel(e.kind)}</span>{#if requested}<span class="who">{" " + t("from {who}", { who: requestedFrom(e.data) })}</span>{/if}{#if e.actor}<span
                class="who">{" " + (requested ? t("by {who}", { who: actorLabel(e.actor) }) : actorLabel(e.actor))}</span
              >{/if}{" "}<span class="ref">{eventRef(en.it)}</span>{" "}<span class="title">{en.it.title}</span>{#if safeURL(en.it.url)}{" "}<a
                href={safeURL(en.it.url)}
                target="_blank"
                rel="noopener noreferrer"
                class="ext"
                onclick={(ev) => ev.stopPropagation()}>{providerName(e.unit.provider)}</a
              >{/if}
          </div>
          {#if data}<span class="data">{data}</span>{/if}
        </li>
      {/if}
    {/each}
    {#if !entries.some((x: { kind: string }) => x.kind === "event")}<li class="empty"><EmptyNote parts={activityEmptyNote(route.q)} /></li>{/if}
  {/if}
</ol>
