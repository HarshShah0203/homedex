<script lang="ts">
  import type { Inventory } from '../api';
  import PageHead from '../PageHead.svelte';
  import { navigate } from '../router';
  import { plural, relativeTime } from '../time';

  let { inventory }: { inventory: Inventory } = $props();
  let query = $state('');
  let visible = $derived(inventory.services.filter((service) => `${service.name} ${service.stack} ${service.image} ${service.host} ${service.route}`.toLowerCase().includes(query.toLowerCase())));
  let recordTotal = $derived(inventory.services.length);
  let unresolvedRoutes = $derived(inventory.routes.filter((route) => !['ok', 'resolved'].includes(route.status.toLowerCase())));
  let firstUnresolved = $derived(unresolvedRoutes[0]);
  let urgentExpiries = $derived(inventory.expiries.filter((record) => ['expired', 'action_needed', 'expiring'].includes(record.status)));
  let nextExpiryDays = $derived.by(() => {
    const days = urgentExpiries.map((record) => record.days_remaining).filter((value): value is number => value !== null);
    return days.length ? Math.min(...days) : null;
  });
  let unreviewedChanges = $derived(inventory.changes.filter((change) => !change.seen));
  let publishedPorts = $derived(inventory.ports.filter((port) => port.published).length);

  function stateClass(state: string) {
    return state === 'running' || state === 'active' ? 'ok' : state === 'gone' ? 'bad' : 'warn';
  }

  function observedLabel(state: string) {
    return state === 'gone' ? 'Gone' : state === 'restarting' ? 'Restarting' : 'Observed';
  }

  function splitImage(image: string): [string, string] {
    const slash = image.lastIndexOf('/');
    const colon = image.indexOf(':', slash + 1);
    return colon > 0 ? [image.slice(0, colon), image.slice(colon)] : [image, ''];
  }

  function portParts(ports: string): { shown: string; more: number } {
    const parts = ports.split(',').map((part) => part.trim()).filter(Boolean);
    if (!parts.length) return { shown: '—', more: 0 };
    return { shown: parts[0], more: parts.length - 1 };
  }

  function go(event: MouseEvent, path: string) {
    event.preventDefault();
    navigate(path);
  }
</script>

<main class="page">
  <PageHead title="Services" meta={`${plural(recordTotal, 'record')} · ${plural(inventory.hosts.length, 'host')}`}>
    {#snippet actions()}<button class="quiet-button" onclick={() => navigate('/hosts')}>View hosts</button><button class="primary-button" onclick={() => navigate('/copy-my-lab')}>Copy my lab</button>{/snippet}
  </PageHead>
  <nav class="action-ledger" data-component-id="index-action-ledger" aria-label="Review queue">
    <a href={firstUnresolved ? `/routes/${firstUnresolved.id}` : '/routes'} onclick={(event) => go(event, firstUnresolved ? `/routes/${firstUnresolved.id}` : '/routes')}><span class="number" class:zero={!unresolvedRoutes.length}>{unresolvedRoutes.length}</span><strong>unresolved {unresolvedRoutes.length === 1 ? 'route' : 'routes'}</strong>{#if firstUnresolved}<small>{firstUnresolved.domain}</small>{/if}</a>
    <a href="/expiry" onclick={(event) => go(event, '/expiry')}><span class="number" class:zero={!urgentExpiries.length}>{urgentExpiries.length}</span><strong>expiring soon</strong>{#if nextExpiryDays !== null}<small>next {nextExpiryDays}d</small>{/if}</a>
    <a href="/changes" onclick={(event) => go(event, '/changes')}><span class="number" class:zero={!unreviewedChanges.length}>{unreviewedChanges.length}</span><strong>unreviewed {unreviewedChanges.length === 1 ? 'change' : 'changes'}</strong></a>
    <a href="/ports" onclick={(event) => go(event, '/ports')}><span class="number" class:zero={!publishedPorts}>{publishedPorts}</span><strong>published ports</strong><small>{inventory.ports.length} total</small></a>
  </nav>
  <div class="toolbar" data-component-id="service-register-controls">
    <input class="inline-search" bind:value={query} aria-label="Filter services" placeholder={`Filter ${recordTotal} services`} />
    <span class="spacer"></span><span class="toolbar-meta">{visible.length} VISIBLE · {recordTotal} TOTAL</span>
  </div>
  <section class="table-shell" data-component-id="virtualized-service-register">
    <header class="table-head"><span>Service</span><span>Image</span><span>Route</span><span>Host</span><span class="num">Ports</span><span class="num">Seen</span></header>
    {#if visible.length}
      <div class="virtual-window" aria-label="Service records">
        {#each visible as service}
          {@const image = splitImage(`${service.image}${service.tag ? `:${service.tag}` : ''}`)}
          {@const ports = portParts(service.ports)}
          <article class="service-row" data-component-id={`service-row-${service.name}`}>
            <div class="service-name" data-label="Service"><i class={`dot ${stateClass(service.state)}`} title={observedLabel(service.state)}></i><strong>{service.name}</strong><small>{service.stack} · SRV-{String(service.id).padStart(3, '0')}</small></div>
            <div class="cell mono" data-label="Image" title={`${service.image}:${service.tag}`}>{image[0]}<span class="dim">{image[1]}</span></div>
            <div class="cell" data-label="Route" title={`${service.host}:${service.ports.split(' ')[0]}`}>{#if service.route && service.route !== '—'}<span class="route-link">{service.route}</span>{:else}<span class="dim">—</span>{/if}</div>
            <div class="cell" data-label="Host">{service.host}</div>
            <div class="cell mono num" data-label="Ports" title={service.ports}>{ports.shown}{#if ports.more}<span class="port-more">+{ports.more}</span>{/if}</div>
            <div class="cell mono num dim" data-label="Seen">{relativeTime(service.last_seen)}</div>
          </article>
        {/each}
      </div>
      <footer class="table-footer"><span>{visible.length} of {recordTotal} records</span><span>{unresolvedRoutes.length} unresolved {unresolvedRoutes.length === 1 ? 'route' : 'routes'}</span></footer>
    {:else}
      <div class="empty-register"><strong>{inventory.services.length ? 'NO MATCHING RECORDS' : 'NO SERVICES INDEXED'}</strong><span>{inventory.services.length ? `No service contains “${query}”.` : inventory.readOnly ? 'This shared inventory contains no service records.' : 'Add a read-only source, then run the first scan.'}</span>{#if !inventory.services.length && !inventory.readOnly}<button class="primary-button" onclick={() => navigate('/setup')}>Add a source</button>{/if}</div>
    {/if}
  </section>
</main>
