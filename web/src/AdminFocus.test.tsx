// @vitest-environment jsdom
import { afterEach, describe, expect, it, vi } from "vitest";
import { cleanup, fireEvent, render, screen } from "@testing-library/react";
import { UserDetail } from "./Admin";
import type { AdminUser } from "./adminApi";

afterEach(cleanup);

const user: AdminUser = {
  id: "0123456789abcdef0123456789abcdef", email: "p@example.test", displayName: "P", role: "USER", approvalStatus: "PENDING", tenantStatus: "UNPROVISIONED",
  tenantDetail: "", tenantAttempts: 0, tenantCheckedAt: "", createdAt: "", approvedAt: "", lastLoginAt: "", binding: null,
};

describe("confirm panel", () => {
  it("takes focus when it opens, so the keyboard user is not left on the trigger", () => {
    render(<UserDetail user={user} onDecide={vi.fn(async () => "")} onRequeue={vi.fn(async () => "")} />);
    const trigger = screen.getByRole("button", { name: "Reject" });
    trigger.focus();
    fireEvent.click(trigger);
    const panel = screen.getByRole("form", { name: "Confirm: Reject" });
    expect(document.activeElement).toBe(panel);
  });
});
