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

  let { path, inventory }: { path: string; inventory: Inventory } = $props();
  let pathname = $derived(path.split('?')[0]);
</script>

{#if pathname === '/' || pathname === '/index'}
  <IndexPage {inventory} />
{:else if pathname.startsWith('/hosts')}
  <HostsPage {path} {inventory} />
{:else if pathname.startsWith('/routes')}
  <RoutePage {path} />
{:else if pathname === '/ports'}
  <PortsPage {inventory} />
{:else if pathname === '/expiry'}
  <ExpiryPage {inventory} />
{:else if pathname === '/changes'}
  <ChangesPage {inventory} />
{:else if pathname === '/copy-my-lab' || pathname === '/settings/export'}
  <CopyLabPage />
{:else if pathname === '/sources' || pathname.startsWith('/settings/connectors')}
  <SourcesPage {inventory} />
{:else}
  <main class="page">
    <PageHead kicker="INDEX · MISSING RECORD" title="That register does not exist." copy="The address may be stale, or this version of Homedex does not expose that record." />
    <section class="empty-register"><strong>NO CURRENT RECORD</strong><span>{pathname}</span><button class="primary-button" onclick={() => navigate('/')}>Return to the index</button></section>
  </main>
{/if}
