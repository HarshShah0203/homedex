<script lang="ts">
  import type { Inventory } from '../api';
  import PageHead from '../PageHead.svelte';

  let { inventory }: { inventory: Inventory } = $props();

  function state(connector: Inventory['connectors'][number]) {
    return connector.last_status === 'connected' || connector.last_status === 'ok' ? 'ok' : 'bad';
  }

  function stateLabel(connector: Inventory['connectors'][number]) {
    return state(connector) === 'ok' ? 'Connected' : connector.last_error?.split(':')[0] || 'Connection refused';
  }
</script>

<main class="page">
  <PageHead kicker="SOURCES · SETTINGS" title="The index starts with declared access." copy="Every connector records its endpoint, read scope, last result, and schedule. There are no agents.">
    {#snippet actions()}<button class="primary-button">Add source</button>{/snippet}
  </PageHead>
  <section class="settings-layout">
    <div class="sources-register">
      <div class="toolbar sources-toolbar"><button class="filter-button"><b>Kind</b><span>All</span>⌄</button><button class="filter-button"><b>State</b><span>Any</span>⌄</button><span class="spacer"></span><span class="toolbar-meta">{inventory.connectors.length} SOURCES · {inventory.connectors.filter((connector) => state(connector) === 'ok').length} CONNECTED</span></div>
      <section class="register" data-component-id="source-register">
        <header class="register-head source-cols"><span>Source</span><span>Endpoint</span><span>State</span><span>Indexed</span><span>Schedule</span></header>
        {#if inventory.connectors.length}
          {#each inventory.connectors as connector}<div class="register-row source-cols"><div data-label="Source"><strong>{connector.name}</strong><small>SRC-{String(connector.id).padStart(3, '0')}</small></div><div data-label="Endpoint"><code>{connector.endpoint || 'Endpoint retained locally'}</code></div><div data-label="State"><span class={`status ${state(connector)}`}>{stateLabel(connector)}</span></div><div data-label="Indexed"><strong>{connector.found || 'Awaiting scan'}</strong><small>last scan 2m ago</small></div><div data-label="Schedule"><span class="mono">{connector.schedule_minutes} min</span></div></div>{/each}
        {:else}
          <div class="empty-register"><strong>NO SOURCES DECLARED</strong><span>Add a read-only connector to begin the inventory.</span><button class="primary-button">Add source</button></div>
        {/if}
      </section>
    </div>
    <aside class="source-contract" data-component-id="source-access-contract"><div class="section-label">Standing access contract</div><h2>Read-only, by construction.</h2><p>The contract applies to every source. Homedex records inventory facts and exposes no connected-system write operations.</p><div class="contract-line"><b>✓</b><div><strong>Identity, state, network, and port facts</strong><small>Read and stored as versioned inventory records.</small></div></div><div class="contract-line deny"><b>×</b><div><strong><code>Config.Env</code></strong><small>Never requested, read, or stored.</small></div></div><div class="contract-line deny"><b>×</b><div><strong>Start, stop, deploy, edit, or delete</strong><small>No endpoints exist for these operations.</small></div></div><div class="contract-line deny"><b>×</b><div><strong>Agents and telemetry</strong><small>No host agent or time-series collector is installed.</small></div></div><button class="quiet-button contract-button">Review full contract</button></aside>
  </section>
</main>
