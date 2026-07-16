<script lang="ts">
  import { onMount } from 'svelte';
  import Shell from './lib/Shell.svelte';
  import Pages from './lib/Pages.svelte';
  import Setup from './lib/Setup.svelte';
  import Login from './lib/Login.svelte';
  import CommandPalette from './lib/CommandPalette.svelte';
  import { createEmptyInventory, getSetupStatus, loadInventory, type Inventory } from './lib/api';
  import { navigate, route } from './lib/router';

  let inventory = $state<Inventory>(createEmptyInventory());
  let loading = $state(true);
  let paletteOpen = $state(false);
  let theme = $state<'dark' | 'light'>('dark');
  let authRequired = $state(false);
  let needsAdmin = $state(false);
  let pathname = $derived($route.split('?')[0]);

  async function refresh() {
    loading = true;
    inventory = await loadInventory();
    authRequired = inventory.issues.some((issue) => issue.kind === 'unauthorized');
    loading = false;
  }

  onMount(async () => {
    theme = localStorage.getItem('homedex-theme') === 'light' ? 'light' : 'dark';
    try {
      const status = await getSetupStatus();
      needsAdmin = !status.configured && !status.auth_disabled;
      if (needsAdmin && pathname !== '/setup') {
        navigate('/setup');
        loading = false;
        return;
      }
    } catch {
      // Older servers may not expose setup status. Inventory loading still
      // provides the authoritative authentication state in that case.
    }
    await refresh();
    const hasInventory = inventory.services.length + inventory.hosts.length + inventory.ports.length + inventory.routes.length > 0;
    if (!authRequired && !inventory.readOnly && !hasInventory && !inventory.connectors.length && !inventory.issues.length && pathname !== '/setup') navigate('/setup');
  });

  $effect(() => {
    if (typeof localStorage !== 'undefined') localStorage.setItem('homedex-theme', theme);
  });

  $effect(() => {
    document.documentElement.dataset.theme = theme;
  });
</script>

{#if pathname === '/setup'}
  <Setup bind:theme {needsAdmin} oncomplete={refresh} />
{:else if authRequired}
  <Login onlogin={refresh} />
{:else}
  <Shell path={$route} bind:paletteOpen bind:theme>
    {#if loading}
      <main class="page loading-page" aria-label="Loading inventory">
        <div class="loading-rule"></div>
        <div class="loading-rule short"></div>
        <div class="loading-register">
          {#each [1, 2, 3, 4, 5] as row}
            <div style={`--delay:${row * 45}ms`}></div>
          {/each}
        </div>
      </main>
    {:else}
      <Pages path={$route} {inventory} onrefresh={refresh} />
    {/if}
  </Shell>
  <CommandPalette bind:open={paletteOpen} {inventory} />
{/if}
