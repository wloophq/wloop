import { svelte } from "@sveltejs/vite-plugin-svelte";
import { defineConfig } from "vitest/config";

export default defineConfig({
  plugins: [svelte()],
  build: {
    outDir: "../static/app",
    emptyOutDir: true,
    sourcemap: false,
    minify: true,
    cssCodeSplit: false,
    lib: {
      entry: "src/main.ts",
      formats: ["iife"],
      name: "cockpit",
      fileName: () => "app.js",
    },
    rolldownOptions: {
      output: { codeSplitting: false },
    },
  },
  resolve: process.env.VITEST ? { conditions: ["browser"] } : undefined,
  test: {
    environment: "happy-dom",
    include: ["src/**/*.test.ts"],
    testTimeout: 30000,
    hookTimeout: 30000,
  },
});
