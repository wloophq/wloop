<script lang="ts">
  import { t } from "../../lib/i18n.svelte";
  import type { J } from "../../lib/util";
  import Markdown from "../common/Markdown.svelte";
  import DraftWhere from "./DraftWhere.svelte";
  import PayloadView from "./PayloadView.svelte";

  // The draft an answer approves, at the rev the question pinned in ref, as its own card shows it, with a note
  // when it changed since or is gone: the person sees what they approve, never only the agent's words for it.
  let { subject, ref, scope }: { subject: J; ref: J; scope: string } = $props();
  const d = $derived((subject.pinned || []).find((x: J) => x.id === ref.id));
</script>

{#if !d}
  <div class="d-pin"><p class="d-note err">{t("Approves draft #{id}, which is gone.", { id: ref.id })}</p></div>
{:else}
  <div class="d-pin" data-draft={String(ref.id)} data-rev={String(ref.rev)}>
    <p class="d-note">{t("Approves draft #{id} at rev {rev}:", { id: ref.id, rev: ref.rev })}</p>
    {#if d.rev !== ref.rev}<p class="d-note err">{t("It changed since the question pinned it and is now at rev {rev}: below is what it says now.", { rev: d.rev })}</p>{/if}
    {#if d.preview}<div class="d-preview"><Markdown text={d.preview} key={`${scope}:pin-preview`} names={d.names || {}} /></div>{/if}
    <DraftWhere draft={d} />
    {#if d.text}<Markdown text={d.text} fold={6} key={`${scope}:pin`} names={d.names || {}} />{/if}
    <PayloadView draft={d} fields={d.fields || []} key={`pin:${ref.id}`} {scope} />
  </div>
{/if}
