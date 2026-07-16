<script lang="ts">
  import { Eye, EyeOff } from 'lucide-svelte';
  import { navigate } from './router';

  let {
    theme = $bindable<'dark' | 'light'>('dark'),
    needsAdmin = false,
    oncomplete = async () => {}
  }: { theme: 'dark' | 'light'; needsAdmin?: boolean; oncomplete?: () => Promise<void> } = $props();

  let adminRequired = $state(false);
  let password = $state('');
  let visible = $state(false);
  let busy = $state(false);
  let error = $state('');

  $effect(() => {
    if (needsAdmin) adminRequired = true;
  });

  const transcript = [
    ['10:42:01', 'Docker · nas-01', 'Host facts recorded', 'PASS', ''],
    ['10:42:02', 'Container inspect', '19 services declared', 'PASS', ''],
    ['10:42:04', 'Network join', '54 ports · 7 networks', 'PASS', ''],
    ['10:42:06', 'Nginx Proxy Manager', '16 route declarations', 'PASS', ''],
    ['10:42:07', 'Route resolution', '15 resolved · 1 broken', 'REVIEW', 'review'],
    ['10:42:09', 'TLS probe', '8 certificates recorded', 'PASS', ''],
    ['10:42:10', 'Inventory commit', 'Scan 0001 sealed', 'PASS', '']
  ];

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
      if (!response.ok && response.status !== 409) throw new Error(await response.text());
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

  async function finish() {
    await oncomplete();
    navigate('/');
  }
</script>

<div class="setup-shell" data-theme={theme} data-component-id="onboarding-read-contract">
  <header class="setup-top">
    <a class="brand" href="/" aria-label="Homedex setup">
      <span class="brand-mark" aria-hidden="true">H</span>
      <span><strong>homedex</strong><small>Initial inventory</small></span>
    </a>
    <span class="setup-local">LOCAL · READ ONLY · NO AGENTS</span>
  </header>
  <nav class="setup-steps" aria-label="Setup progress">
    <div class="setup-step done"><b>✓</b>Trust</div>
    <div class:active={adminRequired} class:done={!adminRequired} class="setup-step"><b>{adminRequired ? '02' : '✓'}</b>Admin</div>
    <div class="setup-step done"><b>✓</b>Sources</div>
    <div class:active={!adminRequired} class="setup-step"><b>04</b>First scan</div>
    <div class="setup-step"><b>05</b>Review</div>
  </nav>

  {#if adminRequired}
    <main class="setup-page admin-setup">
      <header class="setup-heading">
        <div class="kicker">ADMIN · LOCAL CREDENTIAL</div>
        <h1>Secure this local address book.</h1>
        <p>One administrator password protects the index. No recovery email or cloud account is created.</p>
      </header>
      <section class="admin-register">
        <div>
          <div class="section-label">Declaration of storage</div>
          <h2>Password stays on this instance.</h2>
          <p>Only an Argon2id hash is retained. The browser receives a local HttpOnly session cookie.</p>
          <div class="contract-line"><b>✓</b><div><strong>Local authentication only</strong><small>No identity provider or external account is required.</small></div></div>
          <div class="contract-line deny"><b>×</b><div><strong>Recovery email and telemetry</strong><small>Neither is requested or stored.</small></div></div>
        </div>
        <form onsubmit={(event) => { event.preventDefault(); createAdmin(); }}>
          <label class="field-label" for="setup-password">Administrator password</label>
          <div class="password-entry">
            <input id="setup-password" bind:value={password} type={visible ? 'text' : 'password'} autocomplete="new-password" placeholder="At least 12 characters" />
            <button type="button" class="icon-button" aria-label="Show password" onclick={() => (visible = !visible)}>{#if visible}<EyeOff size={15} />{:else}<Eye size={15} />{/if}</button>
          </div>
          <small class="field-help">Use 12 or more characters. A password manager is recommended.</small>
          {#if error}<p class="field-error" role="alert">{error}</p>{/if}
          <button class="primary-button admin-submit" disabled={busy}>{busy ? 'Creating account…' : 'Create account and continue'}</button>
        </form>
      </section>
    </main>
  {:else}
    <main class="setup-page">
      <header class="setup-heading">
        <div class="kicker">FIRST SOURCE · SCAN 0001</div>
        <h1>See exactly what Homedex reads.</h1>
        <p>The source contract remains beside the first-scan receipt, so discovery never outruns consent.</p>
      </header>
      <div class="setup-grid">
        <section class="declaration" data-component-id="explicit-read-contract">
          <header class="panel-heading"><div><div class="section-label">Declaration of access</div><h2>Docker metadata, read only</h2></div><code>HMX-SRC-001</code></header>
          <div class="access-row allow"><b>✓</b><div><strong>Container identity and state</strong><span>Name, image, stack labels, health, and restart policy.</span></div></div>
          <div class="access-row allow"><b>✓</b><div><strong>Network and port declarations</strong><span>Bindings, container IPs, aliases, and Docker networks.</span></div></div>
          <div class="access-row deny"><b>×</b><div><strong>Environment variables</strong><span><code>Config.Env</code> is never read or stored.</span></div></div>
          <div class="access-row deny"><b>×</b><div><strong>Write operations</strong><span>No start, stop, deploy, edit, or delete endpoints exist.</span></div></div>
          <div class="proxy-path"><div class="section-label">Recommended access path</div><strong>docker-socket-proxy</strong><p>ENABLE ONLY: CONTAINERS · INFO · NETWORKS · IMAGES · VERSION</p></div>
        </section>
        <section class="scan-transcript" data-component-id="first-scan-transcript">
          <header class="panel-heading"><div><div class="section-label">First-scan transcript</div><h2>Facts recorded as they arrive</h2></div><code>9.4 SECONDS</code></header>
          {#each transcript as row}
            <div class="transcript-row"><time>{row[0]}</time><div><strong>{row[1]}</strong><span>{row[2]}</span></div><b class:review={row[4] === 'review'}>{row[3]}</b></div>
          {/each}
          <div class="scan-complete"><span class="seal">01</span><div><strong>Inventory committed</strong><span>3 HOSTS · 42 SERVICES · 117 PORTS · 23 ROUTES</span></div><button class="primary-button" data-component-id="review-first-index" onclick={finish}>Review the index</button></div>
        </section>
      </div>
    </main>
  {/if}
</div>
