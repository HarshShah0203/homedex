<script lang="ts">
  import { onMount } from 'svelte';
  import { contextLimit, loadContextExport } from '../api';
  import type { ContextExport } from '../types';
  import PageHead from '../PageHead.svelte';

  type ScopeItem = [string, string, string, boolean];

  let context = $state<ContextExport | null>(null);
  let error = $state('');
  let copied = $state(false);
  let omitted = $derived(context ? Object.values(context.truncation).reduce((sum, count) => sum + count, 0) : 0);
  let scope = $derived<ScopeItem[]>([
    ['Services', 'Current records', context ? String(context.counts.services) : '—', true],
    ['Hosts', 'Addresses and OS facts', context ? String(context.counts.hosts) : '—', true],
    ['Routes', 'Public name and join outcome', context ? String(context.counts.routes) : '—', true],
    ['Ports', 'Published and internal declarations', context ? String(context.counts.ports) : '—', true],
    ['Private notes', 'Excluded by default', 'OFF', false],
    ['Raw labels', 'Secret-like labels remain masked', 'OFF', false]
  ]);

  onMount(() => {
    loadContextExport().then((value) => (context = value)).catch((reason) => {
      error = reason instanceof Error ? reason.message : 'The context export could not be loaded.';
    });
  });

  async function copyMarkdown() {
    if (!context) return;
    await navigator.clipboard?.writeText(context.markdown);
    copied = true;
    window.setTimeout(() => (copied = false), 1800);
  }
</script>

<main class="page">
  <PageHead kicker="Export · Context" title="Copy my lab" copy="A redacted snapshot of your lab, ready to paste into an AI assistant or wiki.">
    {#snippet actions()}<button class="primary-button" disabled={!context} onclick={copyMarkdown}>{copied ? 'Markdown copied' : 'Copy Markdown'}</button>{/snippet}
  </PageHead>
  <section class="export-layout" data-component-id="copy-my-lab-workbench">
    <aside class="scope-register">
      <div class="scope-section"><div class="section-label">Scope register</div><h3>Include inventory facts</h3><p>Current records only. No connector credentials or telemetry exist in this export.</p></div>
      {#each scope as item}<div class="check-row"><span class:on={item[3]} class="checkbox">{item[3] ? '✓' : ''}</span><div><strong>{item[0]}</strong><small>{item[1]}</small></div><code>{item[2]}</code></div>{/each}
      <div class="scope-section"><div class="section-label">Size budget</div><h3>{context?.size ?? '—'} of {contextLimit}</h3><p>{omitted ? `${omitted} records omitted to stay inside the budget.` : 'Deterministic ordering: object type, then record ID.'}</p></div>
      <button class="primary-button copy-exact" disabled={!context} onclick={copyMarkdown}>{copied ? 'Copied to clipboard' : 'Copy exact Markdown'}</button>
    </aside>
    <article class="preview">
      <header class="preview-head"><strong>Exact Markdown preview</strong><span>{context ? `${context.filename.toUpperCase()} · ${context.size}` : 'LOADING BACKEND CONTEXT'}</span></header>
      {#if error}
        <div class="empty-register"><strong>CONTEXT EXPORT UNAVAILABLE</strong><span>{error}</span></div>
      {:else}
        <div class="markdown-paper"><h2>{context?.title ?? 'Loading context…'}</h2><div class="entity-meta">SCHEMA {context?.schema.toUpperCase() ?? 'PENDING'}</div><pre>{context?.markdown ?? 'Loading exact Markdown from Homedex…'}</pre></div>
      {/if}
      <section class="safety-receipt" data-component-id="export-safety-receipt"><div class="section-label">Mandatory safety receipt</div><div class="receipt-line"><span>Connector credentials</span><span>EXCLUDED</span></div><div class="receipt-line"><span>Private notes</span><span>EXCLUDED BY DEFAULT</span></div><div class="receipt-line"><span>Raw labels</span><span>EXCLUDED BY DEFAULT</span></div><div class="receipt-line"><span>Secret-like labels</span><span>MASKED</span></div><div class="receipt-line"><span><code>Config.Env</code></span><span>NEVER INGESTED</span></div><div class="receipt-line"><span>SHA-256</span><span>{context?.shortSha256 ?? 'PENDING'}</span></div></section>
    </article>
  </section>
</main>
