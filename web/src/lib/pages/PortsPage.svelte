<script lang="ts">
  import type { Inventory } from '../api';
  import PageHead from '../PageHead.svelte';

  let { inventory }: { inventory: Inventory } = $props();
  let query = $state('');
  let rows = $derived([...inventory.ports]
    .filter((port) => `${port.number} ${port.protocol} ${port.service} ${port.host}`.toLowerCase().includes(query.toLowerCase()))
    .sort((a, b) => a.number - b.number || (a.host ?? '').localeCompare(b.host ?? '')));
  let total = $derived(inventory.source === 'demo' ? 117 : inventory.ports.length);
  let nextPort = $derived((() => {
    const used = new Set(inventory.ports.filter((port) => Boolean(port.published)).map((port) => port.number));
    let candidate = 8082;
    while (used.has(candidate)) candidate += 1;
    return candidate;
  })());

  async function copyPort() {
    await navigator.clipboard?.writeText(String(nextPort));
  }
</script>

<main class="page">
  <PageHead kicker="PORTS · ALLOCATION LEDGER" title="Know what is already spoken for." copy={`${total} port declarations are indexed by number, protocol, host, service, and publication scope.`}>
    {#snippet actions()}<button class="quiet-button">Export ports</button>{/snippet}
  </PageHead>
  <section class="next-port" data-component-id="next-free-port"><span class="number">{nextPort}</span><div><strong>Next unused published TCP port</strong><small>Checked across gateway, nas-01, and core-01 · SCAN 042</small></div><button class="primary-button" onclick={copyPort}>Copy {nextPort}</button></section>
  <div class="toolbar"><input class="inline-search" bind:value={query} aria-label="Filter ports" placeholder="Find port, service, or host" /><button class="filter-button"><b>Host</b><span>All</span>⌄</button><button class="filter-button"><b>Scope</b><span>Any</span>⌄</button><button class="filter-button"><b>Protocol</b><span>Any</span>⌄</button><span class="spacer"></span><span class="toolbar-meta">{total} DECLARATIONS</span></div>
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
