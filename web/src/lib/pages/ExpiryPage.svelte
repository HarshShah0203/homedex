<script lang="ts">
  import type { Inventory } from '../api';
  import PageHead from '../PageHead.svelte';

  let { inventory }: { inventory: Inventory } = $props();
  let rows = $derived([...inventory.expiries].sort((a, b) => (a.days ?? Number.MAX_SAFE_INTEGER) - (b.days ?? Number.MAX_SAFE_INTEGER)));
  let urgent = $derived(rows.filter((record) => record.days !== null && record.days <= 30));

  function statusClass(days: number | null) { return days !== null && days <= 30 ? 'warn' : 'ok'; }
</script>

<main class="page">
  <PageHead kicker="EXPIRY · FORWARD REGISTER" title="What needs a date, gets a date." copy="Certificates, domains, and manual obligations share one chronological register.">
    {#snippet actions()}<button class="quiet-button">Add manual expiry</button><button class="primary-button">Export calendar</button>{/snippet}
  </PageHead>
  {#if rows.length}
    <div class="summary-line"><strong>{urgent.length} records inside 30 days</strong><span>Next: {rows[0].name} · {rows[0].days ?? 'unknown'} days</span><span>Last checked 6 hours ago</span></div>
    <div class="timeline" data-component-id="expiry-horizon" aria-label="Ninety day expiry horizon">{#each rows.filter((record) => record.days !== null && record.days <= 90) as record}<i style={`left:${Math.min(99, Math.max(0, Number(record.days) / 90 * 100))}%`}></i>{/each}</div>
  {/if}
  <div class="toolbar"><button class="filter-button"><b>Type</b><span>All</span>⌄</button><button class="filter-button"><b>Window</b><span>180 days</span>⌄</button><button class="filter-button"><b>State</b><span>Current</span>⌄</button><span class="spacer"></span><span class="toolbar-meta">{rows.length} VISIBLE · SORTED SOONEST</span></div>
  <section class="register" data-component-id="expiry-register">
    <header class="register-head expiry-cols"><span>Record</span><span>Type</span><span>Authority</span><span>Expires</span><span>Window</span></header>
    {#if rows.length}
      {#each rows as record}
        <div class="register-row expiry-cols"><div data-label="Record"><strong>{record.name}</strong><small>EXP-{String(record.id).padStart(3, '0')}</small></div><div data-label="Type">{record.type}</div><div data-label="Authority"><strong>{record.authority}</strong><small>checked {record.checked}</small></div><div class="mono" data-label="Expires">{record.expires}</div><div data-label="Window"><span class={`status ${statusClass(record.days)}`}>{record.days === null ? 'Unknown' : `${record.days} days`}</span></div></div>
      {/each}
    {:else}
      <div class="empty-register"><strong>NO EXPIRY RECORDS</strong><span>Certificate, domain, and manual dates will share this chronological register.</span></div>
    {/if}
  </section>
</main>
