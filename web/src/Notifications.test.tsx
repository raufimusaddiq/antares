// @vitest-environment jsdom
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { cleanup, fireEvent, render, screen, waitFor } from "@testing-library/react";
import type { Notifications as State } from "./push";

const push = vi.hoisted(() => ({
  unsupportedReason: vi.fn(),
  loadNotifications: vi.fn(),
  deviceStatus: vi.fn(),
  enable: vi.fn(),
  disable: vi.fn(),
  setMode: vi.fn(),
}));
vi.mock("./push", async (orig) => ({ ...(await orig<typeof import("./push")>()), ...push }));

import { Notifications } from "./Notifications";

const DEVICE = { id: "d1", endpointHash: "h" };
const HAS = { device: DEVICE, missing: null };
const NONE = { device: null, missing: "none" };
const state = (over: Partial<State> = {}): State => ({ configured: true, mode: "all", devices: [], ...over });
const ok = <T,>(value: T) => ({ ok: true as const, value });

const openPanel = async () => {
  render(<Notifications />);
  fireEvent.click(screen.getByRole("button", { name: "Notifications" }));
};

beforeEach(() => {
  push.unsupportedReason.mockReturnValue(null);
  push.loadNotifications.mockResolvedValue(ok(state()));
  push.deviceStatus.mockResolvedValue(NONE);
  push.enable.mockResolvedValue(ok(true));
  push.disable.mockResolvedValue(ok(true));
  push.setMode.mockResolvedValue(ok(state()));
});
afterEach(() => {
  cleanup();
  vi.clearAllMocks();
});

describe("the notification opt-in", () => {
  it("asks for nothing until the panel is opened, and never subscribes by itself", async () => {
    render(<Notifications />);
    expect(push.loadNotifications).not.toHaveBeenCalled();
    expect(push.enable).not.toHaveBeenCalled();
    expect(screen.queryByLabelText("Notifications")).toBeNull();
  });

  it("says why when the browser cannot take notifications, and offers no control", async () => {
    push.unsupportedReason.mockReturnValue("This browser does not support push notifications.");
    await openPanel();
    await screen.findByText(/does not support push/);
    expect(screen.queryByRole("button", { name: /Turn on/ })).toBeNull();
  });

  it("says so when the server has push switched off, and offers no control", async () => {
    push.loadNotifications.mockResolvedValue(ok(state({ configured: false })));
    await openPanel();
    await screen.findByText(/not set up on this server/);
    expect(screen.queryByRole("button", { name: /Turn on/ })).toBeNull();
  });

  it("says so when the settings cannot be loaded", async () => {
    push.loadNotifications.mockResolvedValue({ ok: false, code: "NETWORK" });
    await openPanel();
    await screen.findByText(/Could not load notification settings/);
  });

  it("turns on for this device only on the button press, then shows the device controls", async () => {
    push.deviceStatus.mockResolvedValueOnce(NONE).mockResolvedValue(HAS);
    push.loadNotifications.mockResolvedValueOnce(ok(state())).mockResolvedValue(ok(state({ devices: [DEVICE] })));
    await openPanel();
    const on = await screen.findByRole("button", { name: "Turn on for this device" });
    expect(push.enable).not.toHaveBeenCalled();
    fireEvent.click(on);
    await screen.findByRole("button", { name: "Turn off on this device" });
    expect(push.enable).toHaveBeenCalledTimes(1);
  });

  it("explains a refusal in words instead of failing silently", async () => {
    push.enable.mockResolvedValue({ ok: false, code: "DENIED" });
    await openPanel();
    fireEvent.click(await screen.findByRole("button", { name: "Turn on for this device" }));
    await screen.findByText(/blocked for this site/);
    expect(screen.getByRole("button", { name: "Turn on for this device" })).toBeTruthy();
  });

  it("uses a generic message for an unknown failure code", async () => {
    push.enable.mockResolvedValue({ ok: false, code: "SOMETHING_ELSE" });
    await openPanel();
    fireEvent.click(await screen.findByRole("button", { name: "Turn on for this device" }));
    await screen.findByText("That did not work. Try again.");
  });

  it("changes the mode and revokes the device", async () => {
    push.deviceStatus.mockResolvedValue(HAS);
    push.loadNotifications.mockResolvedValue(ok(state({ devices: [DEVICE] })));
    await openPanel();
    fireEvent.change(await screen.findByRole("combobox"), { target: { value: "failures" } });
    await waitFor(() => expect(push.setMode).toHaveBeenCalledWith("failures"));
    fireEvent.click(screen.getByRole("button", { name: "Turn off on this device" }));
    await waitFor(() => expect(push.disable).toHaveBeenCalledWith(DEVICE));
  });

  it("tells a muted reader that a device being on does not resume alerts", async () => {
    push.deviceStatus.mockResolvedValue(HAS);
    push.loadNotifications.mockResolvedValue(ok(state({ mode: "off", devices: [DEVICE] })));
    await openPanel();
    await screen.findByText(/muted for your account, on every device/);
    expect((screen.getByRole("combobox") as HTMLSelectElement).value).toBe("off");
  });

  it("says that turning off removes one device while the choice covers all of them", async () => {
    push.deviceStatus.mockResolvedValue(HAS);
    push.loadNotifications.mockResolvedValue(ok(state({ devices: [DEVICE] })));
    await openPanel();
    await screen.findByText(/removes only this device/);
    expect(screen.getByText(/applies to all your devices/)).toBeTruthy();
  });

  it("does not show the muted line when alerts are on", async () => {
    push.deviceStatus.mockResolvedValue(HAS);
    push.loadNotifications.mockResolvedValue(ok(state({ devices: [DEVICE] })));
    await openPanel();
    await screen.findByRole("combobox");
    expect(screen.queryByText(/muted for your account/)).toBeNull();
  });

  it("says this browser has no device when there is no subscription, and offers to turn on", async () => {
    await openPanel();
    await screen.findByText(/no notification device registered/);
    expect(screen.getByRole("button", { name: "Turn on for this device" })).toBeTruthy();
  });

  it("says so when a subscription exists but the device list does not include it", async () => {
    push.deviceStatus.mockResolvedValue({ device: null, missing: "unlisted" });
    await openPanel();
    await screen.findByText(/does not have on your account/);
    expect(screen.getByRole("button", { name: "Turn on for this device" })).toBeTruthy();
  });

  it("says so when permission is blocked", async () => {
    push.deviceStatus.mockResolvedValue({ device: null, missing: "denied" });
    await openPanel();
    await screen.findByText(/blocked notifications for this site/);
  });

  it("shows no missing-device warning when this browser is registered", async () => {
    push.deviceStatus.mockResolvedValue(HAS);
    push.loadNotifications.mockResolvedValue(ok(state({ devices: [DEVICE] })));
    await openPanel();
    await screen.findByRole("combobox");
    expect(screen.queryByText(/not reaching it/)).toBeNull();
  });
});
