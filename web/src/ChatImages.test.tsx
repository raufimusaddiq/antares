// @vitest-environment jsdom
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { act, cleanup, fireEvent, render, screen, waitFor, within } from "@testing-library/react";
import type { ChatSummary } from "./chatApi";

const api = vi.hoisted(() => ({ listChats: vi.fn(), createChat: vi.fn(), getMessages: vi.fn(), sendMessage: vi.fn(), getRun: vi.fn(), cancelRun: vi.fn() }));
const media = vi.hoisted(() => ({ getImageInput: vi.fn(), uploadImage: vi.fn(), sendMessageWithImages: vi.fn(), deleteAsset: vi.fn() }));

vi.mock("./chatApi", async (orig) => ({ ...(await orig<typeof import("./chatApi")>()), ...api }));
vi.mock("./mediaApi", async (orig) => ({ ...(await orig<typeof import("./mediaApi")>()), ...media }));

import { Chat, Transcript } from "./Chat";
import { parseHistory, stripImageMarkers } from "./chatApi";
import { planRetry } from "./retry";

const ID = "ab".repeat(16);
const chat = (id: string): ChatSummary => ({ id, title: "", createdAt: "", updatedAt: "", activeRunId: "", pinned: false, archived: false, deleteFailed: false, visualize: false });
const ok = <T,>(value: T) => ({ ok: true as const, value });
const verdict = (available: boolean, reason: string | null = null) => ok({ available, reason, model: "deepseek-v4-1-flash" });
const png = () => new File([new Uint8Array(64)], "shot.png", { type: "image/png" });
const box = () => screen.getByRole("textbox", { name: "Message" }) as HTMLTextAreaElement;

class FakeSource {
  static all: FakeSource[] = [];
  static CLOSED = 2;
  readyState = 1;
  onopen = null;
  onerror: (() => void) | null = null;
  constructor(public url: string) {
    FakeSource.all.push(this);
  }
  addEventListener() {}
  close() {
    this.readyState = 2;
  }
}

async function mount() {
  api.listChats.mockResolvedValue(ok({ chats: [chat("c1")], nextCursor: "" }));
  api.getMessages.mockResolvedValue(ok({ turns: [], nextCursor: "", total: 0 }));
  api.getRun.mockResolvedValue({ ok: false, code: "NOT_FOUND" });
  render(<Chat email="a@example.test" onSignOut={() => {}} />);
  await waitFor(() => expect(screen.queryByText("Loading…")).toBeNull());
}

const paste = (files: File[], text = "") =>
  fireEvent.paste(box(), { clipboardData: { files, items: files.map((f) => ({ kind: "file", type: f.type, getAsFile: () => f })), getData: () => text } });

beforeEach(() => {
  localStorage.clear();
  vi.stubGlobal("EventSource", FakeSource);
  vi.stubGlobal("fetch", vi.fn(async () => ({ status: 404, ok: false, json: async () => null }) as Response));
  URL.createObjectURL = vi.fn(() => "blob:thumb");
  URL.revokeObjectURL = vi.fn();
  media.deleteAsset.mockResolvedValue(true);
  api.sendMessage.mockResolvedValue(ok({ runId: "r1", status: "PENDING" }));
});
afterEach(() => {
  cleanup();
  vi.unstubAllGlobals();
  FakeSource.all = [];
  [...Object.values(api), ...Object.values(media)].forEach((m) => m.mockReset());
});

describe("image paste, chip and send (#108 image acceptance, MEDIA-EXT-001/002)", () => {
  it("a pasted image becomes an owned chip, and the send carries only its asset id", async () => {
    media.getImageInput.mockResolvedValue(verdict(true));
    media.uploadImage.mockResolvedValue(ok({ id: ID }));
    media.sendMessageWithImages.mockResolvedValue(ok({ runId: "r1" }));
    await mount();
    await waitFor(() => expect(screen.getByText("Attach image")).toBeTruthy());

    expect(paste([png()])).toBe(false); // the image is taken over, nothing is inserted as HTML or text
    expect(box().value).toBe("");
    await waitFor(() => expect(screen.getByRole("list", { name: "Attached images" })).toBeTruthy());
    expect(screen.getByText("shot.png")).toBeTruthy();
    expect(media.uploadImage).toHaveBeenCalledTimes(1);

    fireEvent.change(box(), { target: { value: "what is this?" } });
    await waitFor(() => expect(screen.getByRole("button", { name: /Send/ }).hasAttribute("disabled")).toBe(false));
    fireEvent.keyDown(box(), { key: "Enter" });
    await waitFor(() => expect(media.sendMessageWithImages).toHaveBeenCalledTimes(1));
    const [chatId, input, key, ids] = media.sendMessageWithImages.mock.calls[0];
    expect([chatId, input, ids]).toEqual(["c1", "what is this?", [ID]]);
    expect(key).toMatch(/^[A-Za-z0-9_-]{8,128}$/);
    expect(api.sendMessage).not.toHaveBeenCalled();
    // The strip is done; the sent turn shows the owner's thumbnail from the asset route.
    await waitFor(() => expect(screen.queryByRole("list", { name: "Attached images" })).toBeNull());
    expect(screen.getByAltText("Image 1 you sent").getAttribute("src")).toBe(`/api/assets/${ID}?inline=1`);
    expect(media.deleteAsset).not.toHaveBeenCalled(); // a sent image stays for the history
  });

  it("pasted text is left alone", async () => {
    media.getImageInput.mockResolvedValue(verdict(true));
    await mount();
    expect(paste([], "plain text")).toBe(true);
    expect(media.uploadImage).not.toHaveBeenCalled();
  });

  it("a model without vision refuses the image out loud: no upload, no hidden picker pretending", async () => {
    media.getImageInput.mockResolvedValue(verdict(false, "MODEL_TEXT_ONLY"));
    await mount();
    await waitFor(() => expect(media.getImageInput).toHaveBeenCalled());
    expect(screen.queryByText("Attach image")).toBeNull();
    paste([png()]);
    expect((await screen.findByRole("alert")).textContent).toMatch(/does not accept images/);
    expect(media.uploadImage).not.toHaveBeenCalled();
    // Text still sends, through the unchanged path.
    fireEvent.change(box(), { target: { value: "hello" } });
    fireEvent.keyDown(box(), { key: "Enter" });
    await waitFor(() => expect(api.sendMessage).toHaveBeenCalledTimes(1));
    expect(media.sendMessageWithImages).not.toHaveBeenCalled();
  });

  it("an unknown verdict (read failed) is off, not on", async () => {
    media.getImageInput.mockResolvedValue({ ok: false, code: "NETWORK", status: 0 });
    await mount();
    await waitFor(() => expect(media.getImageInput).toHaveBeenCalled());
    expect(screen.queryByText("Attach image")).toBeNull();
    paste([png()]);
    expect((await screen.findByRole("alert")).textContent).toMatch(/cannot be confirmed/);
  });

  it("a dropped image works, a dropped non-image is ignored", async () => {
    media.getImageInput.mockResolvedValue(verdict(true));
    media.uploadImage.mockResolvedValue(ok({ id: ID }));
    await mount();
    await waitFor(() => expect(screen.getByText("Attach image")).toBeTruthy());
    const form = box().closest("form") as HTMLFormElement;
    const text = new File(["x"], "a.txt", { type: "text/plain" });
    fireEvent.drop(form, { dataTransfer: { files: [text], items: [], types: ["Files"] } });
    expect(media.uploadImage).not.toHaveBeenCalled();
    fireEvent.drop(form, { dataTransfer: { files: [png()], items: [], types: ["Files"] } });
    await waitFor(() => expect(media.uploadImage).toHaveBeenCalledTimes(1));
  });

  it("a failed upload blocks sending and offers retry; nothing is dropped", async () => {
    media.getImageInput.mockResolvedValue(verdict(true));
    media.uploadImage.mockResolvedValueOnce({ ok: false, code: "NETWORK", status: 0 }).mockResolvedValueOnce(ok({ id: ID }));
    media.sendMessageWithImages.mockResolvedValue(ok({ runId: "r1" }));
    await mount();
    await waitFor(() => expect(screen.getByText("Attach image")).toBeTruthy());
    paste([png()]);
    fireEvent.change(box(), { target: { value: "look" } });
    const strip = await screen.findByRole("list", { name: "Attached images" });
    await within(strip).findByText("Retry");
    expect(screen.getByRole("button", { name: /Send/ }).hasAttribute("disabled")).toBe(true);
    fireEvent.keyDown(box(), { key: "Enter" });
    expect(media.sendMessageWithImages).not.toHaveBeenCalled();
    expect(api.sendMessage).not.toHaveBeenCalled();
    fireEvent.click(within(strip).getByText("Retry"));
    await waitFor(() => expect(screen.getByRole("button", { name: /Send/ }).hasAttribute("disabled")).toBe(false));
    fireEvent.keyDown(box(), { key: "Enter" });
    await waitFor(() => expect(media.sendMessageWithImages).toHaveBeenCalledTimes(1));
  });

  it("a refused image send says so, keeps the images, and 'Send again' repeats the same ids", async () => {
    media.getImageInput.mockResolvedValue(verdict(true));
    media.uploadImage.mockResolvedValue(ok({ id: ID }));
    media.sendMessageWithImages.mockResolvedValueOnce({ ok: false, code: "IMAGE_INPUT_UNAVAILABLE", status: 422 }).mockResolvedValueOnce(ok({ runId: "r2" }));
    await mount();
    await waitFor(() => expect(screen.getByText("Attach image")).toBeTruthy());
    paste([png()]);
    await waitFor(() => expect(screen.getByRole("button", { name: "Remove shot.png" })).toBeTruthy());
    await waitFor(() => expect(media.uploadImage).toHaveBeenCalled());
    fireEvent.change(box(), { target: { value: "look" } });
    await waitFor(() => expect(screen.getByRole("button", { name: /Send/ }).hasAttribute("disabled")).toBe(false));
    fireEvent.keyDown(box(), { key: "Enter" });
    expect((await screen.findByText(/cannot take images/)).textContent).toMatch(/Your message was not sent|Not sent/);
    fireEvent.click(screen.getByText("Send again"));
    await waitFor(() => expect(media.sendMessageWithImages).toHaveBeenCalledTimes(2));
    expect(media.sendMessageWithImages.mock.calls[1][3]).toEqual([ID]);
    expect(media.sendMessageWithImages.mock.calls[1][2]).toBe(media.sendMessageWithImages.mock.calls[0][2]); // same idempotency key
  });

  it("removing a staged chip deletes the owner's asset", async () => {
    media.getImageInput.mockResolvedValue(verdict(true));
    media.uploadImage.mockResolvedValue(ok({ id: ID }));
    await mount();
    await waitFor(() => expect(screen.getByText("Attach image")).toBeTruthy());
    paste([png()]);
    await waitFor(() => expect(screen.getByRole("button", { name: "Remove shot.png" })).toBeTruthy());
    await waitFor(() => expect(media.uploadImage).toHaveBeenCalled());
    await new Promise((r) => setTimeout(r, 0));
    fireEvent.click(screen.getByRole("button", { name: "Remove shot.png" }));
    expect(media.deleteAsset).toHaveBeenCalledWith(ID);
    expect(screen.queryByRole("list", { name: "Attached images" })).toBeNull();
  });
});

describe("history", () => {
  it("parses the server's attachments and drops the [image] markers they replace", () => {
    const p = parseHistory({
      messages: [
        { seq: 0, role: "user", text: "what is this?\n[image]", attachments: [{ assetId: ID, mediaType: "image/png", available: true }] },
        { seq: 1, role: "user", text: "typed [image] by hand" },
        { seq: 2, role: "user", text: "x", attachments: [{ assetId: "../../etc/passwd", mediaType: "image/png", available: true }, { assetId: ID, mediaType: "text/html", available: true }] },
      ],
    });
    expect(p?.turns[0]).toMatchObject({ text: "what is this?", attachments: [{ assetId: ID, available: true }] });
    expect(p?.turns[1].text).toBe("typed [image] by hand");
    expect(p?.turns[2].attachments).toBeUndefined();
  });
});

describe("generated media paths (MEDIA-EXT-006)", () => {
  it("never shows a host file path from an assistant turn or a streaming reply", () => {
    const text = "Here it is\nMEDIA:/opt/data/profiles/u_x/cache/images/gen_1.png";
    const noop = () => {};
    const { container } = render(
      <Transcript
        turns={[{ seq: 0, role: "assistant", text }]}
        pending={null}
        run={{ runId: "r", phase: "streaming", text, tools: [], folded: { count: 0, failed: 0, untracked: 0 }, thinking: false, seenEvent: true } as never}
        onCancel={noop}
        onRetry={noop}
        onReconnect={noop}
        onResend={noop}
      />,
    );
    expect(container.textContent).toContain("Here it is");
    expect(container.textContent).not.toMatch(/opt\/data|gen_1|profiles/);
    // The finished turn shows the image itself, loaded from the user's own profile through /api/media.
    const img = container.querySelector<HTMLImageElement>('[data-media="generated"] img')!;
    expect(img.getAttribute("src")).toBe("/api/media?path=" + encodeURIComponent("/opt/data/profiles/u_x/cache/images/gen_1.png"));
    expect(img.alt).toBe("Generated image 1");
  });
});

const turn = (seq: number, text: string, attachments?: { assetId: string; mediaType: string; available: boolean }[]) => ({ seq, role: "user" as const, text, ...(attachments ? { attachments } : {}) });
const att = (available = true) => [{ assetId: ID, mediaType: "image/png", available }];
const fail = () => {
  const src = FakeSource.all[FakeSource.all.length - 1];
  src.readyState = FakeSource.CLOSED;
  act(() => src.onerror?.());
};

// Review round 1, IMPORTANT: Try again must resolve text AND images from the same turn of the chat on
// screen, not from state left by an earlier send.
describe("retry after reload or navigation", () => {
  async function mountWith(chats: ChatSummary[], history: Record<string, ReturnType<typeof turn>[]>, runs: Record<string, string>) {
    api.listChats.mockResolvedValue(ok({ chats, nextCursor: "" }));
    api.getMessages.mockImplementation(async (id: string) => ok({ turns: history[id] ?? [], nextCursor: "", total: (history[id] ?? []).length }));
    api.getRun.mockImplementation(async (id: string) => ok({ runId: id, status: runs[id] ?? "FAILED" }));
    media.getImageInput.mockResolvedValue(verdict(true));
    render(<Chat email="a@example.test" onSignOut={() => {}} />);
    await waitFor(() => expect(screen.queryByText("Loading…")).toBeNull());
  }

  it("after a reload the failed image turn is re-sent with its images", async () => {
    media.sendMessageWithImages.mockResolvedValue(ok({ runId: "r2" }));
    await mountWith([{ ...chat("c1"), activeRunId: "r1" }], { c1: [turn(0, "look at this", att())] }, {});
    await waitFor(() => expect(FakeSource.all).toHaveLength(1));
    fail();
    fireEvent.click(await screen.findByRole("button", { name: "Try again" }));
    await waitFor(() => expect(media.sendMessageWithImages).toHaveBeenCalledTimes(1));
    expect(media.sendMessageWithImages.mock.calls[0]).toEqual(["c1", "look at this", expect.any(String), [ID]]);
    expect(api.sendMessage).not.toHaveBeenCalled();
  });

  it("another chat's failed run is retried with that chat's own text and no images from the previous send", async () => {
    media.uploadImage.mockResolvedValue(ok({ id: ID }));
    media.sendMessageWithImages.mockResolvedValue(ok({ runId: "r5" }));
    await mountWith([chat("c1"), { ...chat("c2"), title: "Second", activeRunId: "r2" }], { c1: [], c2: [turn(0, "beta")] }, { r5: "RUNNING" });
    await waitFor(() => expect(screen.getByText("Attach image")).toBeTruthy());
    paste([png()]);
    await waitFor(() => expect(media.uploadImage).toHaveBeenCalled());
    await new Promise((r) => setTimeout(r, 0));
    fireEvent.change(box(), { target: { value: "alpha" } });
    await waitFor(() => expect(screen.getByRole("button", { name: /Send/ }).hasAttribute("disabled")).toBe(false));
    fireEvent.keyDown(box(), { key: "Enter" });
    await waitFor(() => expect(media.sendMessageWithImages).toHaveBeenCalledTimes(1));

    fireEvent.click(screen.getByText("Second"));
    await waitFor(() => expect(FakeSource.all.some((s) => s.url.includes("r2"))).toBe(true));
    const src = FakeSource.all.find((s) => s.url.includes("r2"))!;
    src.readyState = FakeSource.CLOSED;
    act(() => src.onerror?.());
    fireEvent.click(await screen.findByRole("button", { name: "Try again" }));
    await waitFor(() => expect(api.sendMessage).toHaveBeenCalledTimes(1));
    expect(api.sendMessage.mock.calls[0].slice(0, 2)).toEqual(["c2", "beta"]);
    expect(media.sendMessageWithImages).toHaveBeenCalledTimes(1); // only the original alpha send
  });

  it("an image turn whose images cannot be proven is not re-sent without them", async () => {
    await mountWith([{ ...chat("c1"), activeRunId: "r1" }], { c1: [turn(0, "what is this?\n[image]")] }, {});
    await waitFor(() => expect(FakeSource.all).toHaveLength(1));
    fail();
    fireEvent.click(await screen.findByRole("button", { name: "Try again" }));
    expect((await screen.findAllByText(/could not be matched/))[0]).toBeTruthy();
    expect(box().value).toContain("what is this?");
    expect(api.sendMessage).not.toHaveBeenCalled();
    expect(media.sendMessageWithImages).not.toHaveBeenCalled();
  });

  it("a deleted image sends nothing and puts the text back", async () => {
    await mountWith([{ ...chat("c1"), activeRunId: "r1" }], { c1: [turn(0, "look", att(false))] }, {});
    await waitFor(() => expect(FakeSource.all).toHaveLength(1));
    fail();
    fireEvent.click(await screen.findByRole("button", { name: "Try again" }));
    expect((await screen.findAllByText(/no longer available/))[0]).toBeTruthy();
    expect(box().value).toBe("look");
    expect(api.sendMessage).not.toHaveBeenCalled();
    expect(media.sendMessageWithImages).not.toHaveBeenCalled();
  });
});

// Review round 2, IMPORTANT: only the transport placeholders are removed; a trailing "[image]" the user typed is data.
describe("literal [image] text survives the history parse", () => {
  const one = [{ assetId: ID, mediaType: "image/png", available: true }];
  const two = [...one, { assetId: "cd".repeat(16), mediaType: "image/png", available: true }];
  const parse = (text: string, attachments: typeof one) => parseHistory({ messages: [{ seq: 0, role: "user", text, attachments }] })!.turns[0].text;

  it("strips exactly as many placeholders as attachments", () => {
    expect(parse("explain [image]\n[image]", one)).toBe("explain [image]");
    expect(parse("explain [image]\n[image]\n[image]", two)).toBe("explain [image]");
    expect(parse("a [image] b [image]\n[image]\n[image]", two)).toBe("a [image] b [image]");
    expect(parse("explain\n[image]\n[image]", two)).toBe("explain");
    expect(parse("[image]\n[image]", one)).toBe("[image]");
  });
  it("keeps text it cannot account for", () => {
    expect(stripImageMarkers("explain", 1)).toBe("explain");
    expect(stripImageMarkers("explain [image]", 2)).toBe("explain [image]");
  });
  it("reload retry re-sends the original prompt, literal token included, with its attachment", async () => {
    media.sendMessageWithImages.mockResolvedValue(ok({ runId: "r2" }));
    api.listChats.mockResolvedValue(ok({ chats: [{ ...chat("c1"), activeRunId: "r1" }], nextCursor: "" }));
    api.getMessages.mockResolvedValue(parseHistoryResult("explain [image]\n[image]", one));
    api.getRun.mockResolvedValue(ok({ runId: "r1", status: "FAILED" }));
    media.getImageInput.mockResolvedValue(verdict(true));
    render(<Chat email="a@example.test" onSignOut={() => {}} />);
    await waitFor(() => expect(FakeSource.all).toHaveLength(1));
    fail();
    fireEvent.click(await screen.findByRole("button", { name: "Try again" }));
    await waitFor(() => expect(media.sendMessageWithImages).toHaveBeenCalledTimes(1));
    expect(media.sendMessageWithImages.mock.calls[0]).toEqual(["c1", "explain [image]", expect.any(String), [ID]]);
  });
});

function parseHistoryResult(text: string, attachments: { assetId: string; mediaType: string; available: boolean }[]) {
  const page = parseHistory({ messages: [{ seq: 0, role: "user", text, attachments }], nextCursor: "", total: 1 })!;
  return ok(page);
}

describe("planRetry", () => {
  const t = (text: string, attachments?: ReturnType<typeof att>) => ({ seq: 0, role: "user" as const, text, ...(attachments ? { attachments } : {}) });
  it("plans from the last user turn only", () => {
    expect(planRetry([t("a", att()), { seq: 1, role: "assistant", text: "x" }, t("b")])).toEqual({ kind: "send", text: "b", images: [] });
    expect(planRetry([t("a", att())])).toEqual({ kind: "send", text: "a", images: att() });
    expect(planRetry([])).toEqual({ kind: "none" });
    expect(planRetry([{ seq: 0, role: "assistant", text: "x" }])).toEqual({ kind: "none" });
  });
});
