import { defineConfig, type Plugin } from "vite";
import react from "@vitejs/plugin-react";

// Strict CSP for the desktop app, injected at build time only (the dev server
// needs an inline script for React Fast Refresh). Mobile builds
// (TERMWARD_TARGET=mobile) skip it: Capacitor's native bridge injects its own
// script into the page.
const csp: Plugin = {
  name: "termward-csp",
  apply: (_config, env) => env.command === "build" && process.env.TERMWARD_TARGET !== "mobile",
  transformIndexHtml: () => [
    {
      tag: "meta",
      injectTo: "head-prepend",
      attrs: {
        "http-equiv": "Content-Security-Policy",
        content: [
          "default-src 'self'",
          "script-src 'self'",
          "style-src 'self' 'unsafe-inline'",
          "font-src 'self' data:",
          "img-src 'self' data:",
          "connect-src http://127.0.0.1:* ws://127.0.0.1:*",
        ].join("; "),
      },
    },
  ],
};

// The packaged apps load dist/index.html from disk, so asset URLs must be relative.
export default defineConfig({
  base: "./",
  plugins: [react(), csp],
  server: {
    port: 5173,
    strictPort: true,
    // Build outputs and native projects: watching them locks files on Windows.
    watch: { ignored: ["**/release/**", "**/android/**", "**/ios/**", "**/dist-electron/**"] },
  },
  build: {
    outDir: "dist",
    emptyOutDir: true,
    // Chromium in Electron and Android System WebView; Safari 16 on iOS.
    target: ["chrome110", "safari16"],
    chunkSizeWarningLimit: 1500,
  },
});
