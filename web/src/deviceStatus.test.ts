// @vitest-environment jsdom
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { deviceStatus, sha256Hex, type Notifications } from "./push";

const list = (devices: Notifications["devices"]): Notifications => ({ configured: true, mode: "all", devices });

describe("deviceStatus()", () => {
  let sub: { endpoint: string } | null;
  let permission: string;

  beforeEach(() => {
    sub = null;
    permission = "default";
    const reg = { pushManager: { getSubscription: async () => sub } };
    vi.stubGlobal("PushManager", class {});
    vi.stubGlobal("Notification", class {});
    Object.defineProperty(Notification, "permission", { get: () => permission, configurable: true });
    Object.defineProperty(window, "isSecureContext", { value: true, configurable: true });
    Object.defineProperty(navigator, "serviceWorker", { value: { getRegistration: async () => reg }, configurable: true });
  });
  afterEach(() => vi.unstubAllGlobals());

  it("reports no subscription", async () => {
    expect(await deviceStatus(list([]))).toEqual({ device: null, missing: "none" });
  });

  it("reports blocked permission when there is no subscription", async () => {
    permission = "denied";
    expect(await deviceStatus(list([]))).toEqual({ device: null, missing: "denied" });
  });

  it("reports a subscription whose endpoint the device list lacks", async () => {
    sub = { endpoint: "https://push.example.test/a" };
    expect(await deviceStatus(list([{ id: "d1", endpointHash: "other" }]))).toEqual({ device: null, missing: "unlisted" });
  });

  it("returns the device when the endpoint is listed", async () => {
    sub = { endpoint: "https://push.example.test/a" };
    const d = { id: "d1", endpointHash: await sha256Hex(sub.endpoint) };
    expect(await deviceStatus(list([d]))).toEqual({ device: d, missing: null });
  });
});
