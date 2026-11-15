<script lang="ts">
  import { onMount } from "svelte";
  import { t } from "../../lib/i18n.svelte";
  import { focusBack } from "./focus";
  import { omniboxTrap } from "./omnibox";
  import Shade from "./Shade.svelte";

  // A question asked in the page, as confirm would: true on Confirm or Enter, false on Cancel, Esc or a click
  // outside. Tab stays between its two buttons, so Enter and Esc keep reaching it.
  let { text, finish }: { text: string; finish: (v: boolean) => void } = $props();

  const restore = focusBack();
  let yes: HTMLButtonElement;
  let no: HTMLButtonElement;
  let done = false;

  function end(v: boolean) {
    if (done) return;
    done = true;
    restore();
    finish(v);
  }

  function key(e: KeyboardEvent) {
    const to = omniboxTrap(e, [no, yes], document.activeElement);
    if (to) to.focus();
    else if (e.key === "Escape") end(false);
    else if (e.key === "Enter" && document.activeElement !== no) end(true);
    else return;
    e.preventDefault();
    e.stopPropagation();
  }

  onMount(() => yes.focus());
</script>

<Shade dismiss={() => end(false)}>
  <!-- svelte-ignore a11y_interactive_supports_focus -->
  <div class="pal pal-confirm" role="alertdialog" aria-label={text} onkeydown={key}>
    <p class="pal-head">{text}</p>
    <div class="pal-acts">
      <button bind:this={no} type="button" class="act" onclick={() => end(false)}>{t("Cancel")}</button>
      <button bind:this={yes} type="button" class="act primary" onclick={() => end(true)}>{t("Confirm")}</button>
    </div>
  </div>
</Shade>
