<script lang="ts">
import { stateLabel, taskAct, verbLabel } from "../../state/verbs";
  import { t } from "../../lib/i18n.svelte";
  import type { J } from "../../lib/util";
  import { pickTask, PICK_ARG, pickVerb, relatedVerbs, taskById, viewHref } from "./task";
  import VerbButton from "./VerbButton.svelte";

  // The tasks related to the task, each with the verb that takes the link off; its children; its merged-from
  // trace, each task merged into it with the verb that gives it back and, under it without one, the tasks merged
  // into that one before; then the verbs that take another task in.
  let { e }: { e: J } = $props();

  const related = $derived(e.related || []);
  const children = $derived(e.children || []);
  const merged = $derived(e.merged_from || []);
  const unrelate = $derived(pickVerb(e, "related"));
  const split = $derived(pickVerb(e, "merged"));
  const takers = $derived(relatedVerbs(e));
</script>

{#snippet refRow(r: J, verb: string, scope: string)}
  {@const own = !r.merged ? taskById(r.id) : null}
  {@const title = (own && own.title) || r.title || r.id}
  {@const st = own ? own.state : r.state}
  <a href={viewHref(r.id)} data-focus={`${scope}:${r.id}`}>{title}</a>{#if st}{" "}<span class="pill">{stateLabel(st)}</span>{/if}{#if verb}{" "}<VerbButton {e} {verb} scope={`${scope}:${r.id}`} name={`${verbLabel(verb)} ${title}`} run={() => taskAct(e, verb, { [PICK_ARG]: r.id })}>{verbLabel(verb)}</VerbButton>{/if}
{/snippet}

{#snippet within(r: J)}
  {#if (r.within || []).length}
    <ul class="d-tasks">{#each r.within as w (w.id)}<li>{@render refRow(w, "", "merged")}{@render within(w)}</li>{/each}</ul>
  {/if}
{/snippet}

{#if related.length}
  <section class="d-related" aria-label={t("Related")}>
    <h2 class="d-sub">{t("Related")}</h2>
    <ul class="d-tasks">{#each related as r (r.id)}<li>{@render refRow(r, unrelate, "related")}</li>{/each}</ul>
  </section>
{/if}
{#if children.length}
  <section class="d-children" aria-label={t("Children")}>
    <h2 class="d-sub">{t("Children")}</h2>
    <ol class="d-tasks">
      {#each children as id (id)}
        {@const c = taskById(id)}
        <li><a href={viewHref(id)} data-focus={"child:" + id}>{(c && c.title) || id}</a>{#if c}{" "}<span class="pill">{stateLabel(c.state)}</span>{/if}</li>
      {/each}
    </ol>
  </section>
{/if}
{#if merged.length}
  <section class="d-merged" aria-label={t("Merged from")}>
    <h2 class="d-sub">{t("Merged from")}</h2>
    <ul class="d-tasks">{#each merged as r (r.id)}<li>{@render refRow(r, split, "merged")}{@render within(r)}</li>{/each}</ul>
  </section>
{/if}
{#if takers.length}
  <div class="d-actions">{#each takers as v (v)}<VerbButton {e} verb={v} scope="related" run={() => pickTask(e, v)}>{verbLabel(v)}</VerbButton>{/each}</div>
{/if}
