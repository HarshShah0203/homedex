<script lang="ts">
  import { onMount } from 'svelte';
  import { loadNextFreePort, type Inventory } from '../api';
  import PageHead from '../PageHead.svelte';

  let { inventory }: { inventory: Inventory } = $props();
  let query = $state('');
  let selectedHostID = $state<number | null>(null);
  let nextPort = $state<number | null>(null);
  let nextPortError = $state('');
  let loadingNextPort = $state(false);
  let rows = $derived([...inventory.ports]
    .filter((port) => (!selectedHostID || port.host_id === selectedHostID) && `${port.number} ${port.protocol} ${port.service} ${port.host}`.toLowerCase().includes(query.toLowerCase()))
    .sort((a, b) => a.number - b.number || (a.host ?? '').localeCompare(b.host ?? '')));
  let total = $derived(inventory.ports.length);
  let selectedHost = $derived(inventory.hosts.find((host) => host.id === selectedHostID));

  onMount(() => {
    selectedHostID = inventory.hosts[0]?.id ?? null;
  });

  $effect(() => {
    const hostID = selectedHostID;
    if (hostID) void findNextPort(hostID);
  });

  async function findNextPort(hostID: number) {
    loadingNextPort = true;
    nextPort = null;
    nextPortError = '';
    try {
      nextPort = await loadNextFreePort(hostID);
    } catch (cause) {
      nextPortError = cause instanceof Error ? cause.message : 'The next-free port could not be checked.';
    } finally {
      loadingNextPort = false;
    }
  }

  async function copyPort() {
    if (nextPort !== null) await navigator.clipboard?.writeText(String(nextPort));
  }
</script>

<main class="page">
  <PageHead kicker="PORTS · ALLOCATION LEDGER" title="Know what is already spoken for." copy={`${total} port declarations are indexed by number, protocol, host, service, and publication scope.`} />
  {#if inventory.hosts.length}<section class="next-port" data-component-id="next-free-port"><span class="number">{loadingNextPort ? '…' : nextPort ?? '—'}</span><div><strong>Next unused TCP port</strong><small>{nextPortError || `Checked for ${selectedHost?.name || 'selected host'} from port 1024`}</small></div><button class="primary-button" disabled={nextPort === null} onclick={copyPort}>Copy {nextPort ?? 'port'}</button></section>{/if}
  <div class="toolbar"><input class="inline-search" bind:value={query} aria-label="Filter ports" placeholder="Find port, service, or host" />{#if inventory.hosts.length}<label class="field-label" for="port-host">Host</label><select id="port-host" bind:value={selectedHostID} aria-label="Select host for port lookup">{#each inventory.hosts as host}<option value={host.id}>{host.name}</option>{/each}</select>{/if}<span class="spacer"></span><span class="toolbar-meta">{rows.length} VISIBLE · {total} DECLARATIONS</span></div>
  <section class="register" data-component-id="port-allocation-register">
    <header class="register-head port-cols"><span>Port</span><span>Protocol</span><span>Service</span><span>Host</span><span>Container fact</span><span>Scope</span></header>
    {#if rows.length}
      {#each rows as port}
        <div class="register-row port-cols"><div class="port-number" data-label="Port">{port.number}</div><div class="mono" data-label="Protocol">{port.protocol}</div><div data-label="Service"><strong>{port.service ?? 'Unjoined service'}</strong><small>Docker service</small></div><div data-label="Host"><strong>{port.host ?? 'Unknown host'}</strong><small>{port.host_ip || '0.0.0.0'}</small></div><div data-label="Container fact"><code>container {port.container_port}</code><small>observed declaration</small></div><div data-label="Scope"><span class:warn={Boolean(port.published)} class:ok={!Boolean(port.published)} class="status">{port.published ? 'Published' : 'Internal'}</span></div></div>
      {/each}
    {:else}
      <div class="empty-register"><strong>{inventory.ports.length ? 'NO MATCHING PORTS' : 'NO PORTS INDEXED'}</strong><span>{inventory.ports.length ? `No declaration contains “${query}”.` : 'Port declarations appear after the first source scan.'}</span></div>
    {/if}
  </section>
</main>
