// @vitest-environment jsdom
import { afterEach, describe, expect, it, vi } from "vitest";
import { act } from "react";
import { createRoot } from "react-dom/client";
import { describeQuota, parseModels } from "./chatApi";
import { ModelPicker } from "./ModelPicker";

(globalThis as unknown as { IS_REACT_ACT_ENVIRONMENT: boolean }).IS_REACT_ACT_ENVIRONMENT = true;

const catalog = {
  fetchedAt: "t",
  default: { provider: "alpha", model: "m-reason" },
  reasoningEfforts: ["low", "high"],
  providers: [
    {
      slug: "alpha", name: "Alpha", available: true,
      models: [
        { id: "m-reason", available: true, reasoning: true, serviceTier: true, price: null },
        { id: "m-plain", available: true, reasoning: false, serviceTier: null, price: null },
      ],
      quota: { state: "AVAILABLE", windows: [{ label: "5h", usedPercent: 0, resetsAt: "", scope: "account" }] },
    },
    { slug: "beta", name: "Beta", available: false, reason: "paste KEY", models: [{ id: "b1", available: false, reasoning: null, serviceTier: null, price: null }], quota: { state: "PROVIDER_USAGE_UNAVAILABLE" } },
  ],
};

describe("parseModels", () => {
  it("keeps unreported values null and unavailable models unavailable", () => {
    const c = parseModels(catalog)!;
    expect(c.providers[1].models[0]).toMatchObject({ available: false, reasoning: null, priority: null });
    expect(c.providers[0].models[1].priority).toBeNull();
    expect(c.providers[1].quota).toBeNull();
  });
  it("rejects what is not a catalog", () => {
    expect(parseModels({})).toBeNull();
    expect(parseModels(null)).toBeNull();
  });
  it("states missing provider usage and shows a real 0% as 0%", () => {
    const c = parseModels(catalog)!;
    expect(describeQuota(c.providers[1])[0]).toContain("unavailable");
    expect(describeQuota(c.providers[0])[0]).toContain("0% used");
  });
});

function respond(map: Record<string, unknown>) {
  vi.stubGlobal("fetch", vi.fn(async (path: string, init?: RequestInit) => {
    const key = `${init?.method ?? "GET"} ${path.split("?")[0]}`;
    const body = key in map ? map[key] : null;
    return new Response(JSON.stringify(body), { status: body === null ? 404 : 200, headers: { "Content-Type": "application/json" } });
  }));
}

afterEach(() => {
  vi.unstubAllGlobals();
  document.body.innerHTML = "";
});

describe("ModelPicker", () => {
  it("disables a model the assistant reports unavailable, and shows controls only where supported", async () => {
    respond({ "GET /api/models": catalog, "GET /api/chats/c1": { id: "c1", model: { provider: "alpha", model: "m-plain" } } });
    const el = document.createElement("div");
    document.body.append(el);
    await act(async () => createRoot(el).render(<ModelPicker chatId="c1" disabled={false} />));
    await act(async () => { el.querySelector("details")!.open = true; el.querySelector("details")!.dispatchEvent(new Event("toggle")); });
    const radios = [...el.querySelectorAll<HTMLInputElement>("input[type=radio]")];
    expect(radios.map((r) => [r.disabled, r.checked])).toEqual([[false, false], [false, true], [true, false]]);
    expect(el.textContent).toContain("Unavailable"); // context window, never inferred
    expect(el.querySelector("select")).toBeNull(); // m-plain lists no reasoning
    expect(el.querySelector("input[type=checkbox]")).toBeNull(); // and no priority tier
    expect(el.textContent).toContain("Provider usage: unavailable");
  });
});

// ---- races: responses arrive in a different order than the requests were made ----------

import { cleanup, fireEvent, render, screen, waitFor } from "@testing-library/react";

interface Call {
  method: string;
  path: string;
  body: Record<string, unknown> | null;
  resolve: (status: number, body: unknown) => void;
}

/** Every request waits until the test answers it, so the test decides the order of the responses. */
function deferred() {
  const calls: Call[] = [];
  vi.stubGlobal("fetch", vi.fn((path: string, init?: RequestInit) =>
    new Promise<Response>((res) => {
      calls.push({
        method: init?.method ?? "GET",
        path: path.split("?")[0],
        body: init?.body ? JSON.parse(String(init.body)) : null,
        resolve: (status, body) => res(new Response(JSON.stringify(body), { status, headers: { "Content-Type": "application/json" } })),
      });
    }),
  ));
  const find = (method: string, path: string) => calls.filter((c) => c.method === method && c.path === path);
  return { calls, find };
}

/** The read is queued behind the chat's pending writes, so it appears asynchronously: wait for it, then answer. */
async function answerGet(d: ReturnType<typeof deferred>, path: string, n: number, body: unknown) {
  await waitFor(() => expect(d.find("GET", path).length).toBeGreaterThan(n));
  await act(async () => d.find("GET", path)[n].resolve(200, body));
}

const chatOf = (id: string, model: unknown) => ({ id, model });
const mA = { provider: "alpha", model: "m-reason" };
const mB = { provider: "alpha", model: "m-plain" };

async function open(d: ReturnType<typeof deferred>) {
  const details = document.querySelector("details")!;
  details.open = true;
  fireEvent(details, new Event("toggle"));
  await waitFor(() => expect(d.find("GET", "/api/models").length).toBeGreaterThan(0));
  await act(async () => d.find("GET", "/api/models").at(-1)!.resolve(200, catalog));
}

const checkedModel = () => [...document.querySelectorAll<HTMLInputElement>("input[type=radio]")].filter((r) => r.checked).map((r) => r.parentElement!.textContent!.trim());

describe("ModelPicker races", () => {
  afterEach(() => {
    cleanup();
    vi.unstubAllGlobals();
  });

  it("a save started in chat A that resolves after switching to chat B does not change B", async () => {
    const d = deferred();
    const { rerender } = render(<ModelPicker chatId="A" disabled={false} />);
    await waitFor(() => expect(d.find("GET", "/api/chats/A")).toHaveLength(1));
    await answerGet(d, "/api/chats/A", 0, chatOf("A", null));
    await open(d);

    fireEvent.click(screen.getByLabelText("m-reason"));
    await waitFor(() => expect(d.find("PUT", "/api/chats/A/model")).toHaveLength(1)); // A's save is in flight

    rerender(<ModelPicker chatId="B" disabled={false} />); // the same instance, as when the parent does not key it
    await waitFor(() => expect(d.find("GET", "/api/chats/B")).toHaveLength(1));
    await answerGet(d, "/api/chats/B", 0, chatOf("B", mB));
    expect(checkedModel()).toEqual(["m-plain"]);

    await act(async () => d.find("PUT", "/api/chats/A/model")[0].resolve(200, { chatId: "A", model: mA })); // A's answer arrives late
    expect(checkedModel()).toEqual(["m-plain"]); // B still shows B's own choice

    // B is not overwritten by A's choice: changing B sends B's provider/model to B
    await waitFor(() => expect(screen.queryByRole("checkbox")).toBeNull()); // m-plain lists no priority tier
    fireEvent.click(screen.getByText("Use the default model"));
    await waitFor(() => expect(d.find("PUT", "/api/chats/B/model")).toHaveLength(1));
    expect(d.find("PUT", "/api/chats/B/model")[0].body).toEqual({ reset: true });
    expect(d.find("PUT", "/api/chats/A/model")).toHaveLength(1); // nothing was ever sent to A again
  });

  it("A's late save is not what B's effort change builds on", async () => {
    const d = deferred();
    const { rerender } = render(<ModelPicker chatId="A" disabled={false} />);
    await answerGet(d, "/api/chats/A", 0, chatOf("A", null));
    await open(d);
    fireEvent.click(screen.getByLabelText("m-reason"));
    await waitFor(() => expect(d.find("PUT", "/api/chats/A/model")).toHaveLength(1));
    rerender(<ModelPicker chatId="B" disabled={false} />);
    await waitFor(() => expect(d.find("GET", "/api/chats/B")).toHaveLength(1));
    await answerGet(d, "/api/chats/B", 0, chatOf("B", { ...mA, reasoningEffort: "low" }));
    await act(async () => d.find("PUT", "/api/chats/A/model")[0].resolve(200, { chatId: "A", model: { ...mA, reasoningEffort: "high", serviceTier: "priority" } }));
    fireEvent.change(await screen.findByLabelText(/Reasoning effort/), { target: { value: "high" } });
    await waitFor(() => expect(d.find("PUT", "/api/chats/B/model")).toHaveLength(1));
    // B keeps its own state (no priority tier from A) and only the effort differs
    expect(d.find("PUT", "/api/chats/B/model")[0].body).toEqual({ provider: "alpha", model: "m-reason", reasoningEffort: "high" });
  });

  it("overlapping saves go out in order and only the newest response is applied", async () => {
    const d = deferred();
    render(<ModelPicker chatId="A" disabled={false} />);
    await answerGet(d, "/api/chats/A", 0, chatOf("A", null));
    await open(d);

    fireEvent.click(screen.getByLabelText("m-reason"));
    await waitFor(() => expect(d.find("PUT", "/api/chats/A/model")).toHaveLength(1));
    fireEvent.click(screen.getByLabelText("m-plain")); // second change while the first is still in flight
    await new Promise((r) => setTimeout(r, 20));
    expect(d.find("PUT", "/api/chats/A/model")).toHaveLength(1); // the second waits for the first: no overlap on the wire
    expect(d.find("PUT", "/api/chats/A/model")[0].body).toMatchObject({ model: "m-reason" });

    await act(async () => d.find("PUT", "/api/chats/A/model")[0].resolve(200, { chatId: "A", model: mA }));
    expect(checkedModel()).toEqual([]); // the older answer is not shown as the final state
    await waitFor(() => expect(d.find("PUT", "/api/chats/A/model")).toHaveLength(2));
    expect(d.find("PUT", "/api/chats/A/model")[1].body).toMatchObject({ model: "m-plain" });
    await act(async () => d.find("PUT", "/api/chats/A/model")[1].resolve(200, { chatId: "A", model: mB }));
    expect(checkedModel()).toEqual(["m-plain"]);
  });

  it("a failed newest save shows what the server holds, not the failed choice", async () => {
    const d = deferred();
    render(<ModelPicker chatId="A" disabled={false} />);
    await answerGet(d, "/api/chats/A", 0, chatOf("A", mA));
    await open(d);
    fireEvent.click(screen.getByLabelText("m-plain"));
    await waitFor(() => expect(d.find("PUT", "/api/chats/A/model")).toHaveLength(1));
    await act(async () => d.find("PUT", "/api/chats/A/model")[0].resolve(422, { error: { code: "MODEL_UNAVAILABLE" } }));
    await waitFor(() => expect(d.find("GET", "/api/chats/A")).toHaveLength(2)); // re-read
    await answerGet(d, "/api/chats/A", 1, chatOf("A", mA));
    expect(checkedModel()).toEqual(["m-reason"]);
    expect(screen.getByRole("alert").textContent).toContain("not available");
  });
});

// ---- A -> B -> A with writes still queued -------------------------------------------------

/** A fake server: a PUT takes effect when it ARRIVES (is sent), a read reports the state when it is answered. */
function serverFake(initial: Record<string, unknown>) {
  const server: Record<string, unknown> = { ...initial };
  const arrivals: { chat: string; model: string }[] = [];
  const pending: { method: string; chat: string; answer: () => void }[] = [];
  vi.stubGlobal("fetch", vi.fn((path: string, init?: RequestInit) =>
    new Promise<Response>((res) => {
      const reply = (status: number, body: unknown) => res(new Response(JSON.stringify(body), { status, headers: { "Content-Type": "application/json" } }));
      const method = init?.method ?? "GET";
      const url = path.split("?")[0];
      if (url === "/api/models") return pending.push({ method, chat: "", answer: () => reply(200, catalog) });
      const chat = url.split("/")[3];
      if (method === "PUT") {
        const b = JSON.parse(String(init!.body));
        server[chat] = b.reset ? null : { provider: b.provider, model: b.model, reasoningEffort: b.reasoningEffort, serviceTier: b.serviceTier };
        arrivals.push({ chat, model: b.reset ? "(default)" : b.model });
        return pending.push({ method, chat, answer: () => reply(200, { chatId: chat, model: server[chat] }) });
      }
      pending.push({ method, chat, answer: () => reply(200, chatOf(chat, server[chat])) });
    }),
  ));
  const next = async (method: string, chat: string) => {
    await waitFor(() => expect(pending.some((p) => p.method === method && p.chat === chat)).toBe(true));
    const i = pending.findIndex((p) => p.method === method && p.chat === chat);
    const [p] = pending.splice(i, 1);
    await act(async () => p.answer());
  };
  return { server, arrivals, pending, next };
}

describe("ModelPicker keeps one chat's writes ordered across chat switches", () => {
  afterEach(() => {
    cleanup();
    vi.unstubAllGlobals();
  });

  it("A1,A2 queued, switch to B, back to A, A3: the stale A2 never overwrites the server", async () => {
    const f = serverFake({ A: null, B: mB });
    const { rerender } = render(<ModelPicker chatId="A" disabled={false} />);
    await f.next("GET", "A");
    const details = document.querySelector("details")!;
    details.open = true;
    fireEvent(details, new Event("toggle"));
    await f.next("GET", ""); // /api/models

    fireEvent.click(screen.getByLabelText("m-reason")); // A1: sent, answer withheld
    await waitFor(() => expect(f.arrivals).toEqual([{ chat: "A", model: "m-reason" }]));
    fireEvent.click(screen.getByLabelText("m-plain")); // A2: queued behind A1, not sent yet

    rerender(<ModelPicker chatId="B" disabled={false} />);
    await f.next("GET", "B");
    rerender(<ModelPicker chatId="A" disabled={false} />); // back to A while A1 is still pending
    fireEvent.click(screen.getByLabelText("m-reason")); // A3

    await f.next("PUT", "A"); // A1 answered: only now may the queue move on (A2 is skipped, A3 supersedes it)
    await f.next("GET", "A"); // the read made on returning to A waited behind the writes; A3 changed things since, so it is dropped
    await f.next("PUT", "A"); // A3 answered
    await new Promise((r) => setTimeout(r, 30));

    // The SERVER's final choice for A is A3, and A2 (superseded while queued) was never sent after it.
    expect(f.server.A).toMatchObject({ provider: "alpha", model: "m-reason" });
    expect(f.arrivals.at(-1)).toEqual({ chat: "A", model: "m-reason" });
    expect(f.arrivals.filter((a) => a.chat === "A" && a.model === "m-plain")).toEqual([]);
    expect(f.server.B).toEqual(mB); // B untouched
    expect(f.arrivals.filter((a) => a.chat === "B")).toEqual([]);
    expect(checkedModel()).toEqual(["m-reason"]);
  });
});
