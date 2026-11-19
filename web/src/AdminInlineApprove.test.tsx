// @vitest-environment jsdom
import { afterEach, describe, expect, it, vi } from "vitest";
import { cleanup, fireEvent, render, screen, waitFor } from "@testing-library/react";
import { UserList } from "./Admin";
import type { AdminUser } from "./adminApi";

afterEach(cleanup);

const pending: AdminUser = {
  id: "0123456789abcdef0123456789abcdef", email: "p@example.test", displayName: "P", role: "USER", approvalStatus: "PENDING", tenantStatus: "UNPROVISIONED",
  tenantDetail: "", tenantAttempts: 0, tenantCheckedAt: "", createdAt: "", approvedAt: "", lastLoginAt: "", binding: null,
};

const show = (onApprove: (u: AdminUser) => Promise<string>) =>
  render(<UserList users={[pending]} filter="all" search="" onOpen={() => {}} hasMore={false} loadingMore={false} onMore={() => {}} onApprove={onApprove} />);

describe("inline Approve", () => {
  it("calls onApprove with the row's user", async () => {
    const onApprove = vi.fn().mockResolvedValue("");
    show(onApprove);
    fireEvent.click(screen.getByRole("button", { name: "Approve" }));
    await waitFor(() => expect(onApprove).toHaveBeenCalledWith(pending));
    expect(screen.queryByRole("alert")).toBeNull();
  });

  it("shows the error on the row when approving fails", async () => {
    show(vi.fn().mockResolvedValue("Could not approve this account."));
    fireEvent.click(screen.getByRole("button", { name: "Approve" }));
    expect((await screen.findByRole("alert")).textContent).toBe("Could not approve this account.");
  });
});
