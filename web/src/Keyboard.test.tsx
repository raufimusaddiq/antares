// @vitest-environment jsdom
import { afterEach, expect, it, vi } from "vitest";
import { cleanup, fireEvent, render, screen } from "@testing-library/react";

vi.mock("./chatApi", async (original) => ({
  ...(await original<typeof import("./chatApi")>()),
  listChats: vi.fn().mockResolvedValue({ ok: true, value: { chats: [], nextCursor: "" } }),
}));
import { Chat } from "./Chat";

afterEach(cleanup);

it("reaches the composer with Alt+Shift+M outside fields and preserves typing, selection and IME input inside them", async () => {
  render(<Chat email="test@example.test" onSignOut={() => {}} />);
  await screen.findByText("No conversations yet.");
  const composer = screen.getByRole("textbox", { name: "Message" });
  const newChat = screen.getByRole("button", { name: "New chat" });
  newChat.focus();
  const shortcut = { key: "M", altKey: true, shiftKey: true };
  expect(fireEvent.keyDown(newChat, shortcut)).toBe(false);
  expect(document.activeElement).toBe(composer);
  for (const field of [screen.getByRole("searchbox"), screen.getByRole("combobox"), composer]) {
    field.focus();
    expect(fireEvent.keyDown(field, shortcut)).toBe(true);
    expect(document.activeElement).toBe(field);
  }
  const editable = document.createElement("div");
  editable.setAttribute("contenteditable", "true");
  editable.tabIndex = 0;
  const child = editable.appendChild(document.createElement("span"));
  document.body.appendChild(editable);
  editable.focus();
  expect(fireEvent.keyDown(child, shortcut)).toBe(true);
  expect(document.activeElement).toBe(editable);
  editable.remove();
  for (const event of [{ ...shortcut, ctrlKey: true }, { ...shortcut, metaKey: true }, { ...shortcut, isComposing: true }, { key: "m" }, { key: "M", shiftKey: true }]) {
    newChat.focus();
    expect(fireEvent.keyDown(newChat, event)).toBe(true);
    expect(document.activeElement).toBe(newChat);
  }
});
