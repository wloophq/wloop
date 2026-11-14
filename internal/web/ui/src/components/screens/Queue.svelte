<script lang="ts">
  import { fmtNumber, t, tn } from "../../lib/i18n.svelte";
  import { HOME } from "../../lib/routes";
  import { queueKey, queueShown } from "../../state/loaders.svelte";
  import { queueState } from "../../state/store.svelte";
  import DraftCard from "../gates/DraftCard.svelte";
  import GateCard from "../gates/GateCard.svelte";
  import ScreenRow from "./ScreenRow.svelte";
  import { queueAct, queueDeps, queueDraft, queueDraftAction, queueDraftOf, queueKeyAction, queueRoute } from "./queue";

  // A decision queue: one decision at a time, the oldest first, with its card or its unit's row, its primary
  // action, the progress through the queue and the decision next up.
  let { hidden = false }: { hidden?: boolean } = $props();

  const route = $derived(queueRoute());
  const queue = $derived(queueShown(route));
  const why = $derived(queueState.key === queueKey(route) ? [queueState.loadError, queueState.error].filter(Boolean) : []);
  const items = $derived((queue && queue.items) || []);
  const i = $derived(Math.min(Math.max(queueState.at, 0), Math.max(items.length - 1, 0)));
  const item = $derived(items[i]);
  const next = $derived(items[i + 1]);
  const more = $derived(items.length - i - 2);
  const primary = $derived(item && item.primary && item.primary.kind && !queueDraftAction(item, "queue:primary") ? item.primary : null);
  const keyOf = (a: string) => queueDeps.keyOf(a);
</script>

{#snippet step(action: string, label: string, off: boolean)}
  {@const key = keyOf(action)}
  <button type="button" class="act queue-step" data-focus={action} aria-keyshortcuts={key || undefined} disabled={off || undefined} onclick={() => queueKeyAction(action)}
    >{label}{#if key}{" "}<kbd>{key}</kbd>{/if}</button
  >
{/snippet}

{#snippet err()}
  {#if why.length}<p class="err" role="alert">{why.join(" ")}</p>{/if}
{/snippet}

<section id="queue" class="queue-screen" aria-label={t("Decision queue")} {hidden}>
  {#if !hidden}
    {#if !queue}
      <div class="queue">
        <nav class="crumbs"><a class="crumb" href={"#view=" + HOME} data-focus="queue:home">{t("Home")}</a></nav>
        {#if why.length}{@render err()}{:else}<p class="loading">{t("Loading the queue...")}</p>{/if}
      </div>
    {:else}
      <div class={["queue", !items.length && "empty"]} data-item={items.length ? String(i) : undefined}>
        <nav class="crumbs" aria-label={t("Where you are")}
          ><a class="crumb" href={"#view=" + HOME} data-focus="queue:home">{t("Home")}</a><span class="sep" aria-hidden="true">/</span><span>{queue.title}</span></nav
        >
        {#if !items.length}
          {@render err()}
          <p class="empty">{queue.empty || t("Nothing left in this queue.")}</p>
          <a class="back-home" href={"#view=" + HOME}>{t("Back to the home")}</a>
        {:else}
          <div class="queue-nav">
            <p class="progress" aria-live="polite">{t("{n} of {total}", { n: fmtNumber(i + 1), total: fmtNumber(items.length) })}</p>
            {@render step("queue:back", t("Previous"), i === 0)}{@render step("queue:skip", t("Next"), i === items.length - 1)}
          </div>
          {@render err()}
          <div class="queue-item">
              {#if queueDraft(item.gate)}
                <ScreenRow task={item.task} view={queue.view} /><DraftCard task={item.task} draft={queueDraftOf(item.gate)} scope="queue" {keyOf} />
              {:else if item.gate}
                <ScreenRow task={item.task} view={queue.view} /><GateCard task={item.task} gate={item.gate} scope="queue" />
              {:else}
                <div class="queue-unit">
                  <ScreenRow task={item.task} view={queue.view} />
                  {#if item.situation && item.situation.label}<p class="situation">{item.situation.label}</p>{/if}
                </div>
              {/if}
            {#if primary}<button
                type="button"
                class="act primary queue-primary"
                data-focus="queue:primary"
                aria-keyshortcuts="Enter"
                disabled={queueState.busy || undefined}
                onclick={() => queueAct(item)}><kbd>↵</kbd> {primary.label || primary.id}</button
              >{/if}
          </div>
          {#if next && next.task.id !== item.task.id}
            <section class="next-up" aria-label={t("Next up")}>
              <h3>{t("Next up")}</h3>
              <ScreenRow task={next.task} view={queue.view} />
              {#if more > 0}<p class="more">{tn("+{n} more", more)}</p>{/if}
            </section>
          {/if}
        {/if}
      </div>
    {/if}
  {/if}
</section>
