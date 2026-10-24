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
    stub(422, { error: { code: "INVALID_REQUEST" } });
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

  it("Cancel returns to adding without saving", () => {
    const f = stub(200, {});
    render(<Host initial={[one]} />);
    fireEvent.click(screen.getByRole("button", { name: "Edit template deploy" }));
    fireEvent.click(screen.getByRole("button", { name: "Cancel" }));
    expect(screen.getByRole("button", { name: "Add template" })).toBeTruthy();
    expect(f).not.toHaveBeenCalled();
  });
});
