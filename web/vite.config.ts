import { defineConfig } from "vite";
import react from "@vitejs/plugin-react";
import path from "node:path";

// Sprint 2.1: Web SPA bundling. Vite dev server runs on :5173 and proxies
// /api/* to the Go backend (:8080 by default). `npm run build` emits static
// assets under web/dist/ which the backend can serve (Sprint 2.1 follow-up).
export default defineConfig({
  plugins: [react()],
  resolve: {
    alias: {
      "@": path.resolve(__dirname, "./src"),
    },
  },
  server: {
    port: 5173,
    strictPort: true,
    proxy: {
      "/api": {
        target: process.env.VITE_BACKEND ?? "http://127.0.0.1:8080",
        changeOrigin: true,
        // /api/v1/ws/* would need ws:true; not needed for the current REST surface.
      },
    },
  },
  build: {
    outDir: "dist",
    sourcemap: true,
    target: "es2022",
  },
});