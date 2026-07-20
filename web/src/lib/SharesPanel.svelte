<script lang="ts">
  import { onMount } from 'svelte';
  import { createShare, loadShares, revokeShare } from './api';
  import { formatDate, relativeTime } from './time';
  import type { Share } from './types';

  let { readOnly = false }: { readOnly?: boolean } = $props();

  let shares = $state<Share[]>([]);
  let loading = $state(true);
  let loadError = $state('');
  let pending = $state<Record<number, string>>({});
  let confirmingID = $state<number | null>(null);
  let confirmTimer = 0;

  let showCreate = $state(false);
  let name = $state('');
  let expiresIn = $state('');
  let creating = $state(false);
  let createError = $state('');
  let created = $state<Share | null>(null);
  let copied = $state(false);

  onMount(() => {
    if (!readOnly) load();
  });

  async function load() {
    loading = true;
    loadError = '';
    try {
      shares = await loadShares();
    } catch (cause) {
      loadError = cause instanceof Error ? cause.message : 'Shares could not be loaded.';
    } finally {
      loading = false;
    }
  }

  function armRevoke(id: number) {
    confirmingID = id;
    window.clearTimeout(confirmTimer);
    confirmTimer = window.setTimeout(() => (confirmingID = null), 4000);
  }

  function clearPending(id: number) {
    const next = { ...pending };
    delete next[id];
    pending = next;
  }

  async function revoke(share: Share) {
    if (confirmingID !== share.id) {
      armRevoke(share.id);
      return;
    }
    window.clearTimeout(confirmTimer);
    confirmingID = null;
    pending = { ...pending, [share.id]: 'Revoking' };
    try {
      await revokeShare(share.id);
      await load();
    } catch (cause) {
      loadError = cause instanceof Error ? cause.message : 'Revoke failed.';
    } finally {
      clearPending(share.id);
    }
  }

  function shareURL(share: Share): string {
    return `${window.location.origin}${share.share_url ?? ''}`;
  }

  async function copyLink() {
    if (!created) return;
    await navigator.clipboard?.writeText(shareURL(created));
    copied = true;
    window.setTimeout(() => (copied = false), 1800);
  }

  async function submitCreate(event: SubmitEvent) {
    event.preventDefault();
    createError = '';
    if (!name.trim()) {
      createError = 'A name is required.';
      return;
    }
    const input: { name: string; expires_in_hours?: number } = { name: name.trim() };
    if (expiresIn.trim()) {
      const hours = Math.trunc(Number(expiresIn));
      if (!Number.isFinite(hours) || hours < 1) {
        createError = 'Expiry hours must be 1 or more.';
        return;
      }
      input.expires_in_hours = hours;
    }
    creating = true;
    try {
      created = await createShare(input);
      name = '';
      expiresIn = '';
      showCreate = false;
      await load();
    } catch (cause) {
      createError = cause instanceof Error ? cause.message : 'Share could not be created.';
    } finally {
      creating = false;
    }
  }
</script>

{#if !readOnly}
  <section class="register" data-component-id="shares-panel">
    <div class="register-row">
      <div>
        <div class="section-label">Read-only shares</div>
        <small>Tokenized links with read access to the inventory. Notes, custom fields, and labels are always excluded.</small>
      </div>
    </div>
    {#if created}
      <div class="register-row reminder-row">
        <div class="source-editor">
          <span class="field-label">Share link</span>
          <code>{shareURL(created)}</code>
          <button class="quiet-button" onclick={copyLink}>{copied ? 'Link copied' : 'Copy link'}</button>
        </div>
        <small>This link is shown once. Homedex stores only a hash.</small>
      </div>
    {/if}
    {#if loading}
      <div class="empty-register compact"><strong>LOADING SHARES</strong><span>Checking for active share links.</span></div>
    {:else if loadError}
      <div class="empty-register compact"><strong>SHARES UNAVAILABLE</strong><span>{loadError}</span></div>
    {:else if shares.length}
      {#each shares as share (share.id)}
        <div class="register-row reminder-row">
          <div>
            <strong>{share.name}</strong>
            <small>created {relativeTime(share.created_at)} · expires {share.expires_at ? formatDate(share.expires_at) : 'no expiry'}</small>
          </div>
          <div class="source-editor">
            <button class={confirmingID === share.id ? 'danger-button' : 'quiet-button'} disabled={Boolean(pending[share.id])} onclick={() => revoke(share)}>{confirmingID === share.id ? 'Confirm revoke' : 'Revoke'}</button>
          </div>
        </div>
      {/each}
    {:else}
      <div class="empty-register compact"><strong>NO SHARES</strong><span>Nothing is exposed until you create a link.</span></div>
    {/if}
    {#if showCreate}
      <form class="source-editor" onsubmit={submitCreate}>
        <label>Name <input type="text" bind:value={name} /></label>
        <label>Expires in hours <input type="number" min="1" placeholder="168" bind:value={expiresIn} /></label>
        <div class="form-actions">
          <button class="primary-button" disabled={creating}>Create share</button>
          <button type="button" class="quiet-button" disabled={creating} onclick={() => { showCreate = false; createError = ''; }}>Cancel</button>
          {#if createError}<span class="status bad" role="alert">{createError}</span>{/if}
        </div>
      </form>
    {:else}
      <div class="register-row"><div><button class="quiet-button" onclick={() => (showCreate = true)}>Create share</button></div></div>
    {/if}
  </section>
{/if}
