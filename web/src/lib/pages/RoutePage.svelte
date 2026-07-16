<script lang="ts">
  import type { Inventory, Route } from '../types';
  import PageHead from '../PageHead.svelte';
  import { navigate } from '../router';

  let { path, inventory }: { path: string; inventory: Inventory } = $props();
  let routeID = $derived(routeRecordID(path));
  let route = $derived(inventory.routes.find((item) => item.id === routeID) ?? (routeID ? undefined : inventory.routes[0]));
  let state = $derived(route ? routeState(route) : 'missing');
  let resolved = $derived(state === 'resolved');
  let facts = $derived(route ? routeFacts(route) : []);
  let checks = $derived(route ? routeChecks(route) : []);
  let routeIssue = $derived(inventory.issues.find((issue) => issue.resource === 'routes'));

  type RouteState = 'resolved' | 'unknown' | 'ambiguous' | 'broken' | 'missing';

  function routeState(item: Route): RouteState {
    const status = item.status.toLowerCase();
    if (status === 'ok' || status === 'resolved') return 'resolved';
    if (status === 'ambiguous') return 'ambiguous';
    if (status === 'unknown') return 'unknown';
    return 'broken';
  }

  function stateLabel(value: RouteState, item: Route): string {
    if (value === 'resolved') return `Resolved · ${item.resolve_confidence || 'unknown'} confidence`;
    if (value === 'ambiguous') return 'Ambiguous · no unique match';
    if (value === 'unknown') return 'Unknown · not resolved';
    return 'Broken · no match';
  }

  function routeRecordID(value: string): number {
    const pathname = value.split('?')[0];
    const segment = pathname.startsWith('/routes/') ? pathname.slice('/routes/'.length).split('/')[0] : '';
    return /^\d+$/.test(segment) ? Number(segment) : 0;
  }

  function record(prefix: string, id: number | null | undefined): string {
    return id ? `${prefix}-${String(id).padStart(3, '0')}` : prefix;
  }

  function routeFacts(item: Route): string[][] {
    const target = `${item.upstream_host}:${item.upstream_port ?? '—'}`;
    if (routeState(item) === 'resolved') {
      return [
        ['PROXY FACT', `${item.proxy || 'The observed proxy'} declares upstream ${target}.`, record('PRX', item.proxy_id ?? item.id)],
        ['DOCKER FACT', `Current service ${item.service || item.upstream_host} matches the declared upstream host.`, record('SRV', item.resolved_service_id)],
        ['PORT FACT', `The route declares ${item.upstream_port ?? 'no'} internal port for the selected service.`, record('RTE', item.id)],
        ['JOIN RESULT', `${item.service || item.upstream_host} is the selected current service at ${item.resolve_confidence || 'unknown'} confidence.`, (item.resolve_confidence || 'unknown').toUpperCase()]
      ];
    }
    const itemState = routeState(item);
    const joinFact = itemState === 'ambiguous'
      ? `More than one current service matches ${item.upstream_host}; Homedex did not guess.`
      : itemState === 'unknown'
        ? `No route resolution has been recorded for ${item.upstream_host}.`
        : `No current container address or network alias matches ${item.upstream_host}.`;
    return [
      ['PROXY FACT', `${item.proxy || 'The observed proxy'} still declares ${target} for ${item.domain}.`, record('PRX', item.proxy_id ?? item.id)],
      ['DOCKER FACT', joinFact, record('RTE', item.id)],
      ['PORT FACT', `The declared port ${item.upstream_port ?? '—'} does not produce a current service join.`, record('RTE', item.id)],
      ['TLS FACT', item.cert_expires_at ? `The attached certificate record expires at ${item.cert_expires_at}.` : `No certificate expiry is attached to ${item.domain}.`, record('RTE', item.id)]
    ];
  }

  function routeChecks(item: Route): string[] {
    return routeState(item) === 'resolved'
      ? [`${item.upstream_host} matches exactly`, `${item.upstream_port ?? 'No port'} is the declared internal port`, `${item.service || item.upstream_host} is current`]
      : ['Confirm target host still exists', `Check ${item.proxy || 'proxy'} source record`, 'Renew or remove stale TLS'];
  }

  async function copyEvidence() {
    await navigator.clipboard?.writeText(facts.map((fact) => `${fact[0]}: ${fact[1]} [${fact[2]}]`).join('\n'));
  }
</script>

{#if route}
  <main class="page">
    <PageHead kicker="ROUTES · RECORD DETAIL" title={route.domain} copy="A route is a join between separately observed proxy and inventory facts.">
      {#snippet actions()}<button class="quiet-button" onclick={copyEvidence}>Copy evidence</button>{#if !inventory.readOnly}<button class="primary-button" onclick={() => navigate('/sources')}>Open source</button>{/if}{/snippet}
    </PageHead>
    <section class="route-strip" data-component-id="route-resolution-ledger">
      <div class="route-step"><span>PUBLIC NAME</span><strong>{route.domain}</strong><code>{route.tls ? 'HTTPS' : 'HTTP'} · {route.path_prefix || '/'}</code></div>
      <div class="route-step"><span>PROXY</span><strong>{route.proxy || 'Unknown proxy'}</strong><code>{record('PRX', route.proxy_id)}</code></div>
      <div class="route-step"><span>DECLARED TARGET</span><strong>{route.upstream_host}:{route.upstream_port ?? '—'}</strong><code>PROXY DECLARATION</code></div>
      <div class:pass={resolved} class:fail={!resolved} class="route-step"><span>INDEX JOIN</span><strong>{resolved ? `${route.service || route.upstream_host} · ${route.resolve_confidence.toUpperCase() || 'UNKNOWN'}` : state === 'ambiguous' ? 'MULTIPLE CURRENT RECORDS' : state === 'unknown' ? 'NOT YET RESOLVED' : 'NO CURRENT RECORD'}</strong><code>{record('RTE', route.id)}</code></div>
    </section>
    <section class="evidence-layout">
      <article class="evidence-main" data-component-id="route-evidence-record">
        <div class="evidence-heading"><div><h2 class="entity-title">{route.domain}</h2><div class="entity-meta">{record('RTE', route.id)} · {route.state.toUpperCase()} · {route.status.toUpperCase()}</div></div><span class:ok={resolved} class:bad={!resolved} class="status">{stateLabel(state, route)}</span></div>
        <h3 class="outcome">{resolved ? 'Why this match is trusted' : state === 'ambiguous' ? 'Why Homedex did not guess' : state === 'unknown' ? 'Resolution is not available' : 'Why this join failed'}</h3>
        <p class="lead-copy">{resolved ? 'The proxy target joins one current service using the observed upstream and resolution confidence returned by Homedex.' : state === 'ambiguous' ? 'The proxy declaration matches more than one current service, so the route remains unjoined.' : state === 'unknown' ? 'The route is recorded, but the backend has not produced a current join outcome.' : 'The proxy declaration is intact, but its target does not match any current container IP, Docker alias, host address, or published port.'}</p>
        <div class="fact-lines">{#each facts as fact}<div class="fact-line"><b>{fact[0]}</b><p>{fact[1]}</p><code>{fact[2]}</code></div>{/each}</div>
      </article>
      <aside class="evidence-side"><div class="section-label">Interpretation</div><h3>{resolved ? 'Evidence agrees' : 'Next checks'}</h3><p>{resolved ? 'The selected route record joins the proxy declaration to one current service.' : 'Homedex cannot repair the route. It keeps the declaration and points to the missing evidence.'}</p><ol class="mini-list">{#each checks as check, index}<li><b>{String(index + 1).padStart(2, '0')}</b><span>{check}</span></li>{/each}</ol></aside>
    </section>
  </main>
{:else}
  <main class="page">
    <PageHead kicker={routeIssue ? 'ROUTES · RESOURCE UNAVAILABLE' : 'ROUTES · MISSING RECORD'} title={routeIssue ? 'Routes could not be loaded.' : 'That route does not exist.'} copy={routeIssue?.message || 'The public name is not present in the current route register.'} />
    <section class="empty-register"><strong>{routeIssue ? 'ROUTE DATA UNAVAILABLE' : 'NO CURRENT RECORD'}</strong><span>{routeIssue?.message || (routeID ? `Route ${routeID}` : 'No route selected')}</span><button class="primary-button" onclick={() => navigate('/')}>Return to the index</button></section>
  </main>
{/if}
