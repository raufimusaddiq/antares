import { afterEach, describe, expect, it, vi } from "vitest";
import { renderToStaticMarkup } from "react-dom/server";
import { AssistantTools, CapabilitiesScreen, CapabilitiesTable, AdminScreens } from "./Admin";
import { getCapabilities, offers, parseInventory } from "./chatApi";
import { parseAdminPath } from "./route";

afterEach(() => vi.unstubAllGlobals());

const body = {
  capabilities: [
    { id: "skills", label: "Skills", status: "available" },
    { id: "settings", label: "Change Hermes settings", status: "unsupported", reason: "Hermes does not report admin_config_rw as enabled" },
    { id: "tool_progress", label: "Live tool progress", status: "enabled" },
  ],
  toolsets: [
    { name: "web", status: "available", tools: ["web_search"] },
    { name: "browser", status: "unsupported", reason: "Not enabled on this Hermes", tools: [] },
  ],
  providers: [{ slug: "example-ai", name: "Example AI", models: ["m1"], totalModels: 1 }],
  skills: [{ name: "s1", description: "d", category: "" }],
};

describe("capability discovery on the client", () => {
  it("only the exact word 'available' offers a capability", () => {
    const inv = parseInventory(body)!;
    expect(offers(inv, "skills")).toBe(true);
    expect(offers(inv, "settings")).toBe(false);
    expect(offers(inv, "tool_progress")).toBe(false); // an unknown status word is not availability
    expect(offers(inv, "never_reported")).toBe(false);
    expect(offers(null, "skills")).toBe(false);
  });

  it("a row it cannot read makes the answer unreadable rather than shorter", () => {
    expect(parseInventory({ ...body, capabilities: [{ label: "no id", status: "available" }] })).toBeNull();
    expect(parseInventory({ ...body, toolsets: "x" })).toBeNull();
    expect(parseInventory(null)).toBeNull();
  });

  it("a failed or unreadable response yields no inventory, so nothing is offered", async () => {
    vi.stubGlobal("fetch", vi.fn(async () => new Response("{}", { status: 200 })));
    expect((await getCapabilities()).ok).toBe(false);
    vi.stubGlobal("fetch", vi.fn(async () => new Response(JSON.stringify({ error: { code: "TENANT_NOT_READY" } }), { status: 409 })));
    const r = await getCapabilities();
    expect(r.ok).toBe(false);
  });

  it("the admin table labels unsupported capabilities as such, with the reason, and never as available", () => {
    const html = renderToStaticMarkup(<CapabilitiesTable inv={parseInventory(body)!} />);
    const settingsRow = html.split("<tr>").find((r) => r.includes("Change Hermes settings"))!;
    expect(settingsRow).toContain("Unsupported");
    expect(settingsRow).not.toContain("Available");
    expect(settingsRow).toContain("admin_config_rw");
    const unknown = html.split("<tr>").find((r) => r.includes("Live tool progress"))!;
    expect(unknown).toContain("Unsupported");
    const browser = html.split("<tr>").find((r) => r.includes("browser"))!;
    expect(browser).toContain("Not enabled on this Hermes");
    expect(browser).not.toContain("Available");
  });

  it("is routed and starts in a labelled loading state", () => {
    expect(parseAdminPath("/admin/capabilities")).toEqual({ kind: "capabilities" });
    expect(renderToStaticMarkup(<AdminScreens route={{ kind: "capabilities" }} onNavigate={() => {}} />)).toContain("Loading");
  });

  it("the page has one h1 and its sections are h2, so Assistant tools reads as a section", () => {
    const page = renderToStaticMarkup(<CapabilitiesScreen />);
    expect(page.match(/<h1/g)).toHaveLength(1);
    expect(page).toContain(">Capabilities</h1>");
    expect(renderToStaticMarkup(<CapabilitiesTable inv={parseInventory(body)!} />)).toMatch(/^<section[^>]*><h2/);
  });

  it("Assistant tools uses the same switch as Models and Writing rules", () => {
    const policy = { tools: [{ name: "web", label: "Web", description: "", state: "on" as const, reason: "" }], custom: true, reverifying: null };
    const html = renderToStaticMarkup(<AssistantTools policy={policy} onSave={async () => ({ ok: true as const, value: policy })} />);
    expect(html).toContain('role="switch"');
    expect(html).toContain('class="switch"');
    expect(html).not.toContain('type="checkbox" class="tool-check"');
  });
});

import golden from "./testdata/capabilities-empty.test.json";

describe("server-to-client: empty catalogs", () => {
  it("the exact answer for empty/unreadable catalogs (also asserted byte-for-byte by the Go test) parses and offers only what is available", () => {
    const inv = parseInventory(golden);
    expect(inv).not.toBeNull();
    expect(inv!.providers).toEqual([]);
    expect(offers(inv, "model_lock")).toBe(true);
    expect(offers(inv, "models")).toBe(false);
    expect(offers(inv, "skills")).toBe(false);
  });

  it("a null list is still an unreadable answer, so the server must never send one", () => {
    expect(parseInventory({ ...golden, providers: null })).toBeNull();
  });
});
