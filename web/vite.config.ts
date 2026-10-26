import { fileURLToPath, URL } from "node:url";
import tailwindcss from "@tailwindcss/vite";
import { tanstackRouter } from "@tanstack/router-plugin/vite";
import react from "@vitejs/plugin-react";
import { defineConfig } from "vite";

// `--mode mockup` produces one JS and one CSS file, which scripts/artifact.mjs
// stitches into a single self-contained HTML page.
export default defineConfig(({ mode }) => {
  const mockup = mode === "mockup";
  return {
    base: "./",
    plugins: [
      tanstackRouter({ target: "react", autoCodeSplitting: !mockup }),
      react(),
      tailwindcss(),
    ],
    resolve: {
      alias: { "@": fileURLToPath(new URL("./src", import.meta.url)) },
    },
    build: {
      outDir: mockup ? "dist-mockup" : "dist",
      cssCodeSplit: !mockup,
      rollupOptions: mockup ? { output: { inlineDynamicImports: true } } : {},
    },
  };
});
