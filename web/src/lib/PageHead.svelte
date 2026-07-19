<script lang="ts">
  import type { Snippet } from 'svelte';

  let {
    kicker,
    title,
    meta,
    copy,
    actions
  }: { kicker?: string; title: string; meta?: string; copy?: string; actions?: Snippet } = $props();
</script>

<header class="page-head">
  <div>
    {#if kicker}<div class="kicker">{kicker}</div>{/if}
    <h1 class="page-title">{title}{#if meta}<span class="page-meta">{meta}</span>{/if}</h1>
    {#if copy}<p>{copy}</p>{/if}
  </div>
  {#if actions}<div class="head-actions">{@render actions()}</div>{/if}
</header>
