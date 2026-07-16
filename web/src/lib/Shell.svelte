<script lang="ts">
  import type { Snippet } from 'svelte';
  import { Moon, Search, Sun } from 'lucide-svelte';
  import { navigate } from './router';

  let {
    path,
    paletteOpen = $bindable(false),
    theme = $bindable<'dark' | 'light'>('dark'),
    children
  }: {
    path: string;
    paletteOpen: boolean;
    theme: 'dark' | 'light';
    children: Snippet;
  } = $props();

  const navItems = [
    { label: 'Index', href: '/' },
    { label: 'Routes', href: '/routes' },
    { label: 'Ports', href: '/ports' },
    { label: 'Changes', href: '/changes' },
    { label: 'Expiry', href: '/expiry' },
    { label: 'Copy my lab', href: '/copy-my-lab' },
    { label: 'Sources', href: '/sources' }
  ];

  let pathname = $derived(path.split('?')[0]);

  function active(href: string) {
    if (href === '/') return pathname === '/' || pathname.startsWith('/hosts');
    return pathname.startsWith(href);
  }

  function follow(event: MouseEvent, href: string) {
    if (event.metaKey || event.ctrlKey || event.shiftKey || event.altKey) return;
    event.preventDefault();
    navigate(href);
  }

  function handleKeydown(event: KeyboardEvent) {
    if ((event.metaKey || event.ctrlKey) && event.key.toLowerCase() === 'k') {
      event.preventDefault();
      paletteOpen = true;
    }
  }
</script>

<svelte:window onkeydown={handleKeydown} />

<div class="app" data-theme={theme} data-component-id="homedex-catalog-shell">
  <header class="topbar" data-component-id="global-header">
    <a class="brand" href="/" aria-label="Homedex index" onclick={(event) => follow(event, '/')}>
      <span class="brand-mark" aria-hidden="true">H</span>
      <span><strong>homedex</strong><small>Address book for your lab</small></span>
    </a>
    <nav class="primary-nav" data-component-id="primary-navigation" aria-label="Primary navigation">
      {#each navItems as item}
        <a class:active={active(item.href)} href={item.href} onclick={(event) => follow(event, item.href)}>{item.label}</a>
      {/each}
    </nav>
    <div class="top-actions">
      <button class="search-trigger" aria-label="Open universal search" onclick={() => (paletteOpen = true)}>
        <Search size={14} aria-hidden="true" />
        <span>Search every record</span>
        <span class="key">⌘ K</span>
      </button>
      <div class="scan-mark"><i class="dot ok"></i><span>Current · 2m</span></div>
      <button
        class="icon-button"
        aria-label={theme === 'dark' ? 'Switch to light theme' : 'Switch to dark theme'}
        title={theme === 'dark' ? 'Use light theme' : 'Use dark theme'}
        onclick={() => (theme = theme === 'dark' ? 'light' : 'dark')}
      >
        {#if theme === 'dark'}<Sun size={15} />{:else}<Moon size={15} />{/if}
      </button>
    </div>
  </header>
  {@render children()}
</div>
