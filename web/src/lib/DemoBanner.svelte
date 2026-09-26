<script lang="ts">
  import { onMount } from 'svelte';
  import { X } from 'lucide-svelte';
  import { DEMO_REFUSED_EVENT, INSTALL_URL, REPO_URL } from './demoMode';

  // Shown only in the static live demo build. The strip says what the visitor
  // is looking at; the notice explains a refused action where they took it.
  let refused = $state(false);
  let timer = 0;

  onMount(() => {
    const show = () => {
      refused = true;
      window.clearTimeout(timer);
      timer = window.setTimeout(() => (refused = false), 8000);
    };
    window.addEventListener(DEMO_REFUSED_EVENT, show);
    return () => {
      window.removeEventListener(DEMO_REFUSED_EVENT, show);
      window.clearTimeout(timer);
    };
  });
</script>

<div class="demo-banner" data-component-id="live-demo-banner" role="note" aria-label="About this live demo">
  <span class="demo-label">Live demo with fabricated data</span>
  <span class="demo-copy">Every record is made up and nothing you change is saved.</span>
  <span class="demo-links">
    <a href={INSTALL_URL}>Install Homedex</a>
    <a class="demo-secondary" href={REPO_URL}>Source on GitHub</a>
  </span>
</div>

{#if refused}
  <div class="demo-notice" data-component-id="live-demo-notice" role="status">
    <div>
      <strong>Nothing is saved in the live demo.</strong>
      <p>There is no server behind this page to test a connection, run a scan, or store a change. Install Homedex to try it on your own lab; it only ever reads.</p>
    </div>
    <a class="primary-button" href={INSTALL_URL}>Install Homedex</a>
    <button class="icon-button" aria-label="Dismiss" onclick={() => (refused = false)}><X size={15} /></button>
  </div>
{/if}

<style>
  .demo-banner {
    min-height: 32px;
    padding: 6px 24px;
    display: flex;
    align-items: center;
    gap: 14px;
    border-bottom: 1px solid var(--border);
    background: var(--strong);
    font-size: 11px;
    color: var(--muted);
  }
  .demo-label {
    font: 700 10px ui-monospace, monospace;
    letter-spacing: 0.78px;
    text-transform: uppercase;
    color: var(--accent);
    white-space: nowrap;
  }
  .demo-copy {
    min-width: 0;
  }
  .demo-links {
    margin-left: auto;
    display: flex;
    gap: 16px;
    white-space: nowrap;
  }
  .demo-links a {
    color: var(--text);
    font-weight: 650;
    border-bottom: 1px solid var(--accent-line);
  }
  .demo-links a:hover {
    border-bottom-color: var(--accent);
  }
  .demo-notice {
    position: fixed;
    left: 50%;
    bottom: 20px;
    transform: translateX(-50%);
    width: min(620px, calc(100% - 32px));
    z-index: 50;
    display: flex;
    align-items: center;
    gap: 14px;
    padding: 14px 12px 14px 16px;
    border: 1px solid var(--border-strong);
    border-left: 3px solid var(--accent);
    background: var(--panel);
    box-shadow: var(--shadow);
  }
  .demo-notice strong {
    font-size: 12.5px;
    font-weight: 650;
  }
  .demo-notice p {
    margin: 4px 0 0;
    color: var(--muted);
    font-size: 11px;
    line-height: 1.5;
  }
  .demo-notice .primary-button {
    flex: 0 0 auto;
  }
  @media (max-width: 900px) {
    .demo-banner {
      padding-inline: 14px;
    }
    .demo-copy {
      display: none;
    }
  }
  @media (max-width: 560px) {
    .demo-banner {
      padding-inline: 10px;
      flex-wrap: wrap;
      gap: 4px 10px;
    }
    .demo-label {
      white-space: normal;
    }
    .demo-secondary {
      display: none;
    }
    .demo-notice {
      flex-wrap: wrap;
      bottom: 12px;
    }
    .demo-notice > div {
      flex: 1 1 calc(100% - 48px);
    }
    .demo-notice .primary-button {
      order: 1;
      width: 100%;
      min-height: 44px;
    }
  }
</style>
