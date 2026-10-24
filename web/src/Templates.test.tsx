// @vitest-environment jsdom
import { afterEach, describe, expect, it, vi } from "vitest";
import { cleanup, fireEvent, render, screen, waitFor } from "@testing-library/react";
import { useState } from "react";
import { Templates } from "./Templates";
import type { OwnTemplate } from "./draftApi";

afterEach(() => {
  cleanup();
  vi.unstubAllGlobals();
});

function stub(status: number, json: unknown) {
  const f = vi.fn(async () => ({ status, ok: status < 400, json: async () => json }) as Response);
  vi.stubGlobal("fetch", f);
  return f;
}
let latest: OwnTemplate[] = [];
function Host({ initial }: { initial: OwnTemplate[] }) {
  const [own, setOwn] = useState(initial);
  latest = own;
  return <Templates own={own} onChange={setOwn} />;
}
const one: OwnTemplate = { id: "a".repeat(32), name: "deploy", body: "old body" };
const fields = () => ({ name: screen.getByLabelText("Name") as HTMLInputElement, text: screen.getByLabelText("Text") as HTMLTextAreaElement });

describe("editing own templates", () => {
  it("loads a template into the form and saves name and body with PUT", async () => {
    const f = stub(200, { ...one, name: "ship", body: "new body" });
    render(<Host initial={[one]} />);
    fireEvent.click(screen.getByRole("button", { name: "Edit template deploy" }));
    expect(fields().name.value).toBe("deploy");
    expect(fields().text.value).toBe("old body");
    fireEvent.change(fields().name, { target: { value: "ship" } });
    fireEvent.change(fields().text, { target: { value: "new body" } });
    fireEvent.click(screen.getByRole("button", { name: "Save changes" }));
    await waitFor(() => expect(latest[0].name).toBe("ship"));
    const [url, init] = f.mock.calls[0] as unknown as [string, RequestInit];
    expect(url).toBe(`/api/me/templates/${one.id}`);
    expect(init.method).toBe("PUT");
    expect(JSON.parse(String(init.body))).toEqual({ name: "ship", body: "new body" });
    expect(latest).toHaveLength(1);
    expect(fields().name.value).toBe(""); // back to adding
  });

  it("a name already in use is refused in words and the form keeps what was typed", async () => {
    stub(409, { error: { code: "TEMPLATE_NAME_TAKEN" } });
    render(<Host initial={[one]} />);
    fireEvent.click(screen.getByRole("button", { name: "Edit template deploy" }));
    fireEvent.change(fields().name, { target: { value: "taken" } });
    fireEvent.click(screen.getByRole("button", { name: "Save changes" }));
    expect((await screen.findByRole("alert")).textContent).toContain("already have a template with that name");
    expect(fields().name.value).toBe("taken");
    expect(latest[0].name).toBe("deploy");
  });

  it("invalid input and a server failure keep the template unchanged", async () => {
    stub(422, { error: { code: "TEMPLATE_NAME_INVALID", message: "A template name is 1-32 characters: a letter or digit first, then letters, digits, - or _" } });
    render(<Host initial={[one]} />);
    fireEvent.click(screen.getByRole("button", { name: "Edit template deploy" }));
    fireEvent.click(screen.getByRole("button", { name: "Save changes" }));
    expect((await screen.findByRole("alert")).textContent).toContain("a letter or digit first");
    stub(500, null);
    fireEvent.click(screen.getByRole("button", { name: "Save changes" }));
    await waitFor(() => expect(screen.getByRole("alert").textContent).toContain("Could not save"));
    expect(latest).toEqual([one]);
  });

  it("a template deleted on another device is dropped from the list when saving it 404s", async () => {
    stub(404, { error: { code: "NOT_FOUND" } });
    render(<Host initial={[one]} />);
    fireEvent.click(screen.getByRole("button", { name: "Edit template deploy" }));
    fireEvent.click(screen.getByRole("button", { name: "Save changes" }));
    expect((await screen.findByRole("alert")).textContent).toContain("no longer exists");
    expect(latest).toEqual([]);
  });

  it("a text over the byte bound is refused before sending, with the real size", async () => {
    const f = stub(201, one);
    render(<Host initial={[]} />);
    fireEvent.change(fields().name, { target: { value: "cjk" } });
    fireEvent.change(fields().text, { target: { value: "漢".repeat(3000) } }); // 3000 characters, 9000 bytes
    fireEvent.click(screen.getByRole("button", { name: "Add template" }));
    const alert = await screen.findByRole("alert");
    expect(alert.textContent).toContain("9,000 bytes");
    expect(alert.textContent).toContain("8,192 bytes");
    expect(alert.textContent).not.toContain("required");
    expect(f).not.toHaveBeenCalled();
  });

  it("a server 422 shows the server's own message, not a generic one", async () => {
    stub(422, { error: { code: "TEMPLATE_BODY_TOO_LARGE", message: "The template text is 9000 bytes; the limit is 8192 bytes" } });
    render(<Host initial={[]} />);
    fireEvent.change(fields().name, { target: { value: "x" } });
    fireEvent.change(fields().text, { target: { value: "short" } });
    fireEvent.click(screen.getByRole("button", { name: "Add template" }));
    expect((await screen.findByRole("alert")).textContent).toBe("The template text is 9000 bytes; the limit is 8192 bytes");
  });

  it("Cancel returns to adding without saving", () => {
    const f = stub(200, {});
    render(<Host initial={[one]} />);
    fireEvent.click(screen.getByRole("button", { name: "Edit template deploy" }));
    fireEvent.click(screen.getByRole("button", { name: "Cancel" }));
    expect(screen.getByRole("button", { name: "Add template" })).toBeTruthy();
    expect(f).not.toHaveBeenCalled();
  });
});
