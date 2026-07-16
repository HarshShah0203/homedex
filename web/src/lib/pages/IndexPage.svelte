<script lang="ts">
  import type { Inventory } from '../api';
  import PageHead from '../PageHead.svelte';
  import { navigate } from '../router';

  let { inventory }: { inventory: Inventory } = $props();
  let query = $state('');
  let visible = $derived(inventory.services.filter((service) => `${service.name} ${service.stack} ${service.image} ${service.host} ${service.route}`.toLowerCase().includes(query.toLowerCase())));
  let recordTotal = $derived(inventory.source === 'demo' ? 42 : inventory.services.length);

  function stateClass(state: string) {
    return state === 'running' || state === 'active' ? 'ok' : state === 'gone' ? 'bad' : 'warn';
  }

  function observedLabel(state: string) {
    return state === 'gone' ? 'Gone' : state === 'restarting' ? 'Restarting' : 'Observed';
  }
</script>

<main class="page">
  <PageHead kicker="INDEX · SERVICES" title="Everything, in its place." copy={`${recordTotal} service records across ${inventory.hosts.length || 3} hosts. Observation state is kept separate from reachability or health.`}>
    {#snippet actions()}<button class="quiet-button" onclick={() => navigate('/hosts')}>View hosts</button><button class="primary-button" onclick={() => navigate('/copy-my-lab')}>Copy my lab</button>{/snippet}
  </PageHead>
  {#if inventory.error}<div class="source-notice"><span class="status warn">Offline copy</span><p>{inventory.error} Showing the last designed development inventory.</p></div>{/if}
  <nav class="action-ledger" data-component-id="index-action-ledger" aria-label="Review queue">
    <a href="/routes/old.lab.example" onclick={(event) => { event.preventDefault(); navigate('/routes/old.lab.example'); }}><span class="number">1</span><span><strong>Unresolved route</strong><small>old.lab.example</small></span></a>
    <a href="/expiry" onclick={(event) => { event.preventDefault(); navigate('/expiry'); }}><span class="number">2</span><span><strong>Expiry review</strong><small>14d · 23d</small></span></a>
    <a href="/changes" onclick={(event) => { event.preventDefault(); navigate('/changes'); }}><span class="number">3</span><span><strong>Unreviewed changes</strong><small>SCAN 042</small></span></a>
    <a href="/ports" onclick={(event) => { event.preventDefault(); navigate('/ports'); }}><span class="number">8082</span><span><strong>Next-free port</strong><small>TCP · ALL HOSTS</small></span></a>
  </nav>
  <div class="toolbar" data-component-id="service-register-controls">
    <input class="inline-search" bind:value={query} aria-label="Filter services" placeholder={`Filter ${recordTotal} services`} />
    <button class="filter-button"><b>Host</b><span>All</span>⌄</button><button class="filter-button"><b>Stack</b><span>All</span>⌄</button><button class="filter-button"><b>State</b><span>Current</span>⌄</button><button class="filter-button"><b>Source</b><span>Any</span>⌄</button><button class="filter-button"><b>Sort</b><span>Name A–Z</span>⌄</button>
    <span class="spacer"></span><span class="toolbar-meta">{visible.length} VISIBLE · {recordTotal} TOTAL</span>
  </div>
  <section class="table-shell" data-component-id="virtualized-service-register">
    <header class="table-head"><span>Service + stack + record ID</span><span>Image : tag</span><span>Route / address</span><span>Host</span><span>Port facts</span><span>Seen</span></header>
    {#if visible.length}
      <div class="virtual-window" aria-label="Virtualized service window">
        {#each visible as service, index}
          <article class:selected={index === 0} class="service-row" data-component-id={`service-row-${service.name}`}>
            <div class="service-name" data-label="Service + stack + record ID"><i class={`dot ${stateClass(service.state)}`}></i><span><strong>{service.name}</strong><small>{service.stack} · SRV-{String(service.id).padStart(3, '0')} · {observedLabel(service.state)}</small></span></div>
            <div class="cell mono" data-label="Image : tag">{service.image}<small>{service.tag}</small></div>
            <div class="cell" data-label="Route / address"><span class:route-link={service.route && service.route !== '—'}>{service.route && service.route !== '—' ? service.route : 'No public route'}</span><small>{service.host}:{service.ports.split(' ')[0]}</small></div>
            <div class="cell" data-label="Host">{service.host}<small>Docker</small></div>
            <div class="cell mono" data-label="Port facts">{service.ports}<small>allocated</small></div>
            <div class="cell mono" data-label="Seen">{service.last_seen.replace(' ago', '')}<small>ago</small></div>
          </article>
        {/each}
      </div>
      <footer class="table-footer"><span>ROWS 1–{visible.length} OF {recordTotal} · ESTIMATED ROW HEIGHT 60PX</span><span>VIRTUAL WINDOW · {visible.length} RECORDS RENDERED</span></footer>
    {:else}
      <div class="empty-register"><strong>{inventory.services.length ? 'NO MATCHING RECORDS' : 'NO SERVICES INDEXED'}</strong><span>{inventory.services.length ? `No service contains “${query}”.` : 'Add a read-only source, then run the first scan.'}</span>{#if !inventory.services.length}<button class="primary-button" onclick={() => navigate('/sources')}>Add a source</button>{/if}</div>
    {/if}
  </section>
</main>
