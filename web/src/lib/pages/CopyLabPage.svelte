<script lang="ts">
  import PageHead from '../PageHead.svelte';

  const scope = [
    ['Services', '42 current records', '42', true],
    ['Hosts', 'Addresses and OS facts', '3', true],
    ['Routes', 'Public name and join outcome', '23', true],
    ['Ports', 'Published and internal declarations', '117', true],
    ['Private notes', 'Excluded by default', 'OFF', false],
    ['Raw labels', 'Secret-like labels remain masked', 'OFF', false]
  ];

  const markdown = `# Homedex lab index
Generated: 2026-07-16T10:42:18Z
Schema: homedex-context/v1

## Hosts
- gateway — 10.0.10.5 — Debian 13 — amd64
- nas-01 — 10.0.20.10 — Ubuntu 24.04 — amd64
- core-01 — 10.0.10.8 — Alpine 3.22 — arm64

## Services
- immich-server — ghcr.io/immich-app/server:v1.135.3
  host: nas-01 · internal: 2283/tcp
  route: photos.lab.example → NPM → immich-server:2283
- jellyfin — jellyfin/jellyfin:10.10.7
  host: nas-01 · published: 8096/tcp

## Unresolved joins
- old.lab.example → Traefik → 10.0.20.14:8080
  result: no current inventory record`;

  let copied = $state(false);

  async function copyMarkdown() {
    await navigator.clipboard?.writeText(markdown);
    copied = true;
    window.setTimeout(() => (copied = false), 1800);
  }
</script>

<main class="page">
  <PageHead kicker="COPY MY LAB · DETERMINISTIC CONTEXT" title="A safe, exact copy of the index." copy="Preview the Markdown exactly as it will be copied. Ordering, schema, redactions, and hash are part of the receipt.">
    {#snippet actions()}<button class="primary-button" onclick={copyMarkdown}>{copied ? 'Markdown copied' : 'Copy Markdown'}</button>{/snippet}
  </PageHead>
  <section class="export-layout" data-component-id="copy-my-lab-workbench">
    <aside class="scope-register">
      <div class="scope-section"><div class="section-label">Scope register</div><h3>Include inventory facts</h3><p>Current records only. No connector credentials or telemetry exist in this export.</p></div>
      {#each scope as item}<div class="check-row"><span class:on={Boolean(item[3])} class="checkbox">{item[3] ? '✓' : ''}</span><div><strong>{item[0]}</strong><small>{item[1]}</small></div><code>{item[2]}</code></div>{/each}
      <div class="scope-section"><div class="section-label">Size budget</div><h3>18.4 KB of 64 KB</h3><p>Deterministic ordering: object type, then record ID.</p></div>
      <button class="primary-button copy-exact" onclick={copyMarkdown}>{copied ? 'Copied to clipboard' : 'Copy exact Markdown'}</button>
    </aside>
    <article class="preview">
      <header class="preview-head"><strong>Exact Markdown preview</strong><span>HMX-CONTEXT-042.MD · 18.4 KB</span></header>
      <div class="markdown-paper"><h2>Homedex lab index</h2><div class="entity-meta">SCHEMA HOMEDEX-CONTEXT/V1 · SCAN 042</div><pre>{markdown}</pre></div>
      <section class="safety-receipt" data-component-id="export-safety-receipt"><div class="section-label">Mandatory safety receipt</div><div class="receipt-line"><span>Connector credentials</span><span>EXCLUDED</span></div><div class="receipt-line"><span>Private notes</span><span>EXCLUDED BY DEFAULT</span></div><div class="receipt-line"><span>Raw labels</span><span>EXCLUDED BY DEFAULT</span></div><div class="receipt-line"><span>Secret-like labels</span><span>MASKED</span></div><div class="receipt-line"><span><code>Config.Env</code></span><span>NEVER INGESTED</span></div><div class="receipt-line"><span>SHA-256</span><span>6A9D 4E3B 78C1 … 2F10</span></div></section>
    </article>
  </section>
</main>
