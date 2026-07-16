<script lang="ts">
  import type { Inventory } from '../api';
  import PageHead from '../PageHead.svelte';

  let { inventory }: { inventory: Inventory } = $props();
  let reviewed = $state(new Set<number>());
  let changes = $derived(inventory.changes);
  let unreviewed = $derived(changes.filter((change) => !Boolean(change.seen) && !reviewed.has(change.id)).length);

  function timeLabel(created: string, index: number) {
    if (created.includes('10:42')) return '10:42';
    if (created.includes('6:00')) return '06:00';
    if (created.toLowerCase().includes('yesterday')) return 'YEST.';
    const date = new Date(created);
    return Number.isFinite(date.getTime()) ? date.toLocaleTimeString([], { hour: '2-digit', minute: '2-digit', hour12: false }) : String(index + 1).padStart(2, '0');
  }

  function reviewAll() {
    reviewed = new Set(changes.map((change) => change.id));
  }
</script>

<main class="page">
  <PageHead kicker="CHANGES · FACTUAL DRIFT" title="What changed, without the alarm theatre." copy="Each line records a before-and-after fact from one sealed scan. Three changes still need review.">
    {#snippet actions()}<button class="quiet-button" onclick={reviewAll}>Mark visible reviewed</button><button class="primary-button">Compare scans</button>{/snippet}
  </PageHead>
  <div class="summary-line"><strong>Scan 042 · today at 10:42</strong><span>{unreviewed} unreviewed</span><span>1 added · 2 modified</span><span class="status ok">Complete</span></div>
  <div class="toolbar"><button class="filter-button"><b>Review</b><span>Any</span>⌄</button><button class="filter-button"><b>Kind</b><span>Any</span>⌄</button><button class="filter-button"><b>Object</b><span>Any</span>⌄</button><span class="spacer"></span><span class="toolbar-meta">{changes.length} VISIBLE · NEWEST FIRST</span></div>
  <section class="register" data-component-id="change-register">
    <header class="register-head change-cols"><span>Observed</span><span>Record</span><span>Factual difference</span><span>Kind</span><span>Review</span></header>
    {#if changes.length}
      {#each changes as change, index}
        <div class:unreviewed-row={!Boolean(change.seen) && !reviewed.has(change.id)} class="register-row change-cols"><div class="mono" data-label="Observed">{timeLabel(change.created_at, index)}</div><div data-label="Record"><strong>{change.summary}</strong><small>CHG-{String(change.id).padStart(3, '0')} · {Boolean(change.seen) || reviewed.has(change.id) ? 'reviewed' : 'unreviewed'}</small></div><div data-label="Factual difference"><code>{change.detail ?? change.diff}</code></div><div data-label="Kind"><span class:ok={change.change_kind === 'added'} class:bad={change.change_kind === 'removed'} class:warn={change.change_kind === 'modified'} class="status">{change.change_kind === 'added' ? '+ service' : change.change_kind === 'removed' ? '− service' : change.entity_type === 'port' ? 'port map' : change.entity_type === 'cert' ? 'expiry' : 'image tag'}</span></div><div data-label="Review"><button class="review-check" aria-label={`Mark ${change.summary} reviewed`} aria-pressed={Boolean(change.seen) || reviewed.has(change.id)} onclick={() => { reviewed = new Set(reviewed).add(change.id); }}>{Boolean(change.seen) || reviewed.has(change.id) ? '✓' : ''}</button></div></div>
      {/each}
    {:else}
      <div class="empty-register"><strong>NO FACTUAL CHANGES</strong><span>Before-and-after scan differences will appear here without alert scoring.</span></div>
    {/if}
  </section>
</main>
