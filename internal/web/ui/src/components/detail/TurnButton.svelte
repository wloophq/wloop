<script lang="ts">
  import { actionTitle } from "../../lib/actions";
  import { t } from "../../lib/i18n.svelte";
  import { keyOf, type J } from "../../lib/util";
  import { act, answerSent } from "../../state/actions.svelte";
  import { busy, state } from "../../state/store.svelte";
  import ActionButton from "../common/ActionButton.svelte";
  import { answerNote } from "./unit";

  // A button of the question card that runs the turn action name, when the row serves it; an answer takes the
  // text typed in the card's box along.
  let {
    row,
    slot,
    label,
    name,
    extra = {},
    primary = false,
    rec = false,
    title = undefined,
    disabled = false,
  }: { row: J; slot: string; label: string; name: string; extra?: J; primary?: boolean; rec?: boolean; title?: string; disabled?: boolean } = $props();

  const st = $derived((row.actions || []).find((a: J) => a.id === name));
</script>

{#if st}
  <ActionButton
    slot={`detail:${slot}`}
    class={["act", st.enabled && primary ? "primary" : "", st.enabled ? "" : "off"].filter(Boolean).join(" ")}
    title={st.enabled && !st.note && title ? title : actionTitle(st, state.specs.byId[name])}
    disabled={busy.has(keyOf(row)) || answerSent(row) || !st.enabled || disabled}
    run={() => act(row, name, name === "answer" && answerNote(row) ? { ...extra, guidance: answerNote(row) } : extra)}
    >{st.label || label}{#if rec}<span class="rec">{" " + t("recommended")}</span>{/if}</ActionButton>
{/if}
