import { defineConfig, type Plugin } from 'vite';
import { svelte } from '@sveltejs/vite-plugin-svelte';
import tailwindcss from '@tailwindcss/vite';

// `npm run build:demo` builds the static live demo: the same app with
// fabricated data and no backend (see src/lib/demoMode.ts), served from a
// GitHub Pages project path. It writes to dist-demo and never touches dist or
// the assets embedded in the Go binary. A fork can pass `-- --base /name/`.
function liveDemo(): Plugin {
  return {
    name: 'homedex-live-demo',
    apply: 'build',
    enforce: 'post',
    transformIndexHtml(html) {
      const description = 'Homedex builds a searchable inventory of homelab services, hosts, ports, routes, and expiry dates from Docker and your reverse proxy. This live demo runs on fabricated data.';
      return {
        html: html.replace(/<title>[^<]*<\/title>/, '<title>Homedex live demo · What runs where</title>'),
        tags: [
          { tag: 'meta', attrs: { property: 'og:title', content: 'Homedex live demo' }, injectTo: 'head' },
          { tag: 'meta', attrs: { property: 'og:description', content: description }, injectTo: 'head' },
          { tag: 'meta', attrs: { property: 'og:image', content: 'https://raw.githubusercontent.com/HarshShah0203/homedex/main/docs/social-card.png' }, injectTo: 'head' }
        ]
      };
    },
    generateBundle(_options, bundle) {
      // GitHub Pages answers unknown paths with 404.html. Making it a copy of
      // the app lets a deep link such as /homedex/routes/7 survive a refresh.
      const index = bundle['index.html'];
      if (!index || index.type !== 'asset') throw new Error('live demo: index.html was not emitted');
      this.emitFile({ type: 'asset', fileName: '404.html', source: index.source });
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
