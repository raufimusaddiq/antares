/// <reference types="vitest/config" />
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

// @openuidev/react-ui asks Google's favicon service for every source a reply cites. The page's CSP
// (img-src 'self' blob:) refuses those images, so each one is a blocked request and a console error, and
// the request itself tells Google which pages the reader's answer cites. The library already falls back to
// its own globe icon when a source has no favicon URL, so the build empties the two places that build one.
// Each rewrite must match exactly once or the build fails, so a library upgrade cannot silently bring the
// third-party request back.
function stripThirdPartyFavicons(): Plugin {
  const rewrites: [string, string][] = [
    ["return `https://www.google.com/s2/favicons?sz=128&domain=${new URL(url).hostname}`;", 'return "";'],
  ];
  return {
    name: "strip-third-party-favicons",
    enforce: "pre",
    transform(code, id) {
      const path = id.replace(/\\/g, "/");
      if (!path.includes("react-ui/dist/genui-lib/index.mjs")) return null;
      let out = code;
      for (const [from, to] of rewrites) {
        if (out.split(from).length !== 2) this.error(`strip-third-party-favicons: expected exactly one "${from}" in ${path}; the library changed, review its favicon requests again`);
        out = out.replace(from, to);
      }
      return { code: out, map: null };
    },
  };
}

// The sources carousel's two arrow buttons are icon-only and carry no accessible name, so a screen reader
// announces "button" twice. IconButton spreads its props onto the <button>, so the build names them.
function labelSourceCarousel(): Plugin {
  const rewrites: [string, string][] = [
    ['onClick: () => ref.current?.scroll("left"),\n\t\t\t\t\tdisabled: !scroll.left,', '"aria-label": "Scroll sources left",\n\t\t\t\t\tonClick: () => ref.current?.scroll("left"),\n\t\t\t\t\tdisabled: !scroll.left,'],
    ['onClick: () => ref.current?.scroll("right"),\n\t\t\t\t\tdisabled: !scroll.right,', '"aria-label": "Scroll sources right",\n\t\t\t\t\tonClick: () => ref.current?.scroll("right"),\n\t\t\t\t\tdisabled: !scroll.right,'],
  ];
  return {
    name: "label-source-carousel",
    enforce: "pre",
    transform(code, id) {
      const path = id.replace(/\\/g, "/");
      if (!path.includes("react-ui/dist/genui-lib/index.mjs")) return null;
      let out = code;
      for (const [from, to] of rewrites) {
        if (out.split(from).length !== 2) this.error(`label-source-carousel: expected exactly one "${from.split("\n")[0]}" in ${path}; the library changed, review the carousel again`);
        out = out.replace(from, to);
      }
      return { code: out, map: null };
    },
  };
}

// @openuidev/react-ui's TextContentWrapper imports rehype-katex on mount, so every rich reply
// downloads KaTeX (259 KB) even when it holds no math. The build makes that import conditional on
// the text actually carrying math ($$…$$, \(…\) or \[…\]) and re-runs it when the text changes, so a
// streaming reply that gains math still gets it. Each rewrite must match exactly once or the build
// fails, so a library upgrade cannot silently bring the unconditional import back.
function loadKatexOnlyForMath(): Plugin {
  const from = String.raw`	const [rehypeKatex, setRehypeKatex] = useState(null);
	useEffect(() => {
		let cancelled = false;
		import("rehype-katex").then((m) => {
			if (!cancelled) setRehypeKatex(() => m.default);
		});
		return () => {
			cancelled = true;
		};
	}, []);`;
  const to = String.raw`	const [rehypeKatex, setRehypeKatex] = useState(null);
	useEffect(() => {
		let cancelled = false;
		const text = props.textMarkdown ?? "";
		if (text.includes("$$") || text.includes("\\(") || text.includes("\\[")) import("rehype-katex").then((m) => {
			if (!cancelled) setRehypeKatex(() => m.default);
		});
		return () => {
			cancelled = true;
		};
	}, [props.textMarkdown]);`;
  return {
    name: "load-katex-only-for-math",
    enforce: "pre",
    transform(code, id) {
      if (!id.replace(/\\/g, "/").includes("react-ui/dist/genui-lib/index.mjs")) return null;
      if (code.split(from).length !== 2) this.error("load-katex-only-for-math: the library's TextContentWrapper changed; review its KaTeX import again");
      // A function replacer: a string replacement would read the "$$" in the guard as an escaped "$".
      return { code: code.replace(from, () => to), map: null };
    },
  };
}

// The bundle is built into the directory the Go binary embeds (internal/webui),
// so `go build` needs no Node. Production never runs Node: this is build-time only.
export default defineConfig({
  plugins: [react(), stripOpenUITelemetry(), stripThirdPartyFavicons(), labelSourceCarousel(), loadKatexOnlyForMath()],
  build: {
    outDir: "../internal/webui/dist",
    emptyOutDir: true,
    sourcemap: false,
  },
  server: { proxy: { "/api": "http://localhost:8080" } },
  // Every test file starts clean: rendered trees unmounted (src/testSetup.ts) and stubbed globals restored.
  // "list" runs afterEach hooks in the order written, so the setup file's unmount comes before a
  // test file's mockReset: a still-mounted chat must not call a mock that was just reset.
  test: { setupFiles: ["./src/testSetup.ts"], unstubGlobals: true, sequence: { hooks: "list" } },
});
