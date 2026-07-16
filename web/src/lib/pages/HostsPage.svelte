<script lang="ts">
  import type { Inventory } from '../api';
  import PageHead from '../PageHead.svelte';
  import HostInspector from '../HostInspector.svelte';
  import { navigate } from '../router';

  let { path, inventory }: { path: string; inventory: Inventory } = $props();
  const hostOrder = ['nas-01', 'gateway', 'core-01'];
  let hosts = $derived([...inventory.hosts].sort((a, b) => {
    const ai = hostOrder.indexOf(a.name);
    const bi = hostOrder.indexOf(b.name);
    return (ai < 0 ? 99 : ai) - (bi < 0 ? 99 : bi) || a.name.localeCompare(b.name);
  }));
  let selectedID = $derived(Number(path.split('?')[0].match(/^\/hosts\/(\d+)/)?.[1] ?? 0));
  let selectedHost = $derived(hosts.find((host) => host.id === selectedID));

  function countServices(name: string) { return inventory.services.filter((service) => service.host === name).length; }
  function countPorts(name: string) { return inventory.ports.filter((port) => port.host === name).length; }
  function countRoutes(name: string) { return inventory.routes.filter((route) => route.upstream_host === name || inventory.services.some((service) => service.host === name && service.name === route.service)).length; }
</script>

<main class="page">
  <PageHead kicker="INDEX · HOSTS" title="Where the address book lives." copy={`${hosts.length} observed Docker hosts, presented as source records rather than monitoring nodes.`}>
    {#snippet actions()}<button class="quiet-button">Export hosts</button><button class="primary-button" onclick={() => navigate('/sources')}>Scan sources</button>{/snippet}
  </PageHead>
  {#if hosts.length}
    <section class="host-grid" data-component-id="host-register">
      {#each hosts as host}
        <article class:selected={host.id === selectedID} class="host-record" data-component-id={`host-record-${host.name}`}>
          <header><h2>{host.name}</h2><span class="status ok">Observed</span></header>
          <p class="address">{host.address} · {host.os} · {host.arch}</p>
          <dl><div><dt>Services</dt><dd>{host.services ?? countServices(host.name)}</dd></div><div><dt>Ports</dt><dd>{host.ports ?? countPorts(host.name)}</dd></div><div><dt>Routes</dt><dd>{countRoutes(host.name)}</dd></div></dl>
          <button class="quiet-button" onclick={() => navigate(`/hosts/${host.id}`)}>{host.id === selectedID ? 'Inspector open' : 'Open connected records'}</button>
        </article>
      {/each}
    </section>
  {:else}
    <section class="empty-register"><strong>NO HOSTS INDEXED</strong><span>Connect a Docker source to create the host register.</span><button class="primary-button" onclick={() => navigate('/sources')}>Add a source</button></section>
  {/if}
</main>
{#if selectedHost}<HostInspector host={selectedHost} {inventory} />{/if}
