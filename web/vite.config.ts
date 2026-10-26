/// <reference types="vitest/config" />
import { fileURLToPath, URL } from "node:url";
import tailwindcss from "@tailwindcss/vite";
import { tanstackRouter } from "@tanstack/router-plugin/vite";
import react from "@vitejs/plugin-react";
import { defineConfig } from "vite";

// Modes:
//   development (pnpm dev)      mock API through the MSW service worker; VITE_API_MODE=real proxies to :8080
//   production  (pnpm build)    real API
//   mockup      (build:mockup)  one self-contained HTML page with the mock API running in-process
export default defineConfig(({ mode }) => {
  const mockup = mode === "mockup";
  return {
    base: mockup ? "./" : "/",
    plugins: [
      tanstackRouter({ target: "react", autoCodeSplitting: !mockup, quoteStyle: "double" }),
      react(),
      tailwindcss(),
    ],
    resolve: {
      alias: { "@": fileURLToPath(new URL("./src", import.meta.url)) },
    },
    server: {
      proxy: {
        "/api": { target: "http://localhost:8080" },
      },
    },
    build: {
      outDir: mockup ? "dist-mockup" : "dist",
      cssCodeSplit: !mockup,
      rollupOptions: mockup ? { output: { inlineDynamicImports: true } } : {},
    },
    test: {
      environment: "jsdom",
      setupFiles: ["./src/test/setup.ts"],
      include: ["src/**/*.test.{ts,tsx}"],
      css: false,
      restoreMocks: true,
    },
  };
});
