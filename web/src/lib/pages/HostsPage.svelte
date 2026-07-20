<script lang="ts">
  import { createManualEntity, type Inventory } from '../api';
  import PageHead from '../PageHead.svelte';
  import { plural } from '../time';
  import HostInspector from '../HostInspector.svelte';
  import { navigate } from '../router';

  let { path, inventory, onrefresh = async () => {} }: { path: string; inventory: Inventory; onrefresh?: () => Promise<void> } = $props();
  let showAdd = $state(false);
  let name = $state('');
  let address = $state('');
  let os = $state('');
  let addError = $state('');
  let adding = $state(false);

  async function submitHost(event: SubmitEvent) {
    event.preventDefault();
    addError = '';
    if (!name.trim()) {
      addError = 'A host name is required.';
      return;
    }
    adding = true;
    try {
      await createManualEntity({ entity_type: 'host', kind: 'manual', name: name.trim(), address: address.trim() || undefined, os: os.trim() || undefined });
      name = '';
      address = '';
      os = '';
      showAdd = false;
      await onrefresh();
    } catch (cause) {
      addError = cause instanceof Error ? cause.message : 'The host could not be created.';
    } finally {
      adding = false;
    }
  }
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
  <PageHead title="Hosts" meta={`${plural(hosts.length, 'host')} · ${plural(serviceTotal, 'service')}`}>
    {#snippet actions()}{#if !inventory.readOnly}<button class="quiet-button" onclick={() => (showAdd = !showAdd)}>Add manual host</button><button class="primary-button" onclick={() => navigate('/sources')}>Manage sources</button>{/if}{/snippet}
  </PageHead>
  {#if !inventory.readOnly && showAdd}
    <form class="source-editor" onsubmit={submitHost}>
      <label>Name <input type="text" bind:value={name} placeholder="printer" /></label>
      <label>Address <input type="text" bind:value={address} placeholder="10.0.0.50" /></label>
      <label>OS <input type="text" bind:value={os} placeholder="linux" /></label>
      <div class="form-actions">
        <button class="primary-button" disabled={adding}>Add host</button>
        <button type="button" class="quiet-button" disabled={adding} onclick={() => { showAdd = false; addError = ''; }}>Cancel</button>
        {#if addError}<span class="status bad" role="alert">{addError}</span>{/if}
      </div>
    </form>
  {/if}
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
{#if selectedHost}<HostInspector host={selectedHost} {inventory} readOnly={inventory.readOnly} />{/if}
