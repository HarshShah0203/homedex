<script lang="ts">
  import { reviewChange, reviewChanges, type Inventory } from '../api';
  import type { Change } from '../types';
  import PageHead from '../PageHead.svelte';

  let { inventory }: { inventory: Inventory } = $props();
  let reviewed = $state(new Set<number>());
  let pending = $state(new Set<number>());
  let error = $state('');
  let changes = $derived(inventory.changes);
  let unreviewed = $derived(changes.filter((change) => !Boolean(change.seen) && !reviewed.has(change.id)).length);
  let latest = $derived([...changes].sort((a, b) => b.scan_run_id - a.scan_run_id || b.id - a.id)[0]);
  let latestChanges = $derived(latest ? changes.filter((change) => change.scan_run_id === latest.scan_run_id) : []);
  let kindSummary = $derived(Object.entries(latestChanges.reduce<Record<string, number>>((counts, change) => ({ ...counts, [change.change_kind]: (counts[change.change_kind] ?? 0) + 1 }), {})).map(([kind, count]) => `${count} ${kind}`).join(' · ') || 'No changes recorded');

  function diffLabel(change: Change): string {
    if (typeof change.detail === 'string' && change.detail) return change.detail;
    let diff: unknown = change.diff;
    if (typeof diff === 'string') {
      const trimmed = diff.trim();
      if (!trimmed || trimmed === '{}') return change.summary;
      try {
        diff = JSON.parse(trimmed);
      } catch {
        return trimmed;
      }
    }
    if (!diff || typeof diff !== 'object') return change.summary;
    const record = diff as Record<string, unknown>;

    if (Array.isArray(record.before) || Array.isArray(record.after)) {
      const before = Array.isArray(record.before) ? record.before.length : 0;
      const after = Array.isArray(record.after) ? record.after.length : 0;
      return `${before} → ${after} declarations`;
    }

    const parts = Object.entries(record).map(([field, value]) => {
      if (value && typeof value === 'object' && 'after' in (value as Record<string, unknown>)) {
        const pair = value as Record<string, unknown>;
        return `${field} ${formatValue(pair.before)} → ${formatValue(pair.after)}`;
      }
      return `${field} ${formatValue(value)}`;
    });
    return parts.length ? parts.join(' · ') : change.summary;
  }

  function formatValue(value: unknown): string {
    if (value === null || value === undefined || value === '') return '∅';
    if (Array.isArray(value)) return `${value.length} items`;
    if (typeof value === 'object') return JSON.stringify(value);
    return String(value);
  }

  function timeLabel(created: string, index: number) {
    if (created.includes('10:42')) return '10:42';
    if (created.includes('6:00')) return '06:00';
    if (created.toLowerCase().includes('yesterday')) return 'YEST.';
    const date = new Date(created);
    return Number.isFinite(date.getTime()) ? date.toLocaleTimeString([], { hour: '2-digit', minute: '2-digit', hour12: false }) : String(index + 1).padStart(2, '0');
  }

  async function reviewOne(id: number) {
    pending = new Set(pending).add(id);
    error = '';
    try {
      await reviewChange(id, true);
      reviewed = new Set(reviewed).add(id);
    } catch (cause) {
      error = cause instanceof Error ? cause.message : 'The review state could not be saved.';
    } finally {
      const next = new Set(pending);
      next.delete(id);
      pending = next;
    }
  }

  async function reviewAll() {
    const ids = changes.filter((change) => !change.seen && !reviewed.has(change.id)).map((change) => change.id);
    if (!ids.length) return;
    pending = new Set(ids);
    error = '';
    try {
      await reviewChanges(ids, true);
      reviewed = new Set([...reviewed, ...ids]);
    } catch (cause) {
      error = cause instanceof Error ? cause.message : 'The review states could not be saved.';
    } finally {
      pending = new Set();
    }
  }
</script>

<main class="page">
  <PageHead kicker="CHANGES · FACTUAL DRIFT" title="What changed, without the alarm theatre." copy={`Each line records a before-and-after fact from one sealed scan. ${unreviewed} changes still need review.`}>
    {#snippet actions()}{#if !inventory.readOnly && unreviewed}<button class="quiet-button" disabled={pending.size > 0} onclick={reviewAll}>Mark visible reviewed</button>{/if}{/snippet}
  </PageHead>
  {#if latest}<div class="summary-line"><strong>Scan {latest.scan_run_id} · {timeLabel(latest.created_at, 0)}</strong><span>{unreviewed} unreviewed</span><span>{kindSummary}</span><span class={`status ${latest.scan_status === 'failed' ? 'bad' : 'ok'}`}>{latest.scan_status || 'Recorded'}</span></div>{/if}
  {#if error}<div class="source-notice" role="alert"><span class="status bad">Not saved</span><p>{error}</p></div>{/if}
  <div class="toolbar"><span class="toolbar-meta">{changes.length} VISIBLE · NEWEST FIRST</span></div>
  <section class="register" data-component-id="change-register">
    <header class="register-head change-cols"><span>Observed</span><span>Record</span><span>Factual difference</span><span>Kind</span><span>Review</span></header>
    {#if changes.length}
      {#each changes as change, index}
        <div class:unreviewed-row={!Boolean(change.seen) && !reviewed.has(change.id)} class="register-row change-cols"><div class="mono" data-label="Observed">{timeLabel(change.created_at, index)}</div><div data-label="Record"><strong>{change.summary}</strong><small>CHG-{String(change.id).padStart(3, '0')} · {Boolean(change.seen) || reviewed.has(change.id) ? 'reviewed' : 'unreviewed'}</small></div><div data-label="Factual difference"><code>{diffLabel(change)}</code></div><div data-label="Kind"><span class:ok={change.change_kind === 'added'} class:bad={change.change_kind === 'removed'} class:warn={change.change_kind === 'modified'} class="status">{change.change_kind === 'added' ? '+ service' : change.change_kind === 'removed' ? '− service' : change.entity_type === 'port' ? 'port map' : change.entity_type === 'cert' ? 'expiry' : 'image tag'}</span></div><div data-label="Review">{#if inventory.readOnly}<span class="mono">{change.seen ? 'REVIEWED' : 'READ ONLY'}</span>{:else}<button class="review-check" disabled={pending.has(change.id) || Boolean(change.seen) || reviewed.has(change.id)} aria-label={`Mark ${change.summary} reviewed`} aria-pressed={Boolean(change.seen) || reviewed.has(change.id)} onclick={() => reviewOne(change.id)}>{Boolean(change.seen) || reviewed.has(change.id) ? '✓' : ''}</button>{/if}</div></div>
      {/each}
    {:else}
      <div class="empty-register"><strong>NO FACTUAL CHANGES</strong><span>Before-and-after scan differences will appear here without alert scoring.</span></div>
    {/if}
  </section>
</main>
