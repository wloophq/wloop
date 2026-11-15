<script lang="ts">
  import { eventLabel } from "../../lib/catalog";
  import { fmtDate, t, tn } from "../../lib/i18n.svelte";
  import type { J } from "../../lib/util";
  import { clock } from "../../state/clock.svelte";
  import { taskState } from "../../state/store.svelte";
  import Markdown from "../common/Markdown.svelte";
  import { eventText, nodeKey, pickKinds, timelineStamp } from "./task";

  // The task's timeline as the daemon folds it into facts, newest first, narrowed to the kinds picked: each a
  // time, a dot in its tone, its words, the prose of its newest event that carries some, and its raw events
  // folded under a count.
  let { e }: { e: J } = $props();

  const kinds = $derived(taskState.kinds);
  const nodes = $derived(taskState.timeline || []);

  function lastSpoken(n: J): J {
    const spoken = (n.events || []).filter((ev: J) => eventText(ev));
    return spoken[spoken.length - 1] || null;
  }
</script>

<section class="d-timeline" aria-label={t("Timeline")}>
  {#if taskState.kindsSeen.length}
    <div class="chips kinds" role="group" aria-label={t("Kinds")}>
      <button type="button" class={"chip" + (kinds.length ? "" : " on")} aria-pressed={kinds.length ? "false" : "true"} data-focus="timeline:kind:" onclick={() => pickKinds(e, [])}>{t("all")}</button>
      {#each taskState.kindsSeen as kind (kind)}
        {@const on = kinds.includes(kind)}
        <button type="button" class={"chip" + (on ? " on" : "")} aria-pressed={on ? "true" : "false"} data-focus={"timeline:kind:" + kind} onclick={() => pickKinds(e, on ? kinds.filter((k) => k !== kind) : [...kinds, kind])}>{eventLabel(kind)}</button>
      {/each}
    </div>
  {/if}
  {#if taskState.timelineError}<p class="plan-error">{taskState.timelineError}</p>{/if}
  {#if !taskState.timeline && !taskState.timelineError}<p class="empty">{t("Loading the timeline.")}</p>{/if}
  {#if taskState.timeline && !nodes.length}<p class="empty">{t("Nothing here yet.")}</p>{/if}
  {#if nodes.length}
    <ol class="tl">
      {#each nodes as n (nodeKey(n))}
        {@const events = n.events || []}
        {@const last = lastSpoken(n)}
        <li class={["ev", n.tone, n.running ? "running" : ""].filter(Boolean).join(" ")} data-rule={n.rule || ""}>
          <time class="tm" datetime={n.at} title={fmtDate(n.at, "long")}>{timelineStamp(n.at, clock.now)}</time>
          <span class="ln" aria-hidden="true"><i></i></span>
          <div class="bd">
            <div class="lb">{n.label}</div>
            {#if last}<div class="dim"><Markdown text={eventText(last)} fold={3} key={"timeline:" + last.seq} /></div>{/if}
            {#if events.length > 1}
              <details>
                <summary>{tn("{n} event", events.length)}</summary>
                <ol class="raw">
                  {#each events as ev (ev.seq)}
                    {@const who = ev.by || ev.actor || ""}
                    <li data-seq={String(ev.seq)}><time class="tm" datetime={ev.at}>{fmtDate(ev.at, "time")}</time>{" "}<span class="what">{eventLabel(ev.kind)}{ev.sub ? " " + ev.sub : ""}</span>{#if who}<span class="who" title={ev.actor || ""}>{" " + who}</span>{/if}</li>
                  {/each}
                </ol>
              </details>
            {/if}
          </div>
        </li>
      {/each}
    </ol>
  {/if}
</section>
