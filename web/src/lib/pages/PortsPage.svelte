<script lang="ts">
  import { onMount } from 'svelte';
  import { loadNextFreePort, loadPortConflicts, type Inventory } from '../api';
  import type { Port, PortConflict } from '../types';
  import PageHead from '../PageHead.svelte';

  let { inventory }: { inventory: Inventory } = $props();
  let query = $state('');
  let selectedHostID = $state<number | null>(null);
  let nextPort = $state<number | null>(null);
  let nextPortError = $state('');
  let loadingNextPort = $state(false);
  let conflicts = $state<PortConflict[]>([]);
  let conflictError = $state('');
  let nextPortToken = 0;
  let rows = $derived([...inventory.ports]
    .filter((port) => (!selectedHostID || port.host_id === selectedHostID) && `${port.number} ${port.protocol} ${port.service} ${port.host}`.toLowerCase().includes(query.toLowerCase()))
    .sort((a, b) => a.number - b.number || (a.host ?? '').localeCompare(b.host ?? '')));
  let total = $derived(inventory.ports.length);
  let selectedHost = $derived(inventory.hosts.find((host) => host.id === selectedHostID));

  onMount(async () => {
    selectedHostID = inventory.hosts[0]?.id ?? null;
    try {
      conflicts = await loadPortConflicts();
    } catch (cause) {
      conflictError = cause instanceof Error ? cause.message : 'Port conflicts could not be checked.';
    }
  });

  function conflictCount(port: Port): number {
    return conflicts.find((conflict) => conflict.host_id === port.host_id && conflict.number === port.number && conflict.protocol === port.protocol)?.count ?? 0;
  }

  $effect(() => {
    const hostID = selectedHostID;
    // Guard on null/undefined, not truthiness, so a real host with id 0 is looked up.
    if (hostID != null) void findNextPort(hostID);
  });

  async function findNextPort(hostID: number) {
    // Sequence overlapping lookups: a slow response for host A must not overwrite
    // a newer response for host B (which would make "Copy port" copy the wrong host's port).
    const token = ++nextPortToken;
    loadingNextPort = true;
    nextPort = null;
    nextPortError = '';
    try {
      const port = await loadNextFreePort(hostID);
      if (token !== nextPortToken) return;
      nextPort = port;
    } catch (cause) {
      if (token !== nextPortToken) return;
      nextPortError = cause instanceof Error ? cause.message : 'The next-free port could not be checked.';
    } finally {
      if (token === nextPortToken) loadingNextPort = false;
    }
  }

  async function copyPort() {
    if (nextPort !== null) await navigator.clipboard?.writeText(String(nextPort));
  }
</script>

<main class="page">
  <PageHead title="Ports" meta={`${total} declarations${conflicts.length ? ` · ${conflicts.length} conflicts` : ''}`} />
  {#if conflictError}<div class="summary-line" role="alert"><strong>Port conflicts unavailable</strong><span>{conflictError}</span><button class="quiet-button" onclick={() => (conflictError = '')}>Dismiss</button></div>{/if}
  {#if inventory.hosts.length}<section class="next-port" data-component-id="next-free-port"><span class="number">{loadingNextPort ? '…' : nextPort ?? '—'}</span><div><strong>Next unused TCP port</strong><small>{nextPortError || `checked for ${selectedHost?.name || 'selected host'} from 1024`}</small></div><button class="primary-button" disabled={nextPort === null} onclick={copyPort}>Copy {nextPort ?? 'port'}</button></section>{/if}
  <div class="toolbar"><input class="inline-search" bind:value={query} aria-label="Filter ports" placeholder="Find port, service, or host" />{#if inventory.hosts.length}<label class="field-label" for="port-host">Host</label><select id="port-host" bind:value={selectedHostID} aria-label="Select host for port lookup">{#each inventory.hosts as host}<option value={host.id}>{host.name}</option>{/each}</select>{/if}<span class="spacer"></span><span class="toolbar-meta">{rows.length} VISIBLE · {total} DECLARATIONS</span></div>
  <section class="register" data-component-id="port-allocation-register">
    <header class="register-head port-cols"><span class="num">Port</span><span>Proto</span><span>Service</span><span>Host</span><span>Binding</span><span>Scope</span></header>
    {#if rows.length}
      {#each rows as port}
        <div class="register-row port-cols"><div class="port-number num" data-label="Port">{port.number}</div><div class="mono" data-label="Proto">{port.protocol}</div><div data-label="Service"><strong>{port.service ?? 'Unjoined'}</strong></div><div data-label="Host" title={port.host_ip || '0.0.0.0'}>{port.host ?? 'Unknown'}{#if port.host_ip && port.host_ip !== '0.0.0.0'}<small>{port.host_ip}</small>{/if}</div><div data-label="Binding"><code>→ {port.container_port}</code></div><div data-label="Scope"><span class:warn={Boolean(port.published)} class:ok={!Boolean(port.published)} class="status">{port.published ? 'Published' : 'Internal'}</span>{#if conflictCount(port)}<span class="status bad">conflict ×{conflictCount(port)}</span>{/if}</div></div>
      {/each}
    {:else}
      <div class="empty-register"><strong>{inventory.ports.length ? 'NO MATCHING PORTS' : 'NO PORTS INDEXED'}</strong><span>{inventory.ports.length ? `No declaration contains “${query}”.` : 'Port declarations appear after the first source scan.'}</span></div>
    {/if}
  </section>
</main>
