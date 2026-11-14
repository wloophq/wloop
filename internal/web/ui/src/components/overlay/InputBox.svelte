<script lang="ts">
  import { onMount } from "svelte";
  import { focusBack } from "./focus";
  import Shade from "./Shade.svelte";

  // The small input a key opens: the typed text on Enter, null on Esc, a click outside or leaving the field.
  let { label, placeholder = "", value = "", finish }: { label: string; placeholder?: string; value?: string; finish: (v: string | null) => void } = $props();

  const restore = focusBack();
  let input: HTMLInputElement;
  let done = false;

  function end(v: string | null) {
    if (done) return;
    done = true;
    restore();
    finish(v);
  }

  function key(e: KeyboardEvent) {
    if (e.key === "Escape") end(null);
    else if (e.key === "Enter" && !e.isComposing) end(input.value);
    else return;
    e.preventDefault();
  }

  onMount(() => {
    input.value = value;
    input.focus();
  });
</script>

<Shade dismiss={() => end(null)}>
  <div class="pal" role="dialog" aria-label={label}>
    <div class="pal-head">{label}</div>
    <input bind:this={input} type="text" aria-label={label} {placeholder} autocomplete="off" onkeydown={key} onblur={() => end(null)} />
  </div>
</Shade>
