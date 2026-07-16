<script lang="ts">
  import PageHead from '../PageHead.svelte';
  import { navigate } from '../router';

  let { path }: { path: string } = $props();
  let resolved = $derived(!decodeURIComponent(path).includes('old.lab.example'));
  let title = $derived(resolved ? 'photos.lab.example' : 'old.lab.example');
  let target = $derived(resolved ? 'immich-server:2283' : '10.0.20.14:8080');
  let facts = $derived(resolved ? [
    ['PROXY FACT', 'Nginx Proxy Manager declares upstream host immich-server and upstream port 2283.', 'PRX-016'],
    ['DOCKER FACT', 'Container SRV-001 exposes network alias immich-server on immich_default.', 'SRC-001'],
    ['PORT FACT', 'The same container declares 2283/tcp internally; no host-port inference is required.', 'PRT-084'],
    ['JOIN RESULT', 'Alias plus internal port produces a unique, current service match.', 'HIGH']
  ] : [
    ['PROXY FACT', 'Traefik still declares 10.0.20.14:8080 for old.lab.example.', 'PRX-023'],
    ['DOCKER FACT', 'No current container address or network alias equals 10.0.20.14.', 'SRC-001—003'],
    ['PORT FACT', 'Port 8080 exists on gateway and core-01, but neither host owns 10.0.20.14.', 'PRT-041/067'],
    ['TLS FACT', 'The certificate for old.lab.example expired four days ago.', 'TLS-008']
  ]);
  let checks = $derived(resolved ? ['Alias matches exactly', 'Internal port matches exactly', 'Record seen in scan 042'] : ['Confirm target host still exists', 'Check Traefik source record', 'Renew or remove expired TLS']);

  async function copyEvidence() {
    await navigator.clipboard?.writeText(facts.map((fact) => `${fact[0]}: ${fact[1]} [${fact[2]}]`).join('\n'));
  }
</script>

<main class="page">
  <PageHead kicker="ROUTES · RECORD DETAIL" {title} copy="A route is a join between separately observed proxy and inventory facts.">
    {#snippet actions()}<button class="quiet-button" onclick={copyEvidence}>Copy evidence</button><button class="primary-button" onclick={() => navigate('/sources')}>Open source</button>{/snippet}
  </PageHead>
  <section class="route-strip" data-component-id="route-resolution-ledger">
    <div class="route-step"><span>PUBLIC NAME</span><strong>{title}</strong><code>HTTPS · /</code></div>
    <div class="route-step"><span>PROXY</span><strong>{resolved ? 'Nginx Proxy Manager' : 'Traefik'}</strong><code>{resolved ? 'gateway' : 'core-01'}</code></div>
    <div class="route-step"><span>DECLARED TARGET</span><strong>{target}</strong><code>PROXY DECLARATION</code></div>
    <div class:pass={resolved} class:fail={!resolved} class="route-step"><span>INDEX JOIN</span><strong>{resolved ? 'SRV-001 · HIGH' : 'NO CURRENT RECORD'}</strong><code>SCAN 042</code></div>
  </section>
  <section class="evidence-layout">
    <article class="evidence-main" data-component-id="route-evidence-record">
      <div class="evidence-heading"><div><h2 class="entity-title">{title}</h2><div class="entity-meta">RTE-{resolved ? '001' : '023'} · LAST CHECKED 6H AGO</div></div><span class:ok={resolved} class:bad={!resolved} class="status">{resolved ? 'Resolved · high confidence' : 'Broken · no match'}</span></div>
      <h3 class="outcome">{resolved ? 'Why this match is trusted' : 'Why this join failed'}</h3>
      <p class="lead-copy">{resolved ? 'The proxy target joins a current Docker record by network alias and declared internal port. These are separate observed facts that agree.' : 'The proxy declaration is intact, but its target does not match any current container IP, Docker alias, host address, or published port.'}</p>
      <div class="fact-lines">{#each facts as fact}<div class="fact-line"><b>{fact[0]}</b><p>{fact[1]}</p><code>{fact[2]}</code></div>{/each}</div>
    </article>
    <aside class="evidence-side"><div class="section-label">Interpretation</div><h3>{resolved ? 'Evidence agrees' : 'Next checks'}</h3><p>{resolved ? 'The join is trusted because two independently collected records point to one current service.' : 'Homedex cannot repair the route. It keeps the declaration and points to the missing evidence.'}</p><ol class="mini-list">{#each checks as check, index}<li><b>{String(index + 1).padStart(2, '0')}</b><span>{check}</span></li>{/each}</ol></aside>
  </section>
</main>
