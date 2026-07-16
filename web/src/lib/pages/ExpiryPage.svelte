<script lang="ts">
  import type { Inventory } from '../api';
  import PageHead from '../PageHead.svelte';

  let { inventory }: { inventory: Inventory } = $props();
  let rows = $derived([...inventory.expiries].sort((a, b) => (a.days_remaining ?? Number.MAX_SAFE_INTEGER) - (b.days_remaining ?? Number.MAX_SAFE_INTEGER)));
  let urgent = $derived(rows.filter((record) => ['expired', 'action_needed', 'expiring'].includes(record.status)));
  let lastChecked = $derived(rows.find((record) => record.checked_at)?.checked_at ?? 'unknown');

  function statusClass(status: string) { return ['expired', 'action_needed', 'expiring'].includes(status) ? 'warn' : 'ok'; }
</script>

<main class="page">
  <PageHead kicker="EXPIRY · FORWARD REGISTER" title="What needs a date, gets a date." copy="Certificates, domains, and manual obligations share one chronological register." />
  {#if rows.length}
    <div class="summary-line"><strong>{urgent.length} records inside 30 days</strong><span>Next: {rows[0].name} · {rows[0].days_remaining ?? 'unknown'} days</span><span>Last checked {lastChecked}</span></div>
    <div class="timeline" data-component-id="expiry-horizon" aria-label="Ninety day expiry horizon">{#each rows.filter((record) => record.days_remaining !== null && record.days_remaining <= 90) as record}<i style={`left:${Math.min(99, Math.max(0, Number(record.days_remaining) / 90 * 100))}%`}></i>{/each}</div>
  {/if}
  <div class="toolbar"><span class="toolbar-meta">{rows.length} VISIBLE · SORTED SOONEST</span></div>
  <section class="register" data-component-id="expiry-register">
    <header class="register-head expiry-cols"><span>Record</span><span>Type</span><span>Authority</span><span>Expires</span><span>Window</span></header>
    {#if rows.length}
      {#each rows as record}
        <div class="register-row expiry-cols"><div data-label="Record"><strong>{record.name}</strong><small>EXP-{String(record.id).padStart(3, '0')}</small></div><div data-label="Type">{record.kind}</div><div data-label="Authority"><strong>{record.authority}</strong><small>checked {record.checked_at}</small></div><div class="mono" data-label="Expires">{record.expires_at ?? 'Unknown'}</div><div data-label="Window"><span class={`status ${statusClass(record.status)}`}>{record.days_remaining === null ? 'Unknown' : `${record.days_remaining} days`}</span></div></div>
      {/each}
    {:else}
      <div class="empty-register"><strong>NO EXPIRY RECORDS</strong><span>Certificate, domain, and manual dates will share this chronological register.</span></div>
    {/if}
  </section>
</main>
