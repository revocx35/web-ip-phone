import { defineConfig } from 'vite';

// JSX is compiled by Vite itself (tsconfig: jsx react-jsx, jsxImportSource preact),
// so no framework plugin is needed.
export default defineConfig({
  build: {
    outDir: 'dist',
    emptyOutDir: true,
    assetsDir: 'assets',
    sourcemap: false,
    // No inline scripts/styles: the server's CSP only allows same-origin files.
    assetsInlineLimit: 0,
    modulePreload: { polyfill: false },
  },
  server: {
    proxy: {
      '/api': { target: 'https://127.0.0.1:8443', secure: false, ws: true },
    },
  },
});
