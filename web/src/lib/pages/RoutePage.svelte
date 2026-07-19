<script lang="ts">
  import type { Inventory, Route } from '../types';
  import PageHead from '../PageHead.svelte';
  import { navigate } from '../router';
  import { formatDate } from '../time';

  let { path, inventory }: { path: string; inventory: Inventory } = $props();
  let routeID = $derived(routeRecordID(path));
  let route = $derived(inventory.routes.find((item) => item.id === routeID));
  let recordState = $derived(route ? routeState(route) : 'missing');
  let resolved = $derived(recordState === 'resolved');
  let facts = $derived(route ? routeFacts(route) : []);
  let checks = $derived(route ? routeChecks(route) : []);
  let routeIssue = $derived(inventory.issues.find((issue) => issue.resource === 'routes'));

  let filter = $state('');
  let brokenCount = $derived(inventory.routes.filter((item) => routeState(item) === 'broken').length);
  let sortedRoutes = $derived([...inventory.routes].sort(
    (a, b) => stateRank(routeState(a)) - stateRank(routeState(b)) || a.domain.localeCompare(b.domain)
  ));
  let visibleRoutes = $derived(sortedRoutes.filter((item) => matchesFilter(item, filter)));

  type RouteState = 'resolved' | 'unknown' | 'ambiguous' | 'broken' | 'missing';

  function routeState(item: Route): RouteState {
    const status = item.status.toLowerCase();
    if (status === 'ok' || status === 'resolved') return 'resolved';
    if (status === 'ambiguous') return 'ambiguous';
    if (status === 'unknown') return 'unknown';
    return 'broken';
  }

  function stateRank(value: RouteState): number {
    if (value === 'broken') return 0;
    if (value === 'ambiguous' || value === 'unknown') return 1;
    return 2;
  }

  function matchesFilter(item: Route, term: string): boolean {
    return `${item.domain} ${item.proxy} ${item.upstream_host} ${item.service}`.toLowerCase().includes(term.toLowerCase());
  }

  function certDays(iso: string): number {
    return Math.floor((new Date(iso).getTime() - Date.now()) / 86400000);
  }

  function certTone(days: number): 'ok' | 'warn' | 'bad' {
    if (days <= 7) return 'bad';
    if (days <= 30) return 'warn';
    return 'ok';
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
      ['TLS FACT', item.cert_expires_at ? `The attached certificate record expires ${formatDate(item.cert_expires_at)}.` : `No certificate expiry is attached to ${item.domain}.`, record('RTE', item.id)]
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

{#if routeID === 0}
  <main class="page">
    <PageHead kicker="Routes · Register" title="Routes" meta={`${inventory.routes.length} routes${brokenCount ? ` · ${brokenCount} broken` : ''}`} />
    {#if inventory.routes.length}
      <div class="toolbar"><input class="inline-search" bind:value={filter} aria-label="Filter routes" placeholder="Find domain, proxy, target, or service" /><span class="spacer"></span><span class="toolbar-meta">{visibleRoutes.length} VISIBLE · {inventory.routes.length} TOTAL</span></div>
      <section class="register" data-component-id="route-register">
        <header class="register-head routes-cols"><span>Public name</span><span>Proxy</span><span>Declared target</span><span>Joined service</span><span>Cert</span></header>
        {#if visibleRoutes.length}
          {#each visibleRoutes as item}
            <a class="register-row routes-cols" href={`/routes/${item.id}`} onclick={(event) => { event.preventDefault(); navigate(`/routes/${item.id}`); }} aria-label={`Open route ${item.domain}`}>
              <div data-label="Public name"><strong>{item.domain}</strong><small class="mono">{item.tls ? 'HTTPS' : 'HTTP'} · {item.path_prefix || '/'}</small></div>
              <div data-label="Proxy"><strong>{item.proxy || 'Unknown'}</strong><small>{record('PRX', item.proxy_id ?? item.id)}</small></div>
              <div data-label="Declared target"><code>{item.upstream_host}:{item.upstream_port ?? '—'}</code></div>
              <div data-label="Joined service">{#if routeState(item) === 'resolved'}<span class="status ok">{item.service || item.upstream_host} · {item.resolve_confidence || 'unknown'}</span>{:else if routeState(item) === 'ambiguous'}<span class="status warn">Ambiguous</span>{:else if routeState(item) === 'unknown'}<span class="status warn">Unresolved</span>{:else}<span class="status bad">No match</span>{/if}</div>
              <div data-label="Cert">{#if item.cert_expires_at}{@const days = certDays(item.cert_expires_at)}<span class={`status ${certTone(days)}`}>{days}d</span>{:else}<code>—</code>{/if}</div>
            </a>
          {/each}
        {:else}
          <div class="empty-register"><strong>NO MATCHING ROUTES</strong><span>No route matches “{filter}”.</span></div>
        {/if}
      </section>
    {:else}
      <section class="empty-register"><strong>NO ROUTES OBSERVED</strong><span>Connect a reverse proxy source to record public routes.</span>{#if !inventory.readOnly}<button class="primary-button" onclick={() => navigate('/sources')}>Add source</button>{/if}</section>
    {/if}
  </main>
{:else if route}
  <main class="page">
    <PageHead kicker="Routes · Record detail" title={route.domain}>
      {#snippet actions()}<button class="quiet-button" onclick={() => navigate('/routes')}>All routes</button><button class="quiet-button" onclick={copyEvidence}>Copy evidence</button>{#if !inventory.readOnly}<button class="primary-button" onclick={() => navigate('/sources')}>Source record</button>{/if}{/snippet}
    </PageHead>
    <section class="route-strip" data-component-id="route-resolution-ledger">
      <div class="route-step"><span>PUBLIC NAME</span><strong>{route.domain}</strong><code>{route.tls ? 'HTTPS' : 'HTTP'} · {route.path_prefix || '/'}</code></div>
      <div class="route-step"><span>PROXY</span><strong>{route.proxy || 'Unknown proxy'}</strong><code>{record('PRX', route.proxy_id)}</code></div>
      <div class="route-step"><span>DECLARED TARGET</span><strong>{route.upstream_host}:{route.upstream_port ?? '—'}</strong><code>PROXY DECLARATION</code></div>
      <div class:pass={resolved} class:fail={!resolved} class="route-step"><span>INDEX JOIN</span><strong>{resolved ? `${route.service || route.upstream_host} · ${route.resolve_confidence.toUpperCase() || 'UNKNOWN'}` : recordState === 'ambiguous' ? 'MULTIPLE CURRENT RECORDS' : recordState === 'unknown' ? 'NOT YET RESOLVED' : 'NO CURRENT RECORD'}</strong><code>{record('RTE', route.id)}</code></div>
    </section>
    <section class="evidence-layout">
      <article class="evidence-main" data-component-id="route-evidence-record">
        <div class="evidence-heading"><div><h2 class="entity-title">{route.domain}</h2><div class="entity-meta">{record('RTE', route.id)} · {route.state.toUpperCase()} · {route.status.toUpperCase()}</div></div><span class:ok={resolved} class:bad={!resolved} class="status">{stateLabel(recordState, route)}</span></div>
        <h3 class="outcome">Evidence</h3>
        <p class="lead-copy">{resolved ? 'The proxy target joins one current service using the observed upstream and resolution confidence returned by Homedex.' : recordState === 'ambiguous' ? 'The proxy declaration matches more than one current service, so the route remains unjoined.' : recordState === 'unknown' ? 'The route is recorded, but the backend has not produced a current join outcome.' : 'The proxy declaration is intact, but its target does not match any current container IP, Docker alias, host address, or published port.'}</p>
        <div class="fact-lines">{#each facts as fact}<div class="fact-line"><b>{fact[0]}</b><p>{fact[1]}</p><code>{fact[2]}</code></div>{/each}</div>
      </article>
      <aside class="evidence-side"><h3>Checks</h3><ol class="mini-list">{#each checks as check, index}<li><b>{String(index + 1).padStart(2, '0')}</b><span>{check}</span></li>{/each}</ol></aside>
    </section>
  </main>
{:else}
  <main class="page">
    <PageHead kicker={routeIssue ? 'Routes · Unavailable' : 'Routes · Missing'} title={routeIssue ? 'Routes unavailable' : 'No route record'} copy={routeIssue?.message || undefined} />
    <section class="empty-register"><strong>{routeIssue ? 'ROUTE DATA UNAVAILABLE' : 'NO CURRENT RECORD'}</strong><span>{routeIssue?.message || (routeID ? `Route ${routeID}` : 'No route selected')}</span><button class="primary-button" onclick={() => navigate('/')}>Return to the index</button></section>
  </main>
{/if}

<style>
  a.register-row:hover { background: var(--hover); }
</style>
