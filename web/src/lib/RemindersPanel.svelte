<script lang="ts">
  import { onMount } from 'svelte';
  import { createNotificationRule, deleteNotificationRule, loadNotificationRules, testNotificationRule } from './api';
  import type { NotificationRule } from './types';

  let { readOnly = false }: { readOnly?: boolean } = $props();

  let rules = $state<NotificationRule[]>([]);
  let loading = $state(true);
  let loadError = $state('');
  let pending = $state<Record<number, string>>({});
  let notices = $state<Record<number, { tone: 'ok' | 'bad'; text: string }>>({});
  let showAdd = $state(false);
  let days = $state(14);
  let url = $state('');
  let addError = $state('');
  let adding = $state(false);
  let confirmingID = $state<number | null>(null);
  let confirmTimer = 0;

  function armDelete(id: number) {
    confirmingID = id;
    window.clearTimeout(confirmTimer);
    confirmTimer = window.setTimeout(() => (confirmingID = null), 4000);
  }

  onMount(load);

  async function load() {
    loading = true;
    loadError = '';
    try {
      rules = await loadNotificationRules();
    } catch (cause) {
      loadError = cause instanceof Error ? cause.message : 'Reminders could not be loaded.';
    } finally {
      loading = false;
    }
  }

  function setNotice(id: number, tone: 'ok' | 'bad', text: string) {
    notices = { ...notices, [id]: { tone, text } };
  }

  function clearPending(id: number) {
    const next = { ...pending };
    delete next[id];
    pending = next;
  }

  function label(rule: NotificationRule) {
    return rule.threshold_days === null ? rule.kind : `${rule.threshold_days}d before`;
  }

  async function testRule(id: number) {
    pending = { ...pending, [id]: 'Testing' };
    setNotice(id, 'ok', 'Sending test…');
    try {
      const result = await testNotificationRule(id);
      if (result.status === 'ok') setNotice(id, 'ok', 'Test notification sent.');
      else setNotice(id, 'bad', result.error || 'Test delivery failed.');
    } catch (cause) {
      setNotice(id, 'bad', cause instanceof Error ? cause.message : 'Test delivery failed.');
    } finally {
      clearPending(id);
    }
  }

  async function removeRule(rule: NotificationRule) {
    if (confirmingID !== rule.id) {
      armDelete(rule.id);
      return;
    }
    window.clearTimeout(confirmTimer);
    confirmingID = null;
    pending = { ...pending, [rule.id]: 'Deleting' };
    try {
      await deleteNotificationRule(rule.id);
      await load();
    } catch (cause) {
      setNotice(rule.id, 'bad', cause instanceof Error ? cause.message : 'Delete failed.');
    } finally {
      clearPending(rule.id);
    }
  }

  async function submitAdd(event: SubmitEvent) {
    event.preventDefault();
    addError = '';
    const threshold = Math.trunc(Number(days));
    if (!Number.isFinite(threshold) || threshold < 1) {
      addError = 'Days must be 1 or more.';
      return;
    }
    if (!url.trim()) {
      addError = 'A Shoutrrr URL is required.';
      return;
    }
    adding = true;
    try {
      await createNotificationRule({ name: `Expiry ${threshold}d`, kind: 'expiry', threshold_days: threshold, channels: [url.trim()] });
      url = '';
      days = 14;
      showAdd = false;
      await load();
    } catch (cause) {
      addError = cause instanceof Error ? cause.message : 'Reminder could not be created.';
    } finally {
      adding = false;
    }
  }
</script>

<section class="register" data-component-id="reminders-panel">
  <div class="register-row">
    <div>
      <div class="section-label">Reminders</div>
      <small>Expiry alerts through ntfy, Discord, or any Shoutrrr URL.</small>
    </div>
  </div>
  {#if loading}
    <div class="empty-register compact"><strong>LOADING REMINDERS</strong><span>Checking for configured expiry alerts.</span></div>
  {:else if loadError}
    <div class="empty-register compact"><strong>REMINDERS UNAVAILABLE</strong><span>{loadError}</span></div>
  {:else if rules.length}
    {#each rules as rule (rule.id)}
      <div class="register-row">
        <div>
          <strong>{label(rule)}</strong>
          <small>{#if rule.channels.length}{#each rule.channels as kind}<code>{kind}</code> {/each}{:else}no channels{/if}</small>
        </div>
        {#if !readOnly}
          <div class="source-editor">
            <button class="quiet-button" disabled={Boolean(pending[rule.id])} onclick={() => testRule(rule.id)}>Test</button>
            <button class={confirmingID === rule.id ? 'danger-button' : 'quiet-button'} disabled={Boolean(pending[rule.id])} onclick={() => removeRule(rule)}>{confirmingID === rule.id ? 'Confirm delete' : 'Delete'}</button>
            {#if notices[rule.id]}<span class={`status ${notices[rule.id].tone}`} role="status">{notices[rule.id].text}</span>{/if}
          </div>
        {/if}
      </div>
    {/each}
  {:else}
    <div class="empty-register compact"><strong>NO REMINDERS</strong><span>Expiry records stay silent until a rule exists.</span></div>
  {/if}
  {#if !readOnly}
    {#if showAdd}
      <form class="source-editor" onsubmit={submitAdd}>
        <label>Days before <input type="number" min="1" bind:value={days} /></label>
        <label>Shoutrrr URL <input type="text" placeholder="ntfy://ntfy.sh/my-lab" bind:value={url} /></label>
        <button class="primary-button" disabled={adding}>Add reminder</button>
        <button type="button" class="quiet-button" disabled={adding} onclick={() => { showAdd = false; addError = ''; }}>Cancel</button>
        {#if addError}<span class="status bad" role="alert">{addError}</span>{/if}
      </form>
    {:else}
      <div class="register-row"><div><button class="quiet-button" onclick={() => (showAdd = true)}>Add reminder</button></div></div>
    {/if}
  {/if}
</section>
