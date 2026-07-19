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
  let serviceTotal = $derived(inventory.services.length);

  function countServices(name: string) { return inventory.services.filter((service) => service.host === name).length; }
  function countPorts(name: string) { return inventory.ports.filter((port) => port.host === name).length; }
  function countRoutes(name: string) { return inventory.routes.filter((route) => route.upstream_host === name || inventory.services.some((service) => service.host === name && service.name === route.service)).length; }
</script>

<main class="page">
  <PageHead kicker="Index · Hosts" title="Hosts" meta={`${hosts.length} hosts · ${serviceTotal} services`}>
    {#snippet actions()}{#if !inventory.readOnly}<button class="primary-button" onclick={() => navigate('/sources')}>Manage sources</button>{/if}{/snippet}
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
    <section class="empty-register"><strong>NO HOSTS INDEXED</strong><span>{inventory.readOnly ? 'This shared inventory contains no host records.' : 'Connect a Docker source to create the host register.'}</span>{#if !inventory.readOnly}<button class="primary-button" onclick={() => navigate('/setup')}>Add a source</button>{/if}</section>
  {/if}
</main>
{#if selectedHost}<HostInspector host={selectedHost} {inventory} />{/if}
