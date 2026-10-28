import { afterEach, describe, expect, it, vi } from "vitest";
import { MAX_IMAGE_BYTES, assetUrl, imageFilesFrom, parseImageInput, precheckImage, sendMessageWithImages, getImageInput, describeImageReason } from "./mediaApi";
import { mediaKind, mediaUrl, splitMedia } from "./mediaTags";

afterEach(() => vi.unstubAllGlobals());

describe("precheckImage", () => {
  it("accepts png/jpeg/gif within the byte budget only", () => {
    expect(precheckImage({ type: "image/png", size: 1000 })).toBeNull();
    expect(precheckImage({ type: "image/gif", size: MAX_IMAGE_BYTES })).toBeNull();
    expect(precheckImage({ type: "image/svg+xml", size: 10 })).toBe("UNSUPPORTED_TYPE");
    expect(precheckImage({ type: "image/webp", size: 10 })).toBe("UNSUPPORTED_TYPE");
    expect(precheckImage({ type: "text/html", size: 10 })).toBe("UNSUPPORTED_TYPE");
    expect(precheckImage({ type: "image/png", size: MAX_IMAGE_BYTES + 1 })).toBe("TOO_LARGE");
    expect(precheckImage({ type: "image/png", size: 0 })).toBe("TOO_LARGE");
  });
});

describe("imageFilesFrom", () => {
  const img = new File(["x"], "a.png", { type: "image/png" });
  const txt = new File(["x"], "a.txt", { type: "text/plain" });
  it("keeps only image files from a drop", () => {
    expect(imageFilesFrom({ files: [img, txt] as unknown as FileList, items: [] as unknown as DataTransferItemList })).toEqual([img]);
  });
  it("reads a clipboard screenshot from items when files is empty", () => {
    const item = { kind: "file", type: "image/png", getAsFile: () => img };
    const text = { kind: "string", type: "text/plain", getAsFile: () => null };
    expect(imageFilesFrom({ files: [] as unknown as FileList, items: [text, item] as unknown as DataTransferItemList })).toEqual([img]);
  });
  it("is empty for nothing", () => {
    expect(imageFilesFrom(null)).toEqual([]);
  });
});

describe("input capability", () => {
  it("parses the server verdict and refuses malformed bodies", () => {
    expect(parseImageInput({ image: { available: true, reason: null, model: "m" } })).toEqual({ available: true, reason: null, model: "m" });
    expect(parseImageInput({ image: { reason: "MODEL_TEXT_ONLY" } })).toBeNull();
    expect(parseImageInput(null)).toBeNull();
    expect(describeImageReason("MODEL_TEXT_ONLY")).toMatch(/does not accept images/);
  });
  it("treats a failed read as an error, not as available", async () => {
    vi.stubGlobal("fetch", vi.fn().mockResolvedValue(new Response(JSON.stringify({ error: { code: "TENANT_NOT_READY" } }), { status: 409 })));
    const r = await getImageInput("c1");
    expect(r).toEqual({ ok: false, code: "TENANT_NOT_READY", status: 409 });
  });
});

describe("sendMessageWithImages", () => {
  it("sends text and asset ids only, with the idempotency key", async () => {
    const f = vi.fn().mockResolvedValue(new Response(JSON.stringify({ runId: "r1" }), { status: 202 }));
    vi.stubGlobal("fetch", f);
    const r = await sendMessageWithImages("chat1", "hi", "idem-key-0001", ["a".repeat(32)]);
    expect(r).toEqual({ ok: true, value: { runId: "r1" } });
    const [url, init] = f.mock.calls[0] as [string, RequestInit];
    expect(url).toBe("/api/chats/chat1/messages");
    expect(JSON.parse(init.body as string)).toEqual({ input: "hi", imageAssetIds: ["a".repeat(32)] });
    expect((init.headers as Record<string, string>)["Idempotency-Key"]).toBe("idem-key-0001");
  });
  it("surfaces the refusal code and never reports success", async () => {
    vi.stubGlobal("fetch", vi.fn().mockResolvedValue(new Response(JSON.stringify({ error: { code: "IMAGE_INPUT_UNAVAILABLE" } }), { status: 422 })));
    expect(await sendMessageWithImages("c", "hi", "idem-key-0002", ["x"])).toEqual({ ok: false, code: "IMAGE_INPUT_UNAVAILABLE", status: 422 });
  });
});

describe("assetUrl", () => {
  it("builds owner-routed urls from an id only", () => {
    expect(assetUrl("abc")).toBe("/api/assets/abc?inline=1");
    expect(assetUrl("abc", false)).toBe("/api/assets/abc");
    expect(assetUrl("../x")).toBe("/api/assets/..%2Fx?inline=1");
  });
});

describe("splitMedia", () => {
  it("takes the tags and the voice marker out of the text and keeps the paths", () => {
    const r = splitMedia("Here you go\n[[audio_as_voice]]\nMEDIA:/opt/data/profiles/u_x/cache/audio/tts_1.mp3");
    expect(r.paths).toEqual(["/opt/data/profiles/u_x/cache/audio/tts_1.mp3"]);
    expect(r.text).toBe("Here you go");
  });
  it("handles quoted paths, trailing punctuation and repeats, and leaves ordinary text alone", () => {
    expect(splitMedia('MEDIA:"/a b/c.png" done')).toEqual({ text: "done", paths: ["/a b/c.png"] });
    expect(splitMedia("See MEDIA:/x/y.png. And MEDIA:/x/y.png").paths).toEqual(["/x/y.png"]);
    expect(splitMedia("no media here")).toEqual({ text: "no media here", paths: [] });
  });
  it("knows the kind from the extension and builds the same-origin URL", () => {
    expect([mediaKind("/a.PNG"), mediaKind("/a.mp3"), mediaKind("/a.webm"), mediaKind("/a.txt")]).toEqual(["image", "audio", "video", null]);
    expect(mediaUrl("/a b.png")).toBe("/api/media?path=%2Fa%20b.png");
  });
});
