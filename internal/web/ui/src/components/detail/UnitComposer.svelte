<script lang="ts">
  import { providerReacts } from "../../lib/catalog";
  import { t } from "../../lib/i18n.svelte";
  import { unitLabel } from "../../lib/tasks";
  import type { J } from "../../lib/util";
  import { scheduler, setNotice } from "../../state/actions.svelte";
  import { postJSON } from "../../state/api";
  import { detailState } from "../../state/store.svelte";
  import ActionButton from "../common/ActionButton.svelte";
  import ActionResult from "../actions/ActionResult.svelte";
  import { replyArgs, replySendsNow } from "./unit";

  // A reply in the unit's conversation, or a reaction where the unit takes one, posted as a draft: Save to Outbox
  // holds it for approval, Send now approves it at once. Its text lives in detailState.compose.
  let { u }: { u: J } = $props();

  const key = $derived("unit:" + u.provider + ":" + u.key);
  const mine = $derived(detailState.compose.key === key);

  function write(field: "text" | "reaction", v: string) {
    if (detailState.compose.key !== key) detailState.compose = { key, text: "", reaction: "" };
    detailState.compose[field] = v;
  }

  async function run(send: boolean) {
    const args = replyArgs(mine ? detailState.compose : { text: "", reaction: "" }, send);
    if (!args) {
      setNotice(t("Write a reply or name a reaction first"), true);
      return;
    }
    try {
      const d = await postJSON("/api/drafts", { unit: u, kind: args.kind, text: args.text, reaction: args.reaction, send: args.send === "now", source: "cockpit" });
      setNotice(d.state === "queued" ? t("Draft {id} approved; it goes out in a moment", { id: d.id }) : t("Draft {id} saved to the Outbox for your approval", { id: d.id }));
      scheduler.load({ tasks: true });
      detailState.compose = { key, text: "", reaction: "" };
    } catch (err) {
      setNotice(t("Reply on {unit} failed: {error}", { unit: unitLabel(u), error: (err as Error).message }), true);
    }
  }

  const order = $derived(replySendsNow() ? [true, false] : [false, true]);
</script>

<section class="d-compose" aria-label={t("Reply")}>
  <h2 class="d-sub">{t("Reply")}</h2>
  <textarea
    rows="3"
    aria-label={t("Reply")}
    placeholder={t("Your reply")}
    data-focus="unit:reply:text"
    bind:value={() => (mine ? detailState.compose.text : ""), (v) => write("text", v)}></textarea>
  {#if providerReacts(u.provider)}
    <input
      type="text"
      class="d-reaction"
      aria-label={t("Reaction")}
      placeholder={t("or a reaction, such as eyes")}
      data-focus="unit:reply:reaction"
      bind:value={() => (mine ? detailState.compose.reaction : ""), (v) => write("reaction", v)} />
  {/if}
  <div class="d-actions">
    {#each order as send, i (send)}
      <ActionButton slot={`unit:reply:${send ? "send" : "save"}`} class={"act" + (i === 0 ? " primary" : "")} run={() => run(send)}
        >{send ? t("Send now") : t("Save to Outbox")}</ActionButton>
    {/each}
  </div>
  <ActionResult slots={["unit:reply:send", "unit:reply:save"]} />
</section>
