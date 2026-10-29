import { readFileSync } from 'node:fs';
import { defineConfig, type Plugin } from 'vite';
import { svelte } from '@sveltejs/vite-plugin-svelte';
import tailwindcss from '@tailwindcss/vite';

// `npm run build:demo` builds the static live demo: the same app with
// fabricated data and no backend (see src/lib/demoMode.ts), served from a
// GitHub Pages project path. It writes to dist-demo and never touches dist or
// the assets embedded in the Go binary. A fork can pass `-- --base /name/`.
// The live demo's top-level pages, as linked from its navigation and posts.
const demoPages = ['hosts', 'routes', 'ports', 'changes', 'expiry', 'copy-my-lab', 'sources', 'setup'];

// Link previews (Reddit, Discord, Slack, iMessage) need an absolute image on
// the same site as the page. raw.githubusercontent.com is often dropped.
const demoOrigin = process.env.HOMEDEX_DEMO_ORIGIN ?? 'https://harshshah0203.github.io';
const socialCardPath = new URL('../docs/social-card.png', import.meta.url);

function liveDemo(): Plugin {
  let base = '/homedex/';
  const description = 'The inventory your homelab never wrote. Services, ports, routes and cert expiry, discovered from Docker and your reverse proxy. This live demo is fabricated data.';
  const preview = () => {
    const root = new URL(base.endsWith('/') ? base : `${base}/`, `${demoOrigin}/`);
    return { pageUrl: root.href, imageUrl: new URL('social-card.png', root).href };
  };
  return {
    name: 'homedex-live-demo',
    apply: 'build',
    enforce: 'post',
    configResolved(config) {
      base = config.base;
    },
    transformIndexHtml(html) {
      const { pageUrl, imageUrl } = preview();
      return {
        html: html.replace(/<title>[^<]*<\/title>/, '<title>Homedex live demo · What runs where</title>'),
        tags: [
          { tag: 'link', attrs: { rel: 'canonical', href: pageUrl }, injectTo: 'head' },
          { tag: 'meta', attrs: { property: 'og:type', content: 'website' }, injectTo: 'head' },
          { tag: 'meta', attrs: { property: 'og:site_name', content: 'Homedex' }, injectTo: 'head' },
          { tag: 'meta', attrs: { property: 'og:url', content: pageUrl }, injectTo: 'head' },
          { tag: 'meta', attrs: { property: 'og:title', content: 'Homedex · What runs where' }, injectTo: 'head' },
          { tag: 'meta', attrs: { property: 'og:description', content: description }, injectTo: 'head' },
          { tag: 'meta', attrs: { property: 'og:image', content: imageUrl }, injectTo: 'head' },
          { tag: 'meta', attrs: { property: 'og:image:width', content: '1280' }, injectTo: 'head' },
          { tag: 'meta', attrs: { property: 'og:image:height', content: '640' }, injectTo: 'head' },
          { tag: 'meta', attrs: { property: 'og:image:alt', content: 'Homedex social card: What runs where. A read-only homelab inventory.' }, injectTo: 'head' },
          { tag: 'meta', attrs: { name: 'twitter:card', content: 'summary_large_image' }, injectTo: 'head' },
          { tag: 'meta', attrs: { name: 'twitter:title', content: 'Homedex · What runs where' }, injectTo: 'head' },
          { tag: 'meta', attrs: { name: 'twitter:description', content: description }, injectTo: 'head' },
          { tag: 'meta', attrs: { name: 'twitter:image', content: imageUrl }, injectTo: 'head' }
        ]
      };
    },
    generateBundle(_options, bundle) {
      // GitHub Pages answers unknown paths with 404.html. Making it a copy of
      // the app lets a deep link such as /homedex/routes/7 survive a refresh.
      // The top-level pages also get a real copy, so a shared link like
      // /homedex/hosts answers 200 and link previews do not read it as broken.
      // Asset URLs in the page are absolute under the base, so copies work.
      const index = bundle['index.html'];
      if (!index || index.type !== 'asset') throw new Error('live demo: index.html was not emitted');
      this.emitFile({ type: 'asset', fileName: 'social-card.png', source: readFileSync(socialCardPath) });
      this.emitFile({ type: 'asset', fileName: '404.html', source: index.source });
      for (const page of demoPages) {
        this.emitFile({ type: 'asset', fileName: `${page}/index.html`, source: index.source });
      }
    }
  };
}

export default defineConfig(({ mode }) => {
  const demo = mode === 'demo';
  return {
    plugins: [...tailwindcss(), ...svelte(), ...(demo ? [liveDemo()] : [])],
    ...(demo ? { base: '/homedex/', build: { outDir: 'dist-demo' } } : {}),
    server: {
      host: '0.0.0.0',
      allowedHosts: true,
      proxy: { '/api': 'http://localhost:7377' }
    }
  };
});
