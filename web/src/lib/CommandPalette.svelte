<script lang="ts">
  import { tick } from 'svelte';
  import { Search, X } from 'lucide-svelte';
  import type { Inventory } from './api';
  import { navigate } from './router';
  import { buildSearchGroups, type SearchResult } from './search';

  let { open = $bindable(false), inventory }: { open: boolean; inventory: Inventory } = $props();
  let query = $state('immich');
  let input = $state<HTMLInputElement>();
  let groups = $derived(buildSearchGroups(inventory, query));
  let resultCount = $derived(groups.reduce((total, group) => total + group.results.length, 0));

  $effect(() => {
    if (open) {
      if (!query) query = 'immich';
      tick().then(() => input?.focus());
    }
  });

  function choose(result: SearchResult) {
    open = false;
    if (result.href) navigate(result.href);
  }

  function handleKeydown(event: KeyboardEvent) {
    if (open && event.key === 'Escape') {
      event.preventDefault();
      open = false;
    }
  }
</script>

<svelte:window onkeydown={handleKeydown} />

{#if open}
  <div class="search-overlay" data-component-id="command-search-overlay" role="presentation" onclick={(event) => { if (event.target === event.currentTarget) open = false; }}>
    <div class="search-dialog" role="dialog" aria-modal="true" aria-label="Search every record">
      <label class="search-entry">
        <Search size={21} aria-hidden="true" />
        <input bind:this={input} bind:value={query} aria-label="Search query" autocomplete="off" />
        <button class="search-close" aria-label="Close search" onclick={() => (open = false)}><X size={16} /><span>ESC</span></button>
      </label>
      <div class="search-summary"><strong>{resultCount} result{resultCount === 1 ? '' : 's'}</strong> across the current index · exact and connected-record matches</div>
      {#if groups.length}
        {#each groups as group}
          <section class="search-group">
            <header><strong>{group.label}</strong><small>{group.results.length} RESULT{group.results.length === 1 ? '' : 'S'}</small></header>
            <div class="search-results">
              {#each group.results as result, index}
                <button class:selected={group.label === groups[0].label && index === 0} class="search-result" aria-label={`${group.label}: ${result.title}`} onclick={() => choose(result)}>
                  <div><strong>{result.title}</strong><small>{result.meta}</small></div>
                  <div class="match-reason"><b>MATCH</b> · {result.reason}</div>
                  <span class:bad={result.state === 'broken'} class="status ok">{result.state}</span>
                </button>
              {/each}
            </div>
          </section>
        {/each}
      {:else}
        <div class="empty-register compact"><strong>No records match “{query}”.</strong><span>Try a service, route, port, or host name.</span></div>
      {/if}
      <footer class="search-footer"><span>↑ ↓ SELECT</span><span>↵ OPEN</span><span>⌘ ↵ OPEN INSPECTOR</span><span>INDEX UPDATED 2M AGO</span></footer>
    </div>
  </div>
{/if}
