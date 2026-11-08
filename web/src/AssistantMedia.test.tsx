// @vitest-environment jsdom
import { afterEach, describe, expect, it, vi } from "vitest";
import { cleanup, fireEvent, render, screen, waitFor } from "@testing-library/react";
import { AssistantMedia } from "./Admin";
import { parseMediaModels, type MediaModels } from "./adminApi";

afterEach(cleanup);

const models = (image = "", video = "", extra: Partial<MediaModels> = {}): MediaModels => ({
  image: { current: image, default: "agnes-image-2-5-flash:free", options: [{ id: "agnes-image-2-5-flash:free", name: "Agnes Image", free: true }, { id: "gpt-image-2", name: "GPT Image 2", free: false }] },
  video: { current: video, default: "veo-3.1-lite", options: [{ id: "veo-3.1-lite", name: "Veo 3.1 Lite", free: false }] },
  catalogue: "ok",
  reverifying: null,
  ...extra,
});

describe("Admin → Assistant media", () => {
  it("offers Kenari's models and the default, and saves both choices", async () => {
    const onSave = vi.fn().mockResolvedValue({ ok: true, value: models("gpt-image-2", "veo-3.1-lite", { reverifying: 2 }) });
    render(<AssistantMedia models={models()} onSave={onSave} />);
    expect((screen.getByLabelText("Image model") as HTMLSelectElement).value).toBe("");
    expect(screen.getByRole("option", { name: "Default (agnes-image-2-5-flash:free)" })).toBeTruthy();
    expect(screen.getByRole("option", { name: "Agnes Image (free)" })).toBeTruthy();
    expect((screen.getByRole("button", { name: "Save media models" }) as HTMLButtonElement).disabled).toBe(true);
    fireEvent.change(screen.getByLabelText("Image model"), { target: { value: "gpt-image-2" } });
    fireEvent.change(screen.getByLabelText("Video model"), { target: { value: "veo-3.1-lite" } });
    expect(screen.getByRole("status").textContent).toBe("Not saved yet.");
    fireEvent.click(screen.getByRole("button", { name: "Save media models" }));
    await waitFor(() => expect(screen.getByRole("status").textContent).toContain("2 accounts are being re-checked"));
    expect(onSave).toHaveBeenCalledWith("gpt-image-2", "veo-3.1-lite");
  });

  it("keeps showing a saved model Kenari no longer lists, and says Kenari's list could not be read", () => {
    render(<AssistantMedia models={models("old-model", "", { catalogue: "unavailable" })} onSave={vi.fn()} />);
    expect((screen.getByLabelText("Image model") as HTMLSelectElement).value).toBe("old-model");
    expect(screen.getByRole("option", { name: "old-model (not in Kenari's list now)" })).toBeTruthy();
    expect(document.body.textContent).toContain("could not be read just now");
  });

  it("shows a refused save as an error", async () => {
    render(<AssistantMedia models={models()} onSave={vi.fn().mockResolvedValue({ ok: false, code: "INVALID_REQUEST", status: 400 })} />);
    fireEvent.change(screen.getByLabelText("Video model"), { target: { value: "veo-3.1-lite" } });
    fireEvent.click(screen.getByRole("button", { name: "Save media models" }));
    await waitFor(() => expect(screen.getByRole("alert")).toBeTruthy());
  });

  it("parses only well-formed answers", () => {
    const ok = { image: { current: "", default: "a", options: [{ id: "a", name: "A", free: true }] }, video: { default: "v", options: [] }, catalogue: "ok" };
    expect(parseMediaModels(ok)?.image.options).toEqual([{ id: "a", name: "A", free: true }]);
    expect(parseMediaModels({ ...ok, catalogue: "weird" })?.catalogue).toBe("unavailable");
    for (const bad of [null, {}, { ...ok, image: { default: "a", options: [{ name: "no id" }] } }, { ...ok, video: { options: [] } }]) expect(parseMediaModels(bad), JSON.stringify(bad)).toBeNull();
  });
});
