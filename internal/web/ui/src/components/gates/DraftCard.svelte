<script lang="ts">
  import { fmtDate, t } from "../../lib/i18n.svelte";
  import { safeURL, type J } from "../../lib/util";
  import { editing } from "../../state/store.svelte";
  import ActionResult from "../actions/ActionResult.svelte";
  import ActionButton from "../common/ActionButton.svelte";
  import Markdown from "../common/Markdown.svelte";
  import DraftWhere from "./DraftWhere.svelte";
  import {
    broadcastToggle, draftActions, draftEffects, draftHead, draftKey, draftResolving, editConflict, itemStateWord, taskHref, windowLine,
  } from "./gates";
  import PayloadView from "./PayloadView.svelte";

  // A draft on task: where it posts and in what language, its text as posted, why it was drafted, what sending it
  // does, its window, its actions and links to its task and its unit. On the draft's own task its actions are the
  // task's verbs, elsewhere its gate's intents. context, an Outbox draft's, adds what it answers; keyOf gives an
  // action's queue key its key.
  let {
    task,
    draft,
    scope = "detail",
    context = undefined,
    keyOf = undefined,
  }: { task: J; draft: J; scope?: string; context?: J; keyOf?: (action: string) => string } = $props();

  const key = $derived(draftKey(task, draft));
  const isEditing = $derived(editing.has(key));
  const conflict = $derived(isEditing && editConflict(draft, key));
  const effects = $derived(draftEffects(draft));
  const actions = $derived(draftActions(task, draft));
  const slots = $derived(actions.map((a) => `${scope}:${a.focus}`));
  const dim = $derived(draftResolving(draft));
  const pendingBroadcast = $derived(["held", "failed"].includes(draft.state));
  const would = $derived(windowLine(draft));
  const taskLink = $derived(draft.task && (!task || task.id !== draft.task));
  const unitLink = $derived(safeURL(draft.permalink || draft.link));
  const postLink = $derived(safeURL(draft.permalink));
  const posted = $derived(draft.updated_at ? t("Posted {time}", { time: fmtDate(draft.updated_at, "datetime") }) : t("Posted"));
  const asks = $derived((context && context.asks) || []);
  const items = $derived((context && context.items) || []);
</script>

<section class={"d-draft" + (dim ? " resolving" : "")} data-draft={String(draft.id)} aria-label={t("Draft")} aria-busy={dim ? "true" : undefined}>
  <div class="d-sub">{draftHead(draft)}</div>
  {#if draft.preview}<div class="d-preview"><Markdown text={draft.preview} key={`${scope}:preview:${draft.id}`} names={draft.names || {}} /></div>{/if}
  {#if asks.length || items.length}
    <div class="d-context">
      {#each asks as a, i (i)}
        <div class="ask">
          <div class="ask-head">{a.summary}{#if a.asker}<span class="who">{" · " + a.asker}</span>{/if}</div>
          {#each a.quotes || [] as q, j (j)}<blockquote class="quote">{q}</blockquote>{/each}
        </div>
      {/each}
      {#each items as it (it.id)}<div class="item"><span class={["st", "st-" + it.state]}>{itemStateWord(it.state)}</span> {it.summary || it.id}</div>{/each}
    </div>
  {/if}
  <DraftWhere {draft} />
  {#if draft.author}<div class="meta d-by">{draft.author}</div>{/if}
  {#if draft.broadcast}
    <label class="meta broadcast"><input type="checkbox" checked={draft.broadcast === "on"} disabled={!pendingBroadcast} data-focus={`${scope}:broadcast:draft:${draft.id}`}
        onchange={async (ev) => {
          const box = ev.currentTarget;
          if (!(await broadcastToggle(draft, box.checked))) box.checked = draft.broadcast === "on";
        }} /> {t("Also send to the conversation")}</label>
  {/if}
  {#if conflict}
    <div class="d-conflict">
      <p class="d-note err">{t("This draft changed since you began editing it; your edit replaces what it says now:")}{draft.author ? " " + draft.author : ""}</p>
      {#if draft.text}<Markdown text={draft.text} key={`conflict:${key}`} names={draft.names || {}} />{/if}
    </div>
  {/if}
  {#if isEditing && !draft.text_locked}
    <textarea class="draft-text" rows="6" aria-label={t("Draft text")} data-focus={`${scope}:draft-text:${key}`}
      bind:value={() => editing.get(key) ?? "", (v) => editing.set(key, v)}></textarea>
  {:else if draft.text}
    <Markdown text={draft.text} fold={6} key={`${scope}:draft:${draft.id}`} names={draft.names || {}} />
  {:else}
    <p class="draft-text">{draft.reaction ? `:${draft.reaction}:` : ""}</p>
  {/if}
  <PayloadView {draft} fields={draft.fields || []} {key} editing={isEditing} {scope} />
  {#each draft.notes || [] as n, i (i)}<p class={"d-note" + (draft.stale ? " err" : "")}>{n}</p>{/each}
  {#if draft.why}<p class="why">{t("Why it was proposed: {why}", { why: draft.why })}</p>{/if}
  {#if draft.quoted}<p class="why">{draft.quoted}</p>{/if}
  {#if effects.length}<ul class="effects">{#each effects as x, i (i)}<li>{x}</li>{/each}</ul>{/if}
  {#if draft.error}<p class="err">{draft.error}</p>{/if}
  {#if would}<p class="would">{would}</p>{/if}
  {#if draft.state === "sent"}
    <p class="d-posted">{#if postLink}<a href={postLink} target="_blank" rel="noopener noreferrer" class="ext">{posted}</a>{:else}{posted}{/if}</p>
  {/if}
  {#if actions.length}
    <div class="d-actions">
      {#each actions as a (a.focus)}
        {@const k = keyOf && a.key ? keyOf(a.key) : ""}
        <ActionButton slot={`${scope}:${a.focus}`} class={"act" + (a.enabled ? "" : " off") + (a.primary ? " primary" : "")} disabled={!a.enabled}
          title={a.title || undefined} keyshortcuts={k || undefined} run={a.run}>{#if k}<kbd>{k === "Enter" ? "↵" : k}</kbd>{" "}{/if}{a.label}</ActionButton>
      {/each}
    </div>
  {/if}
  <ActionResult {slots} />
  {#if taskLink || unitLink}
    <div class="meta d-links">{#if taskLink}<a href={taskHref(draft.task)} class="ref" data-focus={`${scope}:task:draft:${draft.id}`}>{t("Open its task")}</a>{/if}{#if taskLink && unitLink}{" · "}{/if}{#if unitLink}<a href={unitLink} target="_blank" rel="noopener noreferrer" class="ext">{draft.label || t("source")}</a>{/if}</div>
  {/if}
</section>
