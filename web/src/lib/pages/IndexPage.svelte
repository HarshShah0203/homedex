<script lang="ts">
  import type { Inventory } from '../api';
  import PageHead from '../PageHead.svelte';
  import { navigate } from '../router';
  import { relativeTime } from '../time';

  let { inventory }: { inventory: Inventory } = $props();
  let query = $state('');
  let visible = $derived(inventory.services.filter((service) => `${service.name} ${service.stack} ${service.image} ${service.host} ${service.route}`.toLowerCase().includes(query.toLowerCase())));
  let recordTotal = $derived(inventory.services.length);
  let unresolvedRoutes = $derived(inventory.routes.filter((route) => !['ok', 'resolved'].includes(route.status.toLowerCase())));
  let firstUnresolved = $derived(unresolvedRoutes[0]);
  let urgentExpiries = $derived(inventory.expiries.filter((record) => ['expired', 'action_needed', 'expiring'].includes(record.status)));
  let expiryWindow = $derived(urgentExpiries.slice(0, 2).map((record) => record.days_remaining === null ? 'unknown' : `${record.days_remaining} days`).join(' · ') || 'No action-needed records');
  let unreviewedChanges = $derived(inventory.changes.filter((change) => !change.seen));
  let latestScanID = $derived(unreviewedChanges.length ? Math.max(...unreviewedChanges.map((change) => change.scan_run_id)) : null);
  let publishedPorts = $derived(inventory.ports.filter((port) => port.published).length);

  function stateClass(state: string) {
    return state === 'running' || state === 'active' ? 'ok' : state === 'gone' ? 'bad' : 'warn';
  }

  function observedLabel(state: string) {
    return state === 'gone' ? 'Gone' : state === 'restarting' ? 'Restarting' : 'Observed';
  }
</script>

<main class="page">
  <PageHead kicker="Index · Services" title="Services" meta={`${recordTotal} records · ${inventory.hosts.length} hosts`}>
    {#snippet actions()}<button class="quiet-button" onclick={() => navigate('/hosts')}>View hosts</button><button class="primary-button" onclick={() => navigate('/copy-my-lab')}>Copy my lab</button>{/snippet}
  </PageHead>
  <nav class="action-ledger" data-component-id="index-action-ledger" aria-label="Review queue">
    <a href={firstUnresolved ? `/routes/${firstUnresolved.id}` : '/routes'} onclick={(event) => { event.preventDefault(); navigate(firstUnresolved ? `/routes/${firstUnresolved.id}` : '/routes'); }}><span class="number">{unresolvedRoutes.length}</span><span><strong>Unresolved routes</strong><small>{firstUnresolved ? `${firstUnresolved.domain}${firstUnresolved.path_prefix || '/'}` : 'No unresolved records'}</small></span></a>
    <a href="/expiry" onclick={(event) => { event.preventDefault(); navigate('/expiry'); }}><span class="number">{urgentExpiries.length}</span><span><strong>Expiry review</strong><small>{expiryWindow}</small></span></a>
    <a href="/changes" onclick={(event) => { event.preventDefault(); navigate('/changes'); }}><span class="number">{unreviewedChanges.length}</span><span><strong>Unreviewed changes</strong><small>{latestScanID ? `Latest scan ${latestScanID}` : 'No pending review'}</small></span></a>
    <a href="/ports" onclick={(event) => { event.preventDefault(); navigate('/ports'); }}><span class="number">{publishedPorts}</span><span><strong>Published ports</strong><small>{inventory.ports.length} TOTAL DECLARATIONS</small></span></a>
  </nav>
  <div class="toolbar" data-component-id="service-register-controls">
    <input class="inline-search" bind:value={query} aria-label="Filter services" placeholder={`Filter ${recordTotal} services`} />
    <span class="spacer"></span><span class="toolbar-meta">{visible.length} VISIBLE · {recordTotal} TOTAL</span>
  </div>
  <section class="table-shell" data-component-id="virtualized-service-register">
    <header class="table-head"><span>Service + stack + record ID</span><span>Image : tag</span><span>Route / address</span><span>Host</span><span>Port facts</span><span>Seen</span></header>
    {#if visible.length}
      <div class="virtual-window" aria-label="Virtualized service window">
        {#each visible as service, index}
          <article class:selected={index === 0} class="service-row" data-component-id={`service-row-${service.name}`}>
            <div class="service-name" data-label="Service + stack + record ID"><i class={`dot ${stateClass(service.state)}`} title={observedLabel(service.state)}></i><span><strong>{service.name}</strong><small>{service.stack} · SRV-{String(service.id).padStart(3, '0')}</small></span></div>
            <div class="cell mono" data-label="Image : tag">{service.image}<small>{service.tag}</small></div>
            <div class="cell" data-label="Route / address"><span class:route-link={service.route && service.route !== '—'}>{service.route && service.route !== '—' ? service.route : 'No public route'}</span><small>{service.host}:{service.ports.split(' ')[0]}</small></div>
            <div class="cell" data-label="Host">{service.host}</div>
            <div class="cell mono" data-label="Port facts">{service.ports}</div>
            <div class="cell mono" data-label="Seen">{relativeTime(service.last_seen)}</div>
          </article>
        {/each}
      </div>
      <footer class="table-footer"><span>{visible.length} of {recordTotal} records</span><span>{unresolvedRoutes.length} unresolved routes</span></footer>
    {:else}
      <div class="empty-register"><strong>{inventory.services.length ? 'NO MATCHING RECORDS' : 'NO SERVICES INDEXED'}</strong><span>{inventory.services.length ? `No service contains “${query}”.` : inventory.readOnly ? 'This shared inventory contains no service records.' : 'Add a read-only source, then run the first scan.'}</span>{#if !inventory.services.length && !inventory.readOnly}<button class="primary-button" onclick={() => navigate('/setup')}>Add a source</button>{/if}</div>
    {/if}
  </section>
</main>
