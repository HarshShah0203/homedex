<script lang="ts">
  import { onDestroy } from 'svelte';
  import { Eye, EyeOff } from 'lucide-svelte';
  import { createConnector, loadConnectorScans, scanConnector, testConnector } from './api';
  import type { ConnectorInput, ScanEvent } from './types';
  import { navigate } from './router';

  let {
    theme = $bindable<'dark' | 'light'>('light'),
    needsAdmin = false,
    oncomplete = async () => {}
  }: { theme: 'dark' | 'light'; needsAdmin?: boolean; oncomplete?: () => Promise<void> } = $props();

  let adminRequired = $state(false);
  let password = $state('');
  let visible = $state(false);
  let busy = $state(false);
  let error = $state('');
  let dockerName = $state('Local Docker');
  let dockerEndpoint = $state('tcp://docker-socket-proxy:2375');
  let dockerHostName = $state('');
  let dockerHostAddress = $state('');
  let validatedDocker = $state('');
  let dockerConnectorID = $state<number | null>(null);
  let scanRunID = $state<number | null>(null);
  let scanStats = $state<Record<string, number>>({});
  let changes = $state(0);
  let scanComplete = $state(false);
  let includeProxy = $state(false);
  let proxyName = $state('Nginx Proxy Manager');
  let proxyURL = $state('');
  let proxyEmail = $state('');
  let proxyPassword = $state('');
  let validatedProxy = $state('');
  let proxySaved = $state(false);
  let transcript = $state<Array<{ at: string; event: ScanEvent }>>([]);
  let events: EventSource | null = null;
  let dockerFingerprint = $derived(JSON.stringify(dockerInput()));
  let proxyFingerprint = $derived(JSON.stringify(proxyInput()));
  let canReview = $derived(scanComplete && (!includeProxy || proxySaved));
  let statsText = $derived(Object.entries(scanStats).filter(([, count]) => count > 0).map(([name, count]) => `${count} ${name.toUpperCase()}`).join(' · ') || `${changes} CHANGES RECORDED`);

  $effect(() => {
    if (needsAdmin) adminRequired = true;
  });

  onDestroy(() => events?.close());

  function dockerInput(): ConnectorInput {
    return {
      kind: 'docker',
      name: dockerName.trim(),
      config: {
        endpoint: dockerEndpoint.trim(),
        host_name: dockerHostName.trim(),
        host_address: dockerHostAddress.trim()
      },
      enabled: true,
      schedule_minutes: 15
    };
  }

  function proxyInput(): ConnectorInput {
    return {
      kind: 'npm',
      name: proxyName.trim(),
      config: { url: proxyURL.trim(), email: proxyEmail.trim(), password: proxyPassword },
      enabled: true,
      schedule_minutes: 15
    };
  }

  function watchScanEvents() {
    if (events) return;
    events = new EventSource('/api/events');
    events.addEventListener('update', (message) => {
      try {
        const event = JSON.parse((message as MessageEvent<string>).data) as ScanEvent;
        if (dockerConnectorID && event.connector_id && event.connector_id !== dockerConnectorID) return;
        transcript = [...transcript, { at: new Date().toLocaleTimeString([], { hour: '2-digit', minute: '2-digit', second: '2-digit', hour12: false }), event }];
        if (event.stats) scanStats = event.stats;
        if (event.changes !== undefined) changes = event.changes;
        if (event.scan_run_id) scanRunID = event.scan_run_id;
        if (event.type === 'scan.complete') scanComplete = true;
        if (event.type === 'scan.failed') error = event.error || 'The source scan failed.';
      } catch {
        // Ignore malformed stream messages; the mutation response and scan
        // history remain authoritative.
      }
    });
  }

  async function createAdmin() {
    if (password.length < 12) {
      error = 'Use at least 12 characters.';
      return;
    }
    busy = true;
    error = '';
    try {
      const response = await fetch('/api/setup', {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({ password })
      });
      if (!response.ok && response.status !== 409) throw new Error((await response.text()).trim());
      if (response.ok) {
        const session = await response.json();
        if (session.csrf) sessionStorage.setItem('homedex-csrf', session.csrf);
      }
      adminRequired = false;
    } catch (cause) {
      error = cause instanceof Error ? cause.message : 'Could not create the admin account.';
    } finally {
      busy = false;
    }
  }

  async function validateDocker() {
    if (!dockerName.trim() || !dockerEndpoint.trim()) {
      error = 'Source name and Docker endpoint are required.';
      return;
    }
    busy = true;
    error = '';
    try {
      await testConnector(dockerInput());
      validatedDocker = dockerFingerprint;
    } catch (cause) {
      validatedDocker = '';
      error = cause instanceof Error ? cause.message : 'Docker validation failed.';
    } finally {
      busy = false;
    }
  }

  async function saveDocker() {
    if (validatedDocker !== dockerFingerprint) {
      error = 'Test the current Docker settings before saving.';
      return;
    }
    busy = true;
    error = '';
    transcript = [];
    scanComplete = false;
    watchScanEvents();
    try {
      const result = await createConnector(dockerInput());
      dockerConnectorID = result.connector.id;
      scanRunID = result.scan_run_id || null;
      changes = result.changes;
      if (result.scan_error) throw new Error(result.scan_error);
      const runs = await loadConnectorScans(result.connector.id);
      const latest = runs.find((run) => run.id === result.scan_run_id) ?? runs[0];
      if (latest) {
        scanStats = latest.stats;
        scanComplete = latest.status === 'success';
        if (latest.error) error = latest.error;
      } else {
        scanComplete = result.scan_run_id > 0;
      }
      if (!transcript.length) transcript = [{ at: new Date().toLocaleTimeString([], { hour: '2-digit', minute: '2-digit', second: '2-digit', hour12: false }), event: { type: scanComplete ? 'scan.complete' : 'scan.failed', connector_id: result.connector.id, scan_run_id: result.scan_run_id, changes: result.changes, stats: scanStats, message: scanComplete ? 'Inventory scan complete' : 'Inventory scan did not complete', progress: scanComplete ? 100 : 0 } }];
    } catch (cause) {
      error = cause instanceof Error ? cause.message : 'The Docker source could not be saved and scanned.';
    } finally {
      busy = false;
    }
  }

  async function runAgain() {
    if (!dockerConnectorID) return;
    busy = true;
    error = '';
    scanComplete = false;
    transcript = [];
    watchScanEvents();
    try {
      const result = await scanConnector(dockerConnectorID);
      scanRunID = result.scan_run_id;
      changes = result.changes;
      const runs = await loadConnectorScans(dockerConnectorID);
      scanStats = runs.find((run) => run.id === result.scan_run_id)?.stats ?? {};
      scanComplete = result.status === 'success';
    } catch (cause) {
      error = cause instanceof Error ? cause.message : 'The source scan failed.';
    } finally {
      busy = false;
    }
  }

  async function validateProxy() {
    busy = true;
    error = '';
    try {
      await testConnector(proxyInput());
      validatedProxy = proxyFingerprint;
    } catch (cause) {
      validatedProxy = '';
      error = cause instanceof Error ? cause.message : 'Proxy validation failed.';
    } finally {
      busy = false;
    }
  }

  async function saveProxy() {
    if (validatedProxy !== proxyFingerprint) {
      error = 'Test the current proxy settings before saving.';
      return;
    }
    busy = true;
    error = '';
    watchScanEvents();
    try {
      const result = await createConnector(proxyInput());
      if (result.scan_error) throw new Error(result.scan_error);
      proxySaved = result.scan_run_id > 0;
    } catch (cause) {
      error = cause instanceof Error ? cause.message : 'The proxy source could not be saved.';
    } finally {
      busy = false;
    }
  }

  async function finish() {
    if (!canReview) return;
    await oncomplete();
    navigate('/');
  }
</script>

<div class="setup-shell" data-theme={theme} data-component-id="onboarding-read-contract">
  <header class="setup-top">
    <a class="brand" href="/" aria-label="Homedex setup"><span class="brand-mark" aria-hidden="true"><svg width="24" height="24" viewBox="0 0 24 24" fill="none" xmlns="http://www.w3.org/2000/svg"><path d="M3 8.5V5a1 1 0 0 1 1-1h6.2a1 1 0 0 1 .9.55L12 6h8a1 1 0 0 1 1 1v12a1 1 0 0 1-1 1H4a1 1 0 0 1-1-1V8.5Z" fill="currentColor"/><path d="M6.5 11.5h11M6.5 14.5h11M6.5 17.5h7" stroke="var(--bg)" stroke-width="1.4" stroke-linecap="round"/></svg></span><span><strong>homedex</strong><small>First inventory</small></span></a>
    <span class="setup-local">LOCAL · READ ONLY · NO AGENTS</span>
  </header>
  <nav class="setup-steps" aria-label="Setup progress">
    <div class="setup-step done"><b>✓</b>Trust</div>
    <div class:active={adminRequired} class:done={!adminRequired} class="setup-step"><b>{adminRequired ? '02' : '✓'}</b>Admin</div>
    <div class:active={!adminRequired && !dockerConnectorID} class:done={Boolean(dockerConnectorID)} class="setup-step"><b>{dockerConnectorID ? '✓' : '03'}</b>Sources</div>
    <div class:active={!adminRequired && Boolean(dockerConnectorID) && !scanComplete} class:done={scanComplete} class="setup-step"><b>{scanComplete ? '✓' : '04'}</b>First scan</div>
    <div class:active={canReview} class="setup-step"><b>05</b>Review</div>
  </nav>

  {#if adminRequired}
    <main class="setup-page admin-setup">
      <header class="setup-heading"><div class="kicker">ADMIN · LOCAL CREDENTIAL</div><h1>Admin password</h1><p>One administrator password protects the index. No recovery email or cloud account is created.</p></header>
      <section class="admin-register">
        <div><div class="section-label">Declaration of storage</div><h2>Password stays on this instance.</h2><p>Only an Argon2id hash is retained. The browser receives a local HttpOnly session cookie.</p><div class="contract-line"><b>✓</b><div><strong>Local authentication only</strong><small>No identity provider or external account is required.</small></div></div><div class="contract-line deny"><b>×</b><div><strong>Recovery email and telemetry</strong><small>Neither is requested or stored.</small></div></div></div>
        <form onsubmit={(event) => { event.preventDefault(); createAdmin(); }}><label class="field-label" for="setup-password">Administrator password</label><div class="password-entry"><input id="setup-password" bind:value={password} type={visible ? 'text' : 'password'} autocomplete="new-password" placeholder="At least 12 characters" /><button type="button" class="icon-button" aria-label="Show password" onclick={() => (visible = !visible)}>{#if visible}<EyeOff size={15} />{:else}<Eye size={15} />{/if}</button></div><small class="field-help">Use 12 or more characters. A password manager is recommended.</small>{#if error}<p class="field-error" role="alert">{error}</p>{/if}<button class="primary-button admin-submit" disabled={busy}>{busy ? 'Creating account…' : 'Create account and continue'}</button></form>
      </section>
    </main>
  {:else}
    <main class="setup-page">
      <header class="setup-heading"><div class="kicker">FIRST SOURCE · {scanRunID ? `SCAN ${String(scanRunID).padStart(4, '0')}` : 'NOT YET SCANNED'}</div><h1>First source</h1><p>Read-only Docker metadata, recorded as the first scan runs.</p></header>
      <div class="setup-grid">
        <section class="declaration" data-component-id="explicit-read-contract">
          <header class="panel-heading"><div><div class="section-label">Declaration of access</div><h2>Docker metadata, read only</h2></div><code>{dockerConnectorID ? `SRC-${String(dockerConnectorID).padStart(3, '0')}` : 'UNSAVED'}</code></header>
          <div class="access-row allow"><b>✓</b><div><strong>Container identity and state</strong><span>Name, image, stack labels, health, and restart policy.</span></div></div><div class="access-row allow"><b>✓</b><div><strong>Network and port declarations</strong><span>Bindings, container IPs, aliases, and Docker networks.</span></div></div><div class="access-row deny"><b>×</b><div><strong>Environment variables</strong><span><code>Config.Env</code> is never read or stored.</span></div></div><div class="access-row deny"><b>×</b><div><strong>Write operations</strong><span>No start, stop, deploy, edit, or delete endpoints exist.</span></div></div>
          <div class="proxy-path"><div class="section-label">Docker source</div><label class="field-label" for="docker-name">Source name</label><input id="docker-name" bind:value={dockerName} disabled={Boolean(dockerConnectorID)} /><label class="field-label" for="docker-endpoint">Read-only endpoint</label><input id="docker-endpoint" bind:value={dockerEndpoint} disabled={Boolean(dockerConnectorID)} placeholder="tcp://docker-socket-proxy:2375" /><small class="field-help">Compose default. Native binary: unix:///var/run/docker.sock</small><label class="field-label" for="docker-host-name">Host name override</label><input id="docker-host-name" bind:value={dockerHostName} disabled={Boolean(dockerConnectorID)} placeholder="Optional" /><label class="field-label" for="docker-host-address">Host address</label><input id="docker-host-address" bind:value={dockerHostAddress} disabled={Boolean(dockerConnectorID)} placeholder="Optional" />{#if !dockerConnectorID}<div class="row"><button class="quiet-button" disabled={busy} onclick={validateDocker}>{validatedDocker === dockerFingerprint ? 'Connection verified' : 'Test connection'}</button><button class="primary-button" disabled={busy || validatedDocker !== dockerFingerprint} onclick={saveDocker}>{busy ? 'Saving and scanning…' : 'Save and run first scan'}</button></div>{:else}<div class="row"><span class="status ok">Source saved</span><button class="quiet-button" disabled={busy} onclick={runAgain}>{busy ? 'Scanning…' : 'Run scan again'}</button></div>{/if}</div>
          {#if scanComplete}<div class="proxy-path"><label><input type="checkbox" bind:checked={includeProxy} /> Add Nginx Proxy Manager now (optional)</label>{#if includeProxy && !proxySaved}<label class="field-label" for="proxy-name">Source name</label><input id="proxy-name" bind:value={proxyName} /><label class="field-label" for="proxy-url">NPM URL</label><input id="proxy-url" bind:value={proxyURL} placeholder="https://proxy.lab.internal" /><label class="field-label" for="proxy-email">Read-only account</label><input id="proxy-email" type="email" bind:value={proxyEmail} /><label class="field-label" for="proxy-password">Password</label><input id="proxy-password" type="password" bind:value={proxyPassword} /><div class="row"><button class="quiet-button" disabled={busy} onclick={validateProxy}>{validatedProxy === proxyFingerprint ? 'Proxy verified' : 'Test proxy'}</button><button class="primary-button" disabled={busy || validatedProxy !== proxyFingerprint} onclick={saveProxy}>Save and scan proxy</button></div>{:else if proxySaved}<span class="status ok">Proxy saved and scanned</span>{/if}</div>{/if}
          {#if error}<p class="field-error" role="alert">{error}</p>{/if}
        </section>
        <section class="scan-transcript" data-component-id="first-scan-transcript">
          <header class="panel-heading"><div><div class="section-label">First-scan transcript</div><h2>Facts recorded as they arrive</h2></div><code>{busy ? 'LIVE' : scanComplete ? 'COMPLETE' : 'WAITING'}</code></header>
          {#if transcript.length}{#each transcript as row}<div class="transcript-row"><time>{row.at}</time><div><strong>{row.event.phase || row.event.type}</strong><span>{row.event.message || row.event.error || `${row.event.changes ?? 0} changes`}</span></div><b class:review={row.event.type === 'scan.failed'}>{row.event.type === 'scan.failed' ? 'REVIEW' : `${row.event.progress ?? 100}%`}</b></div>{/each}{:else}<div class="empty-register"><strong>NO SCAN STARTED</strong><span>Verify and save the source to open the live event stream.</span></div>{/if}
          {#if scanComplete}<div class="scan-complete"><span class="seal">{scanRunID ?? '✓'}</span><div><strong>Inventory committed</strong><span>{statsText}</span></div><button class="primary-button" data-component-id="review-first-index" disabled={!canReview} onclick={finish}>Review the index</button></div>{/if}
        </section>
      </div>
    </main>
  {/if}
</div>
