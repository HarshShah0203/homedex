<script lang="ts">
  import { deleteConnector, scanConnector, testSavedConnector, updateConnector, type Inventory } from '../api';
  import PageHead from '../PageHead.svelte';
  import { navigate } from '../router';
  import { relativeTime } from '../time';

  let { inventory, onrefresh = async () => {} }: { inventory: Inventory; onrefresh?: () => Promise<void> } = $props();
  let pending = $state<Record<number, string>>({});
  let notices = $state<Record<number, { tone: 'ok' | 'bad'; text: string }>>({});
  let editingID = $state<number | null>(null);
  let editName = $state('');
  let editSchedule = $state(15);
  let confirmingID = $state<number | null>(null);
  let confirmTimer = 0;

  function armDelete(id: number) {
    confirmingID = id;
    window.clearTimeout(confirmTimer);
    confirmTimer = window.setTimeout(() => (confirmingID = null), 4000);
  }

  function connectorTone(connector: Inventory['connectors'][number]) {
    return ['connected', 'ok', 'success'].includes(connector.last_status.toLowerCase()) ? 'ok' : 'bad';
  }

  function stateLabel(connector: Inventory['connectors'][number]) {
    return connectorTone(connector) === 'ok' ? 'Connected' : connector.last_error?.split(':')[0] || 'Connection refused';
  }

  function setNotice(id: number, tone: 'ok' | 'bad', text: string) {
    notices = { ...notices, [id]: { tone, text } };
  }

  async function run(id: number, action: string, operation: () => Promise<string>, refresh = false) {
    pending = { ...pending, [id]: action };
    setNotice(id, 'ok', `${action}…`);
    try {
      const message = await operation();
      setNotice(id, 'ok', message);
      if (refresh) await onrefresh();
    } catch (cause) {
      setNotice(id, 'bad', cause instanceof Error ? cause.message : `${action} failed.`);
    } finally {
      const next = { ...pending };
      delete next[id];
      pending = next;
    }
  }

  function testSource(id: number) {
    return run(id, 'Testing', async () => {
      const result = await testSavedConnector(id);
      return result.status === 'ok' ? 'Connection test passed.' : result.error || 'Connection test failed.';
    });
  }

  function scanSource(id: number) {
    return run(id, 'Scanning', async () => {
      const result = await scanConnector(id);
      return `Scan ${result.scan_run_id} completed · ${result.changes} changes.`;
    }, true);
  }

  function toggleSource(connector: Inventory['connectors'][number]) {
    const enabled = !connector.enabled;
    return run(connector.id, enabled ? 'Enabling' : 'Disabling', async () => {
      const result = await updateConnector(connector.id, { enabled });
      return result.scan_error || `Source ${enabled ? 'enabled' : 'disabled'}.`;
    }, true);
  }

  function startEdit(connector: Inventory['connectors'][number]) {
    editingID = connector.id;
    editName = connector.name;
    editSchedule = connector.schedule_minutes;
  }

  function saveEdit(id: number) {
    if (!editName.trim() || editSchedule < 1) {
      setNotice(id, 'bad', 'Name and a positive schedule are required.');
      return;
    }
    return run(id, 'Saving', async () => {
      const result = await updateConnector(id, { name: editName.trim(), schedule_minutes: editSchedule });
      editingID = null;
      return result.scan_error || 'Source settings saved.';
    }, true);
  }

  async function removeSource(connector: Inventory['connectors'][number]) {
    if (confirmingID !== connector.id) {
      armDelete(connector.id);
      setNotice(connector.id, 'bad', 'Inventory history remains, but this source stops scanning. Confirm to delete.');
      return;
    }
    window.clearTimeout(confirmTimer);
    confirmingID = null;
    await run(connector.id, 'Deleting', async () => {
      await deleteConnector(connector.id);
      return 'Source deleted.';
    }, true);
  }
</script>

<main class="page">
  <PageHead title="Sources" meta={`${inventory.connectors.length} configured`}>
    {#snippet actions()}{#if !inventory.readOnly}<button class="primary-button" onclick={() => navigate('/setup')}>Add source</button>{/if}{/snippet}
  </PageHead>
  <section class="settings-layout">
    <div class="sources-register">
      <div class="toolbar sources-toolbar"><span class="toolbar-meta">{inventory.connectors.length} SOURCES · {inventory.connectors.filter((connector) => connectorTone(connector) === 'ok').length} CONNECTED</span></div>
      <section class="register" data-component-id="source-register">
        <header class="register-head source-cols"><span>Source</span><span>Endpoint</span><span>State</span><span>Indexed</span><span>Schedule</span></header>
        {#if inventory.connectors.length}
          {#each inventory.connectors as connector}
            <div class="source-record">
              <div class="register-row source-cols"><div data-label="Source"><strong>{connector.name}</strong><small>SRC-{String(connector.id).padStart(3, '0')}</small></div><div data-label="Endpoint"><code>{connector.endpoint || 'Stored in connector configuration'}</code></div><div data-label="State"><span class={`status ${connectorTone(connector)}`}>{connector.enabled ? stateLabel(connector) : 'Disabled'}</span></div><div data-label="Indexed"><strong>{connector.found || 'Inventory count unavailable'}</strong><small>updated {connector.updated_at ? relativeTime(connector.updated_at) : 'never'}</small></div><div data-label="Schedule"><span class="mono">{connector.schedule_minutes} min</span></div></div>
              {#if !inventory.readOnly}
                <div class="source-actions" aria-label={`Actions for ${connector.name}`}>
                  <button class="quiet-button" disabled={Boolean(pending[connector.id])} onclick={() => testSource(connector.id)}>Test</button>
                  <button class="quiet-button" disabled={Boolean(pending[connector.id]) || !connector.enabled} onclick={() => scanSource(connector.id)}>Scan now</button>
                  <button class="quiet-button" disabled={Boolean(pending[connector.id])} onclick={() => toggleSource(connector)}>{connector.enabled ? 'Disable' : 'Enable'}</button>
                  <button class="quiet-button" disabled={Boolean(pending[connector.id])} onclick={() => startEdit(connector)}>Edit</button>
                  <button class={confirmingID === connector.id ? 'danger-button' : 'quiet-button'} disabled={Boolean(pending[connector.id])} onclick={() => removeSource(connector)}>{confirmingID === connector.id ? 'Confirm delete' : 'Delete'}</button>
                  {#if notices[connector.id]}<span class={`status ${notices[connector.id].tone}`} role="status">{notices[connector.id].text}</span>{/if}
                </div>
                {#if editingID === connector.id}
                  <form class="source-editor" onsubmit={(event) => { event.preventDefault(); saveEdit(connector.id); }}>
                    <label>Source name <input bind:value={editName} /></label>
                    <label>Schedule, minutes <input type="number" min="1" bind:value={editSchedule} /></label>
                    <button class="primary-button" disabled={Boolean(pending[connector.id])}>Save</button>
                    <button type="button" class="quiet-button" onclick={() => (editingID = null)}>Cancel</button>
                  </form>
                {/if}
              {/if}
            </div>
          {/each}
        {:else}
          <div class="empty-register"><strong>{inventory.readOnly ? 'SOURCES HIDDEN IN SHARED VIEW' : 'NO SOURCES DECLARED'}</strong><span>{inventory.readOnly ? 'Connector configuration is outside this share token’s read scope.' : 'Add a read-only connector to begin the inventory.'}</span>{#if !inventory.readOnly}<button class="primary-button" onclick={() => navigate('/setup')}>Add source</button>{/if}</div>
        {/if}
      </section>
    </div>
    <aside class="source-contract" data-component-id="source-access-contract"><div class="section-label">Access contract</div><h2>Read-only, by construction.</h2><p>Applies to every source.</p><div class="contract-line"><b>✓</b><div><strong>Identity, state, network, and port facts</strong><small>Read and stored as versioned inventory records.</small></div></div><div class="contract-line deny"><b>×</b><div><strong><code>Config.Env</code></strong><small>Never requested, read, or stored.</small></div></div><div class="contract-line deny"><b>×</b><div><strong>Start, stop, deploy, edit, or delete</strong><small>No endpoints exist for these operations.</small></div></div><div class="contract-line deny"><b>×</b><div><strong>Agents and telemetry</strong><small>No host agent or time-series collector is installed.</small></div></div></aside>
  </section>
</main>
