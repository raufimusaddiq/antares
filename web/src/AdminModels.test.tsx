// @vitest-environment jsdom
import { cleanup, fireEvent, render, screen, waitFor, within } from "@testing-library/react";
import { afterEach, describe, expect, it, vi } from "vitest";
import { AdminScreens } from "./Admin";
import { offersFor } from "./AdminModels";
import { parseModels } from "./adminModelsApi";
import { parseAdminPath } from "./route";

afterEach(() => {
  cleanup();
  vi.unstubAllGlobals();
});

const json = (status: number, body: unknown) => new Response(JSON.stringify(body), { status, headers: { "content-type": "application/json" } });
const purposes = (over: Record<string, unknown> = {}) => ({
  chat: { current: "", effective: "", warning: "" },
  summary: { current: "gpt-oss-20b", effective: "gpt-oss-20b", warning: "" },
  blog_assistant: { current: "", effective: "", warning: "" },
  image: { current: "", effective: "", warning: "" },
  video: { current: "", effective: "", warning: "" },
  ...over,
});
const text = ["chat", "summary", "blog_assistant"];
const view = (over: Record<string, unknown> = {}) => ({
  catalogue: "ok",
  models: [
    { id: "deepseek", name: "DeepSeek", on: true, purposes: text, defaultFor: [], usage: { calls: 812, tokens: 9_400_000 } },
    { id: "gpt-oss-20b", name: "GPT OSS 20B", on: true, purposes: text, defaultFor: ["summary"], usage: null },
    { id: "glm", name: "GLM", on: false, purposes: text, defaultFor: [], usage: { calls: 96, tokens: 1_100_000 } },
    { id: "agnes", name: "Agnes Image", on: true, purposes: ["image"], defaultFor: ["image"], usage: null },
  ],
  purposes: purposes(),
  ...over,
});

/** The Models routes, scripted; every call is recorded so a test can say what was sent. */
function server(o: { read?: () => unknown | number; policy?: (b: unknown) => Response; defaults?: (b: unknown) => Response; media?: (b: unknown) => Response } = {}) {
  const calls: { method: string; path: string; body: unknown }[] = [];
  vi.stubGlobal(
    "fetch",
    vi.fn(async (input: RequestInfo | URL, init?: RequestInit) => {
      const u = new URL(String(input), "https://antares.test");
      const body = init?.body ? JSON.parse(String(init.body)) : undefined;
      calls.push({ method: init?.method ?? "GET", path: u.pathname, body });
      if (u.pathname === "/api/admin/models") {
        const r = o.read ? o.read() : view();
        return typeof r === "number" ? json(r, { error: { code: "INTERNAL" } }) : json(200, r);
      }
      if (u.pathname === "/api/admin/model-policy") return o.policy ? o.policy(body) : json(200, { blocked: (body as { blocked: string[] }).blocked });
      if (u.pathname === "/api/admin/model-defaults") return o.defaults ? o.defaults(body) : json(200, view());
      if (u.pathname === "/api/admin/media-models") return o.media ? o.media(body) : json(200, {});
      return json(404, {});
    }),
  );
  return calls;
}

describe("Admin → Models", () => {
  it("lists every model with its switch, its defaults and its usage, never per person", async () => {
    server();
    render(<AdminScreens route={{ kind: "models" }} onNavigate={vi.fn()} />);
    expect(await screen.findByRole("rowheader", { name: "DeepSeek" })).toBeTruthy();
    expect((screen.getByLabelText("DeepSeek for everyone") as HTMLInputElement).checked).toBe(true);
    expect((screen.getByLabelText("GLM for everyone") as HTMLInputElement).checked).toBe(false);
    expect(screen.getByText("812 calls · 9.4M tokens")).toBeTruthy();
    expect(screen.getByText("Summaries and titles", { selector: "td" })).toBeTruthy();
    expect(document.body.textContent).toContain("never per person");
  });

  it("switches a model off by sending the list of models that are off, and reads again", async () => {
    const calls = server();
    render(<AdminScreens route={{ kind: "models" }} onNavigate={vi.fn()} />);
    fireEvent.click(await screen.findByLabelText("DeepSeek for everyone"));
    await waitFor(() => expect(calls.find((c) => c.method === "PUT")?.body).toEqual({ blocked: ["glm", "deepseek"] }));
    await waitFor(() => expect(calls.filter((c) => c.path === "/api/admin/models").length).toBe(2));
  });

  it("says which purposes depend on a model it refuses to switch off, and changes nothing", async () => {
    server({ policy: () => json(409, { error: { code: "MODEL_IS_DEFAULT", purposes: ["summary"] } }) });
    render(<AdminScreens route={{ kind: "models" }} onNavigate={vi.fn()} />);
    fireEvent.click(await screen.findByLabelText("GPT OSS 20B for everyone"));
    const alert = await screen.findByRole("alert");
    expect(alert.textContent).toContain("GPT OSS 20B was not switched off");
    expect(alert.textContent).toContain("Summaries and titles");
    expect((screen.getByLabelText("GPT OSS 20B for everyone") as HTMLInputElement).checked).toBe(true);
  });

  it("offers each purpose only models that are on and made for it, plus Service default", async () => {
    server();
    render(<AdminScreens route={{ kind: "models" }} onNavigate={vi.fn()} />);
    await screen.findByRole("rowheader", { name: "DeepSeek" });
    const chat = screen.getByLabelText("Chat") as HTMLSelectElement;
    expect([...chat.options].map((o) => o.textContent)).toEqual(["Service default", "DeepSeek", "GPT OSS 20B"]);
    const image = screen.getByLabelText("Images") as HTMLSelectElement;
    expect([...image.options].map((o) => o.textContent)).toEqual(["Service default", "Agnes Image"]);
    expect((screen.getByLabelText("Summaries and titles") as HTMLSelectElement).value).toBe("gpt-oss-20b");
  });

  it("saves text defaults and media defaults through their own routes, only what changed", async () => {
    const calls = server();
    render(<AdminScreens route={{ kind: "models" }} onNavigate={vi.fn()} />);
    await screen.findByRole("rowheader", { name: "DeepSeek" });
    const save = screen.getByRole("button", { name: "Save defaults" }) as HTMLButtonElement;
    expect(save.disabled).toBe(true);
    fireEvent.change(screen.getByLabelText("Chat"), { target: { value: "deepseek" } });
    fireEvent.change(screen.getByLabelText("Images"), { target: { value: "agnes" } });
    expect(screen.getByText("Not saved yet.")).toBeTruthy();
    fireEvent.click(save);
    await waitFor(() => expect(calls.find((c) => c.path === "/api/admin/model-defaults")?.body).toEqual({ chat: "deepseek" }));
    await waitFor(() => expect(calls.find((c) => c.path === "/api/admin/media-models")?.body).toEqual({ image: "agnes", video: "" }));
  });

  it("warns that a saved default is no longer in use, and keeps showing the saved choice", async () => {
    server({ read: () => view({ purposes: purposes({ blog_assistant: { current: "kimi-k2", effective: "", warning: "not_listed" } }) }) });
    render(<AdminScreens route={{ kind: "models" }} onNavigate={vi.fn()} />);
    expect(await screen.findByText(/Not offered any more. The service default is in use./)).toBeTruthy();
    const sel = screen.getByLabelText("Blog assistant") as HTMLSelectElement;
    expect(sel.value).toBe("kimi-k2");
    expect(within(sel).getByText("kimi-k2 (not offered now)")).toBeTruthy();
  });

  it("says plainly when the model list cannot be read, keeps what is saved, and offers a retry", async () => {
    server({ read: () => view({ catalogue: "unavailable", models: [] }) });
    render(<AdminScreens route={{ kind: "models" }} onNavigate={vi.fn()} />);
    expect(await screen.findByText("The model list could not be read just now.")).toBeTruthy();
    expect((screen.getByRole("button", { name: "Save defaults" }) as HTMLButtonElement).disabled).toBe(true);
    expect(screen.getByRole("button", { name: "Try again" })).toBeTruthy();
  });

  it("shows a refused save as an error and does not claim it was saved", async () => {
    server({ defaults: () => json(400, { error: { code: "INVALID_REQUEST" } }) });
    render(<AdminScreens route={{ kind: "models" }} onNavigate={vi.fn()} />);
    await screen.findByRole("rowheader", { name: "DeepSeek" });
    fireEvent.change(screen.getByLabelText("Chat"), { target: { value: "deepseek" } });
    fireEvent.click(screen.getByRole("button", { name: "Save defaults" }));
    expect((await screen.findByRole("alert")).textContent).toContain("refused");
    expect(screen.queryByText("Saved.")).toBeNull();
  });
});

describe("the models reader", () => {
  it("refuses a read it cannot understand instead of guessing", () => {
    expect(parseModels({ catalogue: "ok", models: [{ nonsense: 1 }], purposes: purposes() })).toBeNull();
    expect(parseModels({ catalogue: "maybe", models: [], purposes: purposes() })).toBeNull();
    expect(parseModels({ catalogue: "ok", models: [], purposes: { chat: {} } })).toBeNull();
  });
  it("offersFor keeps a saved choice that is not offered now", () => {
    const v = parseModels(view({ purposes: purposes({ chat: { current: "gone", effective: "", warning: "not_listed" } }) }))!;
    expect(offersFor(v, "chat").map((o) => o.id)).toEqual(["deepseek", "gpt-oss-20b", "gone"]);
  });
  it("routes /admin/models", () => {
    expect(parseAdminPath("/admin/models")).toEqual({ kind: "models" });
  });
});
