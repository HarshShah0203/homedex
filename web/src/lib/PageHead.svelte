<script lang="ts">
  import type { Snippet } from 'svelte';

  let {
    kicker,
    title,
    copy,
    actions
  }: { kicker: string; title: string; copy: string; actions?: Snippet } = $props();
</script>

<header class="page-head">
  <div>
    <div class="kicker">{kicker}</div>
    <h1 class="page-title">{title}</h1>
    <p>{copy}</p>
  </div>
  {#if actions}<div class="head-actions">{@render actions()}</div>{/if}
</header>
