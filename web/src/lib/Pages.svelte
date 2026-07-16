<script lang="ts">
  import type { Inventory } from './api';
  import IndexPage from './pages/IndexPage.svelte';
  import HostsPage from './pages/HostsPage.svelte';
  import RoutePage from './pages/RoutePage.svelte';
  import PortsPage from './pages/PortsPage.svelte';
  import ExpiryPage from './pages/ExpiryPage.svelte';
  import ChangesPage from './pages/ChangesPage.svelte';
  import CopyLabPage from './pages/CopyLabPage.svelte';
  import SourcesPage from './pages/SourcesPage.svelte';
  import PageHead from './PageHead.svelte';
  import { navigate } from './router';

  let { path, inventory, onrefresh = async () => {} }: { path: string; inventory: Inventory; onrefresh?: () => Promise<void> } = $props();
  let pathname = $derived(path.split('?')[0]);
  let issueSummary = $derived(inventory.issues.map((issue) => `${issue.resource}: ${issue.message}`).join(' '));
</script>

{#if inventory.source === 'demo'}
  <div class="summary-line" role="status"><strong>Development demo inventory</strong><span>{inventory.error || 'The API returned no records, so local fixture data is shown only in this development build.'}</span></div>
{:else if inventory.readOnly}
  <div class="summary-line" role="status"><strong>Read-only shared inventory</strong><span>Settings and all mutation controls are unavailable.</span></div>
{:else if inventory.issues.length}
  <div class="summary-line" role="alert"><strong>{inventory.issues.length === 1 ? 'Inventory resource unavailable' : 'Partial inventory loaded'}</strong><span>{issueSummary}</span></div>
{/if}

{#if pathname === '/' || pathname === '/index'}
  <IndexPage {inventory} />
{:else if pathname.startsWith('/hosts')}
  <HostsPage {path} {inventory} />
{:else if pathname.startsWith('/routes')}
  <RoutePage {path} {inventory} />
{:else if pathname === '/ports'}
  <PortsPage {inventory} />
{:else if pathname === '/expiry'}
  <ExpiryPage {inventory} />
{:else if pathname === '/changes'}
  <ChangesPage {inventory} />
{:else if pathname === '/copy-my-lab' || pathname === '/settings/export'}
  <CopyLabPage />
{:else if pathname === '/sources' || pathname.startsWith('/settings/connectors')}
  <SourcesPage {inventory} {onrefresh} />
{:else}
  <main class="page">
    <PageHead kicker="INDEX · MISSING RECORD" title="That register does not exist." copy="The address may be stale, or this version of Homedex does not expose that record." />
    <section class="empty-register"><strong>NO CURRENT RECORD</strong><span>{pathname}</span><button class="primary-button" onclick={() => navigate('/')}>Return to the index</button></section>
  </main>
{/if}
