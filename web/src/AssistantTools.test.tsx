// @vitest-environment jsdom
import { afterEach, describe, expect, it, vi } from "vitest";
import { cleanup, fireEvent, render, screen, waitFor } from "@testing-library/react";
import { AssistantTools } from "./Admin";
import { parseToolPolicy, type ToolPolicy } from "./adminApi";

afterEach(cleanup);

const policy = (on: string[], reverifying: number | null = null): ToolPolicy => ({
  custom: reverifying !== null,
  reverifying,
  tools: [
    ...["web", "memory", "tts"].map((name) => ({ name, label: name.toUpperCase(), description: `about ${name}`, state: on.includes(name) ? ("on" as const) : ("off" as const), reason: "" })),
    { name: "terminal", label: "terminal", description: "", state: "locked" as const, reason: "Could read other people's data on the shared server." },
  ],
});

describe("Admin → Assistant tools", () => {
  it("offers a checkbox per offered tool, lists locked ones with the reason and no control", () => {
    render(<AssistantTools policy={policy(["web", "memory"])} onSave={vi.fn()} />);
    expect((screen.getByLabelText(/WEB/) as HTMLInputElement).checked).toBe(true);
    expect((screen.getByLabelText(/TTS/) as HTMLInputElement).checked).toBe(false);
    expect(screen.queryByLabelText(/terminal/)).toBeNull();
    expect(document.body.textContent).toContain("Could read other people's data on the shared server.");
    expect(document.body.textContent).toContain("server's default");
    expect((screen.getByRole("button", { name: "Save tools" }) as HTMLButtonElement).disabled).toBe(true);
  });

  it("saves only the offered tools chosen and says how many accounts are re-checked", async () => {
    const onSave = vi.fn().mockResolvedValue({ ok: true, value: policy(["web", "memory", "tts"], 3) });
    render(<AssistantTools policy={policy(["web", "memory"])} onSave={onSave} />);
    fireEvent.click(screen.getByLabelText(/TTS/));
    expect(screen.getByRole("status").textContent).toBe("Not saved yet.");
    fireEvent.click(screen.getByRole("button", { name: "Save tools" }));
    await waitFor(() => expect(screen.getByRole("status").textContent).toContain("3 accounts are being re-checked"));
    expect(onSave).toHaveBeenCalledWith(["web", "memory", "tts"]);
    expect((screen.getByRole("button", { name: "Save tools" }) as HTMLButtonElement).disabled).toBe(true);
  });

  it("will not save an empty set, and shows a refused save as an error", async () => {
    const onSave = vi.fn().mockResolvedValue({ ok: false, code: "INVALID_REQUEST", status: 400 });
    render(<AssistantTools policy={policy(["web"])} onSave={onSave} />);
    fireEvent.click(screen.getByLabelText(/WEB/));
    expect((screen.getByRole("button", { name: "Save tools" }) as HTMLButtonElement).disabled).toBe(true);
    expect(screen.getByRole("status").textContent).toBe("Choose at least one tool.");
    fireEvent.click(screen.getByLabelText(/MEMORY/));
    fireEvent.click(screen.getByRole("button", { name: "Save tools" }));
    await waitFor(() => expect(screen.getByRole("alert")).toBeTruthy());
    expect(onSave).toHaveBeenCalledWith(["memory"]);
  });

  it("parses only well-formed policies", () => {
    expect(parseToolPolicy({ tools: [{ name: "web", label: "Web", state: "on" }], custom: true, reverifying: 2 })).toEqual({
      tools: [{ name: "web", label: "Web", description: "", state: "on", reason: "" }],
      custom: true,
      reverifying: 2,
    });
    for (const bad of [null, {}, { tools: [{ name: "web", state: "maybe" }] }, { tools: [{ state: "on" }] }]) expect(parseToolPolicy(bad), JSON.stringify(bad)).toBeNull();
  });
});
