import { fileURLToPath } from "node:url";
import { readFileSync } from "node:fs";
import react from "@vitejs/plugin-react";
import tailwindcss from "@tailwindcss/vite";
import { defineConfig } from "vite-plus";

// Development: serve the UI over HTTPS so the control plane's Secure cookie and
// exact Origin check work. Start the control plane with --origin set to this server.
const devCert = process.env.AMUOS_DEV_TLS_CERT;
const devKey = process.env.AMUOS_DEV_TLS_KEY;
const controlPlane = process.env.AMUOS_DEV_CONTROL_PLANE ?? "https://127.0.0.1:8443";

export default defineConfig({
  plugins: [react(), tailwindcss()],
  resolve: {
    alias: { "@": fileURLToPath(new URL("./src", import.meta.url)) },
  },
  // The control plane replaces this placeholder with a fresh nonce per response.
  html: { cspNonce: "AMUOS_CSP_NONCE" },
  build: {
    outDir: "../internal/webui/static/app",
    emptyOutDir: true,
    assetsInlineLimit: 0,
    modulePreload: { polyfill: false },
    // Licenses of bundled dependencies, collected into release archives.
    license: { fileName: ".vite/license.md" },
  },
  server: {
    https:
      devCert && devKey ? { cert: readFileSync(devCert), key: readFileSync(devKey) } : undefined,
    proxy: {
      "/api": { target: controlPlane, secure: false },
    },
  },
  lint: {
    ignorePatterns: ["playwright-report/**", "test-results/**"],
    options: { typeAware: true, typeCheck: true },
  },
  fmt: {
    ignorePatterns: ["playwright-report/**", "test-results/**", "pnpm-lock.yaml"],
  },
  test: {
    include: ["src/**/*.test.ts"],
  },
});
