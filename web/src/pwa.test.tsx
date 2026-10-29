import { afterEach, expect, it, vi } from "vitest";
import { renderToStaticMarkup } from "react-dom/server";
import { UpdateNotice } from "./PwaNotice";
import { reloadForUpdate, watchUpdates } from "./pwa";
import { loadSession, signOut } from "./api";
import { listChats } from "./chatApi";

afterEach(() => {vi.unstubAllEnvs(); });

it("announces the waiting update and offers an explicit keyboard action", () => {
  const markup = renderToStaticMarkup(<UpdateNotice onReload={() => {}} />);
  expect(markup).toContain('role="status"');
  expect(markup).toContain('<button type="button"');
  expect(markup).toContain("Reload to update");
  expect(markup).toContain("interrupts any reply in progress");
});

it("reloads only after the user's update action and controller change", () => {
  const container = new EventTarget() as ServiceWorkerContainer;
  const worker = { postMessage: vi.fn() } as unknown as ServiceWorker;
  const reload = vi.fn();
  container.dispatchEvent(new Event("controllerchange"));
  expect(reload).not.toHaveBeenCalled();
  reloadForUpdate(worker, container, reload);
  expect(reload).not.toHaveBeenCalled();
  expect(worker.postMessage).toHaveBeenCalledWith({ type: "ACTIVATE_UPDATE" });
  container.dispatchEvent(new Event("controllerchange"));
  container.dispatchEvent(new Event("controllerchange"));
  expect(reload).toHaveBeenCalledOnce();
});

it("finds a worker already waiting and a newly installed update without activating either", async () => {
  vi.stubEnv("PROD", true);
  const waiting = { postMessage: vi.fn() } as unknown as ServiceWorker;
  const installing = new EventTarget();
  const registration = Object.assign(new EventTarget(), { waiting: null as ServiceWorker | null, installing });
  const register = vi.fn(async () => registration);
  vi.stubGlobal("navigator", { serviceWorker: { register, controller: {} } });
  const onWaiting = vi.fn();
  const stop = await watchUpdates(onWaiting);
  expect(register).toHaveBeenCalledWith("/sw.js", { scope: "/", updateViaCache: "none" });
  expect(onWaiting).not.toHaveBeenCalled();
  registration.waiting = waiting;
  installing.dispatchEvent(new Event("statechange"));
  expect(onWaiting).toHaveBeenCalledWith(waiting);
  expect(waiting.postMessage).not.toHaveBeenCalled();
  stop();
  onWaiting.mockClear();
  installing.dispatchEvent(new Event("statechange"));
  expect(onWaiting).not.toHaveBeenCalled();
  const stopAgain = await watchUpdates(onWaiting);
  expect(onWaiting).toHaveBeenCalledWith(waiting);
  stopAgain();
});

it("asks all workers to purge on logout, expired session, and suspension from either API", async () => {
  const postMessage = vi.fn();
  const waitingPost = vi.fn();
  const serviceWorker = { controller: { postMessage }, getRegistration: async () => ({ active: { postMessage }, waiting: { postMessage: waitingPost } }) };
  vi.stubGlobal("navigator", { serviceWorker });
  for (const [response, call] of [
    [new Response(null, { status: 204 }), signOut],
    [new Response(null, { status: 401 }), () => loadSession(false)],
    [Response.json({ state: "SUSPENDED" }), () => loadSession(false)],
    [Response.json({ error: { code: "ACCOUNT_SUSPENDED" } }, { status: 403 }), listChats],
  ] as const) {
    postMessage.mockClear(); waitingPost.mockClear();
    vi.stubGlobal("fetch", vi.fn(async () => response));
    await call();
    expect(postMessage).toHaveBeenCalledWith({ type: "PURGE_USER_STATE" });
    expect(waitingPost).toHaveBeenCalledWith({ type: "PURGE_USER_STATE" });
  }
  postMessage.mockClear();
  vi.stubGlobal("fetch", vi.fn(async () => new Response(null, { status: 500 })));
  await signOut();
  expect(postMessage).not.toHaveBeenCalled();
});
