<script lang="ts">
  import { t } from "../../lib/i18n.svelte";
  import type { J } from "../../lib/util";
  import { editing } from "../../state/store.svelte";
  import { startEditing, stopEditing } from "../../state/verbs";
  import Markdown from "../common/Markdown.svelte";
  import DraftWhere from "./DraftWhere.svelte";
  import { draftEditable, draftWord, pinKey } from "./gates";
  import PayloadView from "./PayloadView.svelte";

  // The draft an answer approves, as it stands now, with a note when it changed since the question pinned it or is
  // gone: the person sees what they approve, never only the agent's words for it, and edits it as its own card
  // does; picking the option posts it as they edited it.
  let { subject, ref, scope }: { subject: J; ref: J; scope: string } = $props();
  const d = $derived((subject.pinned || []).find((x: J) => x.id === ref.id));
  const key = $derived(pinKey(ref.id));
  const isEditing = $derived(!!d && editing.has(key));
</script>

{#if !d}
  <div class="d-pin"><p class="d-note err">{t("Picking this option posts draft #{id}, which is gone.", { id: ref.id })}</p></div>
{:else}
  <div class="d-pin" data-draft={String(ref.id)} data-rev={String(d.rev)}>
    <p class="d-note">{t("Picking this option posts draft #{id} at rev {rev}:", { id: ref.id, rev: d.rev })}</p>
    {#if d.rev !== ref.rev}<p class="d-note err">{t("It changed since the question pinned it and is now at rev {rev}; picking this option posts what it says now.", { rev: d.rev })}</p>{/if}
    {#if d.preview}<div class="d-preview"><Markdown text={d.preview} key={`${scope}:pin-preview`} names={d.names || {}} /></div>{/if}
    <DraftWhere draft={d} />
    {#if isEditing && !d.text_locked}
      <textarea class="draft-text" rows="6" aria-label={t("Draft text")} data-focus={`${scope}:draft-text:${key}`}
        bind:value={() => editing.get(key) ?? "", (v) => editing.set(key, v)}></textarea>
    {:else if d.text}
      <Markdown text={d.text} fold={6} key={`${scope}:pin`} names={d.names || {}} />
    {/if}
    <PayloadView draft={d} fields={d.fields || []} {key} editing={isEditing} {scope} />
    {#if isEditing}
      <p class="d-note">{t("Your edit posts when you pick this option and answer.")}</p>
      <div class="d-actions"><button type="button" class="act" data-focus={`${scope}:cancel:${key}`} onclick={() => stopEditing(key)}>{t("Cancel")}</button></div>
    {:else if draftEditable(d)}
      <div class="d-actions"><button type="button" class="act" data-focus={`${scope}:edit:${key}`} onclick={() => startEditing(key, d)}>{draftWord("draft.edit")}</button></div>
    {/if}
  </div>
{/if}
