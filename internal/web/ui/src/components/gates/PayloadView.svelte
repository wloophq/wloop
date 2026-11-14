<script lang="ts">
  import type { J } from "../../lib/util";
  import Markdown from "../common/Markdown.svelte";
  import { fieldEdits, setFieldEdit } from "./gates";
  import PayloadView from "./PayloadView.svelte";

  // Every value a draft's payload posts, under its field's word, as the daemon serves them: a worded value keeps
  // its raw one in its title. While the draft is edited under key, each editable field with options is a select.
  let { draft, fields, key, editing = false, scope }: { draft: J; fields: J[]; key: string; editing?: boolean; scope: string } = $props();
  const edits = $derived(editing ? fieldEdits(draft, key) : {});
</script>

{#if fields.length}
  <dl class="d-payload">
    {#each fields as f (f.pointer)}
      <dt>{f.label}</dt>
      <dd data-pointer={f.pointer}>
        {#if (f.fields || []).length}
          <PayloadView {draft} fields={f.fields} {key} {editing} {scope} />
        {:else if editing && f.editable && (f.options || []).length}
          {@const current = f.pointer in edits ? edits[f.pointer] : f.value}
          <select class="d-field" aria-label={f.label} data-focus={`${scope}:field:${key}:${f.pointer}`} onchange={(ev) => setFieldEdit(key, draft, f.pointer, ev.currentTarget.value)}>
            {#each f.options as o (o.value)}<option value={o.value} selected={o.value === current}>{o.label}</option>{/each}
          </select>
        {:else if f.markdown}
          <div class="pv md" data-value={f.value}><Markdown text={f.value} key={`${scope}:field:${key}:${f.pointer}`} /></div>
        {:else}
          <span class="pv" data-value={f.value} title={f.shown && f.shown !== f.value ? f.value : undefined}>{f.shown || f.value}</span>
        {/if}
      </dd>
    {/each}
  </dl>
{/if}
