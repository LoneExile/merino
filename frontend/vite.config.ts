import { defineConfig } from "vite";
import react from "@vitejs/plugin-react";
import wails from "@wailsio/runtime/plugins/vite";

// https://vitejs.dev/config/
export default defineConfig({
  server: {
    host: "127.0.0.1",
    port: Number(process.env.WAILS_VITE_PORT) || 9245,
    strictPort: true,
  },
  plugins: [react(), wails("./bindings")],
  build: {
    // Written into internal/assets rather than frontend/dist because
    // //go:embed cannot reach upward out of its own package directory: a
    // binary under cmd/ could never embed ../../frontend/dist. One package
    // owns the embed so every entry point serves a byte-identical UI.
    //
    // emptyOutDir is explicit because the directory now sits outside vite's
    // root, and vite refuses to clear such a directory without being told.
    outDir: "../internal/assets/dist",
    emptyOutDir: true,
    // Keep @wailsio/runtime out of a cycle with the dynamic wailsClient
    // chunk. A cycle left the nanoid alphabet undefined at module init:
    //   undefined is not an object (evaluating 'a[Math.random()*64|0]')
    // which blanked the whole menubar panel.
    rollupOptions: {
      output: {
        manualChunks(id) {
          if (id.includes("node_modules/@wailsio/runtime") || id.includes("@wailsio/runtime/")) {
            return "wails-runtime";
          }
        },
      },
    },
  },
});
