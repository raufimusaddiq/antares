import { defineConfig, type Plugin } from "vite";
import react from "@vitejs/plugin-react";

// @openuidev/lang-core ships opt-in usage telemetry (a PostHog key, host and sender).
// It is off unless OPENUI_RUNTIME_TELEMETRY_ENABLED is set and our CSP would block it,
// but "nothing credential-shaped reaches the browser" is checked on the bundle
// (internal/webui/webui_test.go), and a vendor analytics key does not belong in it. So
// the build removes the sender instead of allow-listing it: the entry points that
// start a capture, and the sender itself, become no-ops and the rest is tree-shaken. Each rewrite must match
// exactly once or the build fails, so a library upgrade cannot silently reintroduce it.
function stripOpenUITelemetry(): Plugin {
  const rewrites: Record<string, [string, string][]> = {
    "lang-core/dist/shared-": [
      ["function isRuntimeTelemetryEnabled(env) {", "function isRuntimeTelemetryEnabled(env) { return false;"],
      ["function getPostHogConfig(env) {", "function getPostHogConfig(env) { return void 0;"],
    ],
    "lang-core/dist/index.mjs": [
      ["function recordSystemPromptGeneration(spec, inputShape) {", "function recordSystemPromptGeneration(spec, inputShape) { return;"],
      ["function prepareParserParseTelemetry() {", "function prepareParserParseTelemetry() { return void 0;"],
      ["async function postCapture(event, properties, runtime) {", "async function postCapture(event, properties, runtime) { return;"],
    ],
  };
  return {
    name: "strip-openui-telemetry",
    enforce: "pre",
    transform(code, id) {
      const path = id.replace(/\\/g, "/");
      for (const [file, list] of Object.entries(rewrites)) {
        if (!path.includes(file) || !path.endsWith(".mjs")) continue;
        let out = code;
        for (const [from, to] of list) {
          if (out.split(from).length !== 2) this.error(`strip-openui-telemetry: expected exactly one "${from}" in ${path}; the library changed, review its telemetry again`);
          out = out.replace(from, to);
        }
        return { code: out, map: null };
      }
      return null;
    },
  };
}

// The bundle is built into the directory the Go binary embeds (internal/webui),
// so `go build` needs no Node. Production never runs Node: this is build-time only.
export default defineConfig({
  plugins: [react(), stripOpenUITelemetry()],
  build: {
    outDir: "../internal/webui/dist",
    emptyOutDir: true,
    sourcemap: false,
  },
  server: { proxy: { "/api": "http://localhost:8080" } },
});
