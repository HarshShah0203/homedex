<script lang="ts">
  import { Eye, EyeOff, LockKeyhole } from 'lucide-svelte';
  import { login } from './api';

  let { onlogin }: { onlogin: () => Promise<void> } = $props();
  let password = $state('');
  let visible = $state(false);
  let busy = $state(false);
  let error = $state('');

  async function submit() {
    if (!password) return;
    busy = true;
    error = '';
    try {
      await login(password);
      await onlogin();
    } catch (cause) {
      error = cause instanceof Error ? cause.message : 'Sign in failed.';
    } finally {
      busy = false;
    }
  }
</script>

<div class="login-shell">
  <header class="setup-top">
    <span class="brand"><span class="brand-mark">H</span><span><strong>homedex</strong><small>Address book for your lab</small></span></span>
    <span class="setup-local">LOCAL SESSION · NO TELEMETRY</span>
  </header>
  <main class="login-page">
    <section class="login-sheet">
      <div class="login-declaration"><span class="login-lock"><LockKeyhole size={19} /></span><div class="kicker">PRIVATE INDEX · AUTHENTICATION</div><h1 class="page-title">Open your address book.</h1><p>This password is checked by the Homedex instance on your local network. It is never sent elsewhere.</p></div>
      <form onsubmit={(event) => { event.preventDefault(); submit(); }}>
        <label class="field-label" for="login-password">Administrator password</label>
        <div class="password-entry"><input id="login-password" bind:value={password} type={visible ? 'text' : 'password'} autocomplete="current-password" /><button class="icon-button" type="button" aria-label="Show password" onclick={() => (visible = !visible)}>{#if visible}<EyeOff size={15} />{:else}<Eye size={15} />{/if}</button></div>
        {#if error}<p class="field-error" role="alert">{error}</p>{/if}
        <button class="primary-button login-submit" disabled={busy || !password}>{busy ? 'Signing in…' : 'Sign in'}</button>
      </form>
    </section>
  </main>
</div>
