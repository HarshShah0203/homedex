import { configDefaults, defineConfig } from 'vitest/config';
import { svelte } from '@sveltejs/vite-plugin-svelte';

export default defineConfig({
  plugins: [...svelte()],
  resolve: { conditions: ['browser'] },
  test: { environment: 'jsdom', setupFiles: ['./src/test-setup.ts'], exclude: [...configDefaults.exclude, 'tests/**'] }
});
