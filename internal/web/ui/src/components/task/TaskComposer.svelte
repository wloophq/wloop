<script lang="ts">
import { handlerVerb, verbState, verbTask } from "../../state/verbs";
  import { t } from "../../lib/i18n.svelte";
  import type { J } from "../../lib/util";
  import { taskState } from "../../state/store.svelte";
  import ActionButton from "../common/ActionButton.svelte";
  import ActionResult from "../actions/ActionResult.svelte";
  import { replyBroadcasts, replyButtons, replyWith, sendChat } from "./task";

  // Writes to the task's agent: Send, the default that Ctrl+Enter runs, posts to its chat. Send reply and Save as
  // draft write the text as a reply, the one the reply verb's served send names first and marked default. The
  // text lives in taskState, so no update of the task touches the textarea.
  let { e }: { e: J } = $props();

  const verb = $derived(handlerVerb("reply"));
  const reply = $derived(verb ? verbState(e, verb) : null);
  const on = $derived(!!(reply && reply.enabled));
  const title = $derived(reply ? (on ? verbTask(verb).summary : reply.reason) || undefined : t("Not offered here"));

  function onkeydown(ev: KeyboardEvent) {
    if (ev.key !== "Enter" || !(ev.ctrlKey || ev.metaKey)) return;
    ev.preventDefault();
    sendChat(e);
  }
</script>

<section class={["compose", "d-compose", taskState.sending ? "resolving" : ""].filter(Boolean).join(" ")} aria-label={t("Message")} aria-busy={taskState.sending ? "true" : "false"}>
  <textarea rows="3" aria-label={t("Message")} placeholder={t("Write to the agent")} data-focus="task:compose:text" bind:value={taskState.text} {onkeydown}></textarea>
  {#if e.reply_broadcast}
    <label class="meta broadcast"><input type="checkbox" checked={replyBroadcasts(e)} data-focus="task:compose:broadcast" onchange={(ev) => (taskState.broadcast = (ev.currentTarget as HTMLInputElement).checked)} />{" "}{t("Also send to the conversation")}</label>
  {/if}
  <div class="d-actions">
    <ActionButton slot="task:compose:send" class="act primary" run={() => sendChat(e)}>{t("Send")}</ActionButton>
    {#each replyButtons() as b (b.focus)}
      <ActionButton slot={b.focus} class={"act" + (b.first ? " default" : "") + (on ? "" : " off")} disabled={!on} {title} run={() => replyWith(e, verb, b.send)}>{b.label}</ActionButton>
    {/each}
  </div>
  <ActionResult slots={["task:compose:send", ...replyButtons().map((b) => b.focus)]} />
</section>
