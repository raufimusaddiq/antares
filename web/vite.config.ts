import { defineConfig } from "vite";
import react from "@vitejs/plugin-react";

// The bundle is built into the directory the Go binary embeds (internal/webui),
// so `go build` needs no Node. Production never runs Node: this is build-time only.
export default defineConfig({
  plugins: [react()],
  build: {
    outDir: "../internal/webui/dist",
    emptyOutDir: true,
    sourcemap: false,
  },
  server: { proxy: { "/api": "http://localhost:8080" } },
});
