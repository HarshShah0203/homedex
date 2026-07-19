<script lang="ts">
  import { createConnector, deleteConnector, scanConnector, testConnector, testSavedConnector, updateConnector, type Inventory } from '../api';
  import type { ConnectorConfig, ConnectorInput } from '../types';
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

  const addKinds = [
    { kind: 'docker', label: 'Docker', name: 'Docker', schedule: 15 },
    { kind: 'traefik', label: 'Traefik', name: 'Traefik', schedule: 15 },
    { kind: 'caddy', label: 'Caddy', name: 'Caddy', schedule: 15 },
    { kind: 'npm', label: 'Nginx Proxy Manager', name: 'Nginx Proxy Manager', schedule: 15 },
    { kind: 'tlsprobe', label: 'TLS probe', name: 'TLS probe', schedule: 1440 },
    { kind: 'rdap', label: 'RDAP domains', name: 'RDAP domains', schedule: 1440 }
  ];

  let adding = $state(false);
  let addKind = $state('docker');
  let addName = $state('Docker');
  let addSchedule = $state(15);
  let addBusy = $state(false);
  let addStatus = $state<{ tone: 'ok' | 'bad'; text: string } | null>(null);
  let addNotice = $state<{ tone: 'ok' | 'bad'; text: string } | null>(null);
  let verifiedFingerprint = $state('');

  // Kind-specific field values. Shared names (url, password) are reused across
  // kinds that need them; buildConfig() only reads the fields for the active kind.
  let fEndpoint = $state('tcp://docker-socket-proxy:2375');
  let fHostName = $state('');
  let fHostAddress = $state('');
  let fUrl = $state('');
  let fUsername = $state('');
  let fPassword = $state('');
  let fEmail = $state('');
  let fTargets = $state('');
  let fTimeout = $state<number | null>(null);
  let fDomains = $state('');

  function splitLines(value: string): string[] {
    return value.split('\n').map((line) => line.trim()).filter(Boolean);
  }

  function buildConfig(): ConnectorConfig {
    switch (addKind) {
      case 'traefik':
        return { url: fUrl.trim(), username: fUsername.trim(), password: fPassword };
      case 'caddy':
        return { url: fUrl.trim() };
      case 'npm':
        return { url: fUrl.trim(), email: fEmail.trim(), password: fPassword };
      case 'tlsprobe':
        return { targets: splitLines(fTargets), ...(fTimeout && fTimeout > 0 ? { timeout_seconds: fTimeout } : {}) };
      case 'rdap':
        return { domains: splitLines(fDomains) };
      default:
        return { endpoint: fEndpoint.trim(), host_name: fHostName.trim(), host_address: fHostAddress.trim() };
    }
  }

  function addInput(): ConnectorInput {
    return { kind: addKind, name: addName.trim(), config: buildConfig(), enabled: true, schedule_minutes: addSchedule };
  }

  let addFingerprint = $derived(JSON.stringify(addInput()));
  let addVerified = $derived(verifiedFingerprint === addFingerprint);

  function toggleAdd() {
    adding = !adding;
    if (adding) applyKind(addKind, true);
  }

  function applyKind(kind: string, resetName = true) {
    addKind = kind;
    const preset = addKinds.find((item) => item.kind === kind);
    if (preset && resetName) {
      addName = preset.name;
      addSchedule = preset.schedule;
    }
    verifiedFingerprint = '';
    addStatus = null;
  }

  async function testAdd() {
    if (!addName.trim()) {
      addStatus = { tone: 'bad', text: 'A source name is required.' };
      return;
    }
    addBusy = true;
    addStatus = { tone: 'ok', text: 'Testing connection…' };
    try {
      const result = await testConnector(addInput());
      if (result.status === 'ok') {
        verifiedFingerprint = addFingerprint;
        addStatus = { tone: 'ok', text: 'Connection verified.' };
      } else {
        verifiedFingerprint = '';
        addStatus = { tone: 'bad', text: result.error || 'Connection test failed.' };
      }
    } catch (cause) {
      verifiedFingerprint = '';
      addStatus = { tone: 'bad', text: cause instanceof Error ? cause.message : 'Connection test failed.' };
    } finally {
      addBusy = false;
    }
  }

  async function saveAdd() {
    if (!addVerified) {
      addStatus = { tone: 'bad', text: 'Test the current settings before saving.' };
      return;
    }
    addBusy = true;
    addStatus = { tone: 'ok', text: 'Saving and scanning…' };
    try {
      const result = await createConnector(addInput());
      if (result.scan_error) {
        addNotice = { tone: 'bad', text: `Source added, first scan failed: ${result.scan_error}` };
      } else {
        addNotice = { tone: 'ok', text: `Source added, ${result.changes} changes recorded.` };
      }
      adding = false;
      resetAdd();
      await onrefresh();
    } catch (cause) {
      addStatus = { tone: 'bad', text: cause instanceof Error ? cause.message : 'The source could not be saved.' };
    } finally {
      addBusy = false;
    }
  }

  function cancelAdd() {
    adding = false;
    resetAdd();
  }

  function resetAdd() {
    verifiedFingerprint = '';
    addStatus = null;
    fUrl = '';
    fUsername = '';
    fPassword = '';
    fEmail = '';
    fTargets = '';
    fTimeout = null;
    fDomains = '';
    fEndpoint = 'tcp://docker-socket-proxy:2375';
    fHostName = '';
    fHostAddress = '';
  }

  function armDelete(id: number) {
    confirmingID = id;
    window.clearTimeout(confirmTimer);
    confirmTimer = window.setTimeout(() => (confirmingID = null), 4000);
  }

  function connectorTone(connector: Inventory['connectors'][number]) {
    return ['connected', 'ok', 'success'].includes(connector.last_status.toLowerCase()) ? 'ok' : 'bad';
  }

  function scheduleLabel(minutes: number) {
    return minutes % 60 === 0 && minutes >= 60 ? `${minutes / 60}h` : `${minutes} min`;
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
    {#snippet actions()}{#if !inventory.readOnly}<button class="primary-button" onclick={toggleAdd}>{adding ? 'Close add form' : 'Add source'}</button>{/if}{/snippet}
  </PageHead>
  <section class="settings-layout">
    <div class="sources-register">
      <div class="toolbar sources-toolbar"><span class="toolbar-meta">{inventory.connectors.length} SOURCES · {inventory.connectors.filter((connector) => connector.enabled && connectorTone(connector) === 'ok').length} CONNECTED</span></div>
      {#if addNotice}<span class={`status ${addNotice.tone}`} role="status">{addNotice.text}</span>{/if}
      {#if !inventory.readOnly && adding}
        <form class="source-editor" data-component-id="add-source-form" onsubmit={(event) => { event.preventDefault(); saveAdd(); }}>
          <div class="section-label">Add source</div>
          <label class="field-label">Source type
            <select bind:value={addKind} onchange={(event) => applyKind((event.currentTarget as HTMLSelectElement).value)}>
              {#each addKinds as item}<option value={item.kind}>{item.label}</option>{/each}
            </select>
          </label>
          <label class="field-label">Source name <input bind:value={addName} /></label>
          {#if addKind === 'docker'}
            <label class="field-label">Read-only endpoint <input bind:value={fEndpoint} placeholder="tcp://docker-socket-proxy:2375" /></label>
            <label class="field-label">Host name <input bind:value={fHostName} placeholder="Optional" /></label>
            <label class="field-label">Host address <input bind:value={fHostAddress} placeholder="Optional" /></label>
          {:else if addKind === 'traefik'}
            <label class="field-label">Traefik URL <input bind:value={fUrl} placeholder="https://traefik.lab.internal" /></label>
            <label class="field-label">Username <input bind:value={fUsername} placeholder="Optional" /></label>
            <label class="field-label">Password <input type="password" bind:value={fPassword} /></label>
          {:else if addKind === 'caddy'}
            <label class="field-label">Admin endpoint <input bind:value={fUrl} placeholder="http://caddy:2019" /></label>
          {:else if addKind === 'npm'}
            <label class="field-label">NPM URL <input bind:value={fUrl} placeholder="https://proxy.lab.internal" /></label>
            <label class="field-label">Read-only account <input type="email" bind:value={fEmail} /></label>
            <label class="field-label">Password <input type="password" bind:value={fPassword} /></label>
          {:else if addKind === 'tlsprobe'}
            <label class="field-label">Targets, one per line <textarea bind:value={fTargets} rows="3" placeholder="example.com:443"></textarea></label>
            <label class="field-label">Timeout, seconds <input type="number" min="1" bind:value={fTimeout} placeholder="Optional" /></label>
          {:else if addKind === 'rdap'}
            <label class="field-label">Domains, one per line <textarea bind:value={fDomains} rows="3" placeholder="example.com"></textarea></label>
          {/if}
          <label class="field-label">Schedule, minutes <input type="number" min="1" bind:value={addSchedule} /></label>
          <div class="register-row">
            <button type="button" class="quiet-button" disabled={addBusy} onclick={testAdd}>{addVerified ? 'Connection verified' : 'Test connection'}</button>
            <button class="primary-button" disabled={addBusy || !addVerified}>Save and scan</button>
            <button type="button" class="quiet-button" disabled={addBusy} onclick={cancelAdd}>Cancel</button>
            {#if addStatus}<span class={`status ${addStatus.tone}`} role="status">{addStatus.text}</span>{/if}
          </div>
          {#if addKind === 'docker'}<small class="field-help"><a href="/setup" onclick={(event) => { event.preventDefault(); navigate('/setup'); }}>Or use the guided setup</a></small>{/if}
        </form>
      {/if}
      <section class="register" data-component-id="source-register">
        <header class="register-head source-cols"><span>Source</span><span>Endpoint</span><span>State</span><span>Indexed</span><span>Schedule</span></header>
        {#if inventory.connectors.length}
          {#each inventory.connectors as connector}
            <div class="source-record">
              <div class="register-row source-cols"><div data-label="Source"><strong>{connector.name}</strong><small>SRC-{String(connector.id).padStart(3, '0')}</small></div><div data-label="Endpoint"><code>{connector.endpoint || 'Stored in connector configuration'}</code></div><div data-label="State"><span class={`status ${connector.enabled ? connectorTone(connector) : 'idle'}`}>{connector.enabled ? stateLabel(connector) : 'Disabled'}</span></div><div data-label="Indexed"><strong>{connector.found || '—'}</strong><small>updated {connector.updated_at ? relativeTime(connector.updated_at) : 'never'}</small></div><div class="num" data-label="Schedule"><span class="mono">{scheduleLabel(connector.schedule_minutes)}</span></div></div>
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
