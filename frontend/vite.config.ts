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
