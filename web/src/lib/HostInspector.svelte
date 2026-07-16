<script lang="ts">
  import { X } from 'lucide-svelte';
  import type { Inventory } from './api';
  import type { Host } from './types';
  import { navigate } from './router';

  let { host, inventory }: { host: Host; inventory: Inventory } = $props();
  let tab = $state<'Overview' | 'Notes' | 'History'>('Overview');
  let services = $derived(inventory.services.filter((service) => service.host === host.name));
  let ports = $derived(inventory.ports.filter((port) => port.host === host.name));
  let routes = $derived(inventory.routes.filter((route) => route.upstream_host === host.name || services.some((service) => service.name === route.service)));

  function close() {
    navigate('/hosts');
  }

  function handleKeydown(event: KeyboardEvent) {
    if (event.key === 'Escape') close();
  }
</script>

<svelte:window onkeydown={handleKeydown} />
<button class="scrim" aria-label="Close connected record inspector" onclick={close}></button>
<div class="inspector" data-component-id="connected-record-inspector" role="dialog" aria-modal="true" aria-label={`Connected records for ${host.name}`}>
  <header class="inspector-head">
    <div class="row"><span class="section-label">Connected-record inspector</span><button class="icon-button" aria-label="Close inspector" onclick={close}><X size={15} /></button></div>
    <h2>{host.name}</h2>
    <p>HST-{String(host.id).padStart(3, '0')} · {host.kind.toUpperCase()} · OBSERVED {host.last_seen ?? 'RECENTLY'}</p>
  </header>
  <nav class="tabs" aria-label="Inspector sections">
    {#each ['Overview', 'Notes', 'History'] as item}
      <button class:active={tab === item} onclick={() => (tab = item as typeof tab)}>{item}</button>
    {/each}
  </nav>
  <div class="inspector-body">
    {#if tab === 'Overview'}
      <section class="inspect-section"><h3>HOST FACTS</h3><dl class="definition-list"><div><dt>Address</dt><dd class="mono">{host.address}</dd></div><div><dt>System</dt><dd>{host.os} · {host.arch}</dd></div><div><dt>Engine</dt><dd>Docker 28.1.1</dd></div><div><dt>Source</dt><dd>docker-socket-proxy</dd></div><div><dt>Last seen</dt><dd>{host.last_seen ?? 'Recently'}</dd></div></dl></section>
      <section class="inspect-section"><h3>CONNECTED RECORDS · {services.length + ports.length + routes.length}</h3>
        {#each services.slice(0, 2) as service}<div class="connected-row"><b>S</b><div><strong>{service.name}</strong><small>Service · {service.stack} · {service.state}</small></div><i>›</i></div>{/each}
        {#each routes.slice(0, 1) as route}<div class="connected-row"><b>R</b><div><strong>{route.domain}</strong><small>Route · {route.proxy}</small></div><i>›</i></div>{/each}
        {#each ports.slice(0, 1) as port}<div class="connected-row"><b>P</b><div><strong>{port.number} / {port.protocol}</strong><small>Port · {port.published ? 'published' : 'internal'} · {port.service}</small></div><i>›</i></div>{/each}
      </section>
    {:else if tab === 'Notes'}
      <section class="inspect-section"><h3>PRIVATE NOTE</h3><p class="inspector-copy">Primary storage host. Original media remains on the host and is never included in Copy my lab.</p></section>
    {:else}
      <section class="inspect-section"><h3>OBSERVATION HISTORY</h3><div class="connected-row"><b>42</b><div><strong>Current scan</strong><small>Observed {host.last_seen ?? 'recently'} · no factual changes</small></div></div><div class="connected-row"><b>41</b><div><strong>Prior scan</strong><small>Observed 17 minutes ago · complete</small></div></div></section>
    {/if}
  </div>
</div>
