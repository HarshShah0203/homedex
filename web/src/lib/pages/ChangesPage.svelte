<script lang="ts">
  import { reviewChange, reviewChanges, type Inventory } from '../api';
  import type { Change } from '../types';
  import PageHead from '../PageHead.svelte';
  import { formatDate, relativeTime } from '../time';

  type ChangeDetail = {
    label: string;
    before: string;
    after: string;
    category: string;
  };

  let { inventory }: { inventory: Inventory } = $props();
  let reviewed = $state(new Set<number>());
  let pending = $state(new Set<number>());
  let expandedChangeId = $state<number | null>(null);
  let error = $state('');
  let changes = $derived(inventory.changes);
  let unreviewed = $derived(changes.filter((change) => !Boolean(change.seen) && !reviewed.has(change.id)).length);
  let latest = $derived([...changes].sort((a, b) => b.scan_run_id - a.scan_run_id || b.id - a.id)[0]);
  let latestChanges = $derived(latest ? changes.filter((change) => change.scan_run_id === latest.scan_run_id) : []);
  let kindSummary = $derived(Object.entries(latestChanges.reduce<Record<string, number>>((counts, change) => ({ ...counts, [change.change_kind]: (counts[change.change_kind] ?? 0) + 1 }), {})).map(([kind, count]) => `${count} ${kind}`).join(' · ') || 'No changes recorded');

  function parsedDiff(change: Change): Record<string, unknown> {
    let diff: unknown = change.diff;

    if (typeof diff === 'string') {
      try {
        diff = JSON.parse(diff);
      } catch {
        return {};
      }
    }

    if (!diff || typeof diff !== 'object' || Array.isArray(diff)) return {};
    return Object.fromEntries(
      Object.entries(diff as Record<string, unknown>).filter(([field]) => field !== 'natural_key')
    );
  }

  function formatValue(value: unknown): string {
    if (value === null || value === undefined || value === '') return '—';
    if (Array.isArray(value)) {
      return value.length ? value.map((item) => formatValue(item)).join(', ') : 'none';
    }
    if (typeof value === 'object') return JSON.stringify(value, null, 2);
    const text = String(value);
    return /^\d{4}-\d{2}-\d{2}T[\d:.]+Z?$/.test(text) ? formatDate(text) : text;
  }

  function formatMountList(value: unknown): string {
    if (!Array.isArray(value) || value.length === 0) return 'No mount points';

    return value.map((item) => {
      if (!item || typeof item !== 'object') return String(item);

      const mount = item as Record<string, unknown>;
      const source = String(mount.source ?? '—');
      const destination = String(mount.destination ?? '—');
      const details = [
        mount.type,
        mount.mode,
        mount.rw === true ? 'Read/write' : mount.rw === false ? 'Read-only' : null,
        mount.propagation
      ].filter(Boolean).join(' · ');

      return `${source} → ${destination}${details ? `\n${details}` : ''}`;
    }).join('\n\n');
  }

  function fieldLabel(field: string): string {
    const labels: Record<string, string> = {
      mounts: 'Mount Point',
      image: 'Image',
      image_id: 'Image ID',
      digest: 'Image Digest',
      sha256: 'SHA256',
      tag: 'Image Tag',
      state: 'State',
      status: 'Status',
      name: 'Name',
      version: 'Version',
      address: 'Address',
      port: 'Port',
      fingerprint: 'Fingerprint',
      expires_at: 'Expiration',
      issuer: 'Issuer'
    };

    return labels[field] ?? field.replaceAll('_', ' ').replace(/\b\w/g, (letter) => letter.toUpperCase());
  }

  function fieldCategory(field: string, entity: string): string {
    if (field === 'mounts') return 'Mount';
    if (/image|digest|sha256|tag/i.test(field)) return 'Image';
    if (/cert|fingerprint|issuer|expires/i.test(field)) return 'Cert';
    if (/domain/i.test(field)) return 'Domain';
    if (/host/i.test(field)) return 'Host';
    if (/port/i.test(field)) return 'Port';
    if (/route/i.test(field)) return 'Route';
    return entity.charAt(0).toUpperCase() + entity.slice(1);
  }

  function entityLabel(change: Change): string {
    const labels: Record<string, string> = {
      cert: 'Certificate',
      service: 'Service',
      image: 'Image',
      host: 'Host',
      port: 'Port',
      route: 'Route',
      domain: 'Domain'
    };
    return labels[change.entity_type] ?? change.entity_type ?? 'Record';
  }

  function detailRows(change: Change): ChangeDetail[] {
    const diff = parsedDiff(change);
    const rows: ChangeDetail[] = [];
    const entity = change.entity_type || 'record';

    for (const [field, value] of Object.entries(diff)) {
      if (value && typeof value === 'object' && !Array.isArray(value) &&
          ('before' in (value as Record<string, unknown>) || 'after' in (value as Record<string, unknown>))) {
        const pair = value as Record<string, unknown>;
        const before = pair.before;
        const after = pair.after;

        if (field === 'mounts') {
          const beforeCount = Array.isArray(before) ? before.length : 0;
          const afterCount = Array.isArray(after) ? after.length : 0;
          const label = beforeCount === 0 && afterCount > 0
            ? 'Mount Point Added'
            : beforeCount > 0 && afterCount === 0
              ? 'Mount Point Removed'
              : 'Mount Points Changed';

          rows.push({
            label,
            before: formatMountList(before),
            after: formatMountList(after),
            category: 'Mount'
          });
        } else {
          rows.push({
            label: `${fieldLabel(field)} Changed`,
            before: formatValue(before),
            after: formatValue(after),
            category: fieldCategory(field, entity)
          });
        }
      } else {
        rows.push({
          label: fieldLabel(field),
          before: '—',
          after: formatValue(value),
          category: fieldCategory(field, entity)
        });
      }
    }

    if (change.change_kind === 'added' || change.change_kind === 'removed') {
      const entityChange = `${entityLabel(change)} ${change.change_kind === 'added' ? 'Added' : 'Removed'}`;
      if (!rows.some((row) => row.label === entityChange)) {
        rows.unshift({
          label: entityChange,
          before: change.change_kind === 'added' ? 'Not present' : 'Present',
          after: change.change_kind === 'added' ? 'Discovered' : 'Removed',
          category: entityLabel(change)
        });
      }
    }

    if (!rows.length) {
      rows.push({
        label: change.detail || change.summary,
        before: '—',
        after: change.summary,
        category: entityLabel(change)
      });
    }

    return rows;
  }

  function changeLabel(change: Change): string {
    return detailRows(change).map((row) => row.label).join(' · ');
  }

  function kindLabel(change: Change): string {
    const categories = [...new Set(detailRows(change).map((row) => row.category))];
    return categories.length > 1 ? 'Multi' : categories[0] ?? entityLabel(change);
  }

  function kindClass(change: Change): string {
    if (change.change_kind === 'added') return 'ok';
    if (change.change_kind === 'removed') return 'bad';
    return 'warn';
  }

  function toggleChange(id: number) {
    expandedChangeId = expandedChangeId === id ? null : id;
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
  <PageHead title="Changes" meta={`${unreviewed} unreviewed`}>
    {#snippet actions()}
      {#if !inventory.readOnly && unreviewed}
        <button class="quiet-button" disabled={pending.size > 0} onclick={reviewAll}>Mark visible reviewed</button>
      {/if}
    {/snippet}
  </PageHead>

  {#if latest}
    <div class="summary-line">
      <strong>Scan {latest.scan_run_id} · {relativeTime(latest.created_at)}</strong>
      <span>{unreviewed} unreviewed</span>
      <span>{kindSummary}</span>
      <span class={`status ${latest.scan_status === 'failed' ? 'bad' : 'ok'}`}>{latest.scan_status || 'Recorded'}</span>
    </div>
  {/if}

  {#if error}
    <div class="source-notice" role="alert">
      <span class="status bad">Not saved</span>
      <p>{error}</p>
    </div>
  {/if}

  <div class="toolbar">
    <span class="toolbar-meta">{changes.length} VISIBLE · NEWEST FIRST</span>
  </div>

  <section class="register" data-component-id="change-register">
    <header class="register-head change-cols">
      <span>Observed</span>
      <span>Record</span>
      <span>Change</span>
      <span>Kind</span>
      <span class="num">Review</span>
    </header>

    {#if changes.length}
      {#each changes as change}
        <article class:unreviewed-row={!Boolean(change.seen) && !reviewed.has(change.id)} class="change-record">
          <div class="register-row change-cols">
            <div class="mono" data-label="Observed">{relativeTime(change.created_at)}</div>

            <div data-label="Record" class="change-record-cell">
              <button
                class="change-expand"
                aria-expanded={expandedChangeId === change.id}
                onclick={() => toggleChange(change.id)}
              >
                <span class="expand-mark">{expandedChangeId === change.id ? '▾' : '▸'}</span>
                <strong>{change.summary}</strong>
              </button>
              <small>CHG-{String(change.id).padStart(3, '0')} · {Boolean(change.seen) || reviewed.has(change.id) ? 'reviewed' : 'unreviewed'}</small>
            </div>

            <div data-label="Change" class="change-label">{changeLabel(change)}</div>

            <div data-label="Kind">
              <span class={`status ${kindClass(change)}`}>{kindLabel(change)}</span>
            </div>

            <div data-label="Review">
              {#if inventory.readOnly}
                <span class="mono">{change.seen ? 'REVIEWED' : 'READ ONLY'}</span>
              {:else}
                <button
                  class="review-check"
                  disabled={pending.has(change.id) || Boolean(change.seen) || reviewed.has(change.id)}
                  aria-label={`Mark ${change.summary} reviewed`}
                  aria-pressed={Boolean(change.seen) || reviewed.has(change.id)}
                  onclick={() => reviewOne(change.id)}
                >{Boolean(change.seen) || reviewed.has(change.id) ? '✓' : ''}</button>
              {/if}
            </div>
          </div>

          {#if expandedChangeId === change.id}
            <section class="change-details" aria-label={`Details for ${change.summary}`}>
              <div class="change-detail-heading">CHANGE DETAILS</div>
              {#each detailRows(change) as detail}
                <div class="change-detail-entry">
                  <strong>{detail.label}</strong>
                  <div class="change-detail-values">
                    <div>
                      <span>Before</span>
                      <code>{detail.before}</code>
                    </div>
                    <div>
                      <span>After</span>
                      <code>{detail.after}</code>
                    </div>
                  </div>
                </div>
              {/each}
            </section>
          {/if}
        </article>
      {/each}
    {:else}
      <div class="empty-register">
        <strong>NO FACTUAL CHANGES</strong>
        <span>Before-and-after scan differences will appear here without alert scoring.</span>
      </div>
    {/if}
  </section>
</main>
