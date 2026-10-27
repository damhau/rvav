import { screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { describe, expect, it } from "vitest";
import { db } from "@/mocks/seed";
import { renderApp } from "@/test/render";

describe("approvals inbox against the mock API", () => {
  it("lists what waits for me, oldest first, with the badge", async () => {
    renderApp("/approvals");
    const inbox = await screen.findByRole("region", { name: "Waiting for you" });
    const items = await within(inbox).findAllByRole("button", { pressed: false });
    expect(items).toHaveLength(1); // the first one is selected
    expect(within(inbox).getByRole("button", { pressed: true })).toHaveTextContent("REQ-10477");
    // My own firewall request waits for someone else: not in my inbox.
    expect(within(inbox).queryByText(/Open firewall flow/)).not.toBeInTheDocument();
    expect(await screen.findByLabelText("2 waiting for you")).toBeInTheDocument();
  });

  it("asks for a comment before rejecting", async () => {
    const user = userEvent.setup();
    renderApp("/approvals");
    const detail = await screen.findByRole("article", { name: "Approval for REQ-10477" });
    await user.click(within(detail).getByRole("button", { name: "Reject" }));
    expect(within(detail).getByLabelText("Comment")).toHaveAccessibleDescription(/Say why you reject it/);
    expect(db.approvals.find((a) => a.request.number === "REQ-10477")?.status).toBe("pending");
  });

  it("approves with a lower size, and the request starts", async () => {
    const user = userEvent.setup();
    renderApp("/approvals");
    const detail = await screen.findByRole("article", { name: "Approval for REQ-10477" });
    expect(within(detail).getByText(/Why this needs you/)).toBeInTheDocument();
    const size = within(detail).getByText("Size", { selector: "dt" });
    expect(size.nextElementSibling).toHaveTextContent("8 vCPU · 48 GB · 120 GB");

    await user.click(within(detail).getByRole("radio", { name: /^Large/ }));
    await user.type(within(detail).getByLabelText("Comment"), "32 GB is enough for the JVM.");
    await user.click(within(detail).getByRole("button", { name: "Approve with Large" }));

    // The next approval is selected, and REQ-10477 provisions with 32 GB.
    await screen.findByRole("article", { name: "Approval for REQ-10479" });
    const stored = db.requests.find((r) => r.request.number === "REQ-10477")!;
    await waitFor(() => expect(stored.request.status).toBe("running"));
    expect(stored.request.inputs?.size).toBe("L");
    const level = db.approvals.find((a) => a.request.number === "REQ-10477")!;
    expect(level.outcome).toBe("Approved by Léa Martin");
    expect(level.votes[0]?.changes).toEqual([{ field: "size", label: "Size", from: "XL", to: "L" }]);
    expect(stored.steps[1]?.title).toBe("Approved by Léa Martin");
    expect(stored.steps[1]?.detail).toMatch(/Size changed from XL to L by Léa Martin/);
  });

  it("works for an approver who is in no project", async () => {
    db.me.projects = [];
    const app = renderApp("/catalog");
    expect(await screen.findByText("You are not in a project yet")).toBeInTheDocument();
    await app.router.navigate({ to: "/approvals" });
    expect(await screen.findByRole("article", { name: "Approval for REQ-10477" })).toBeInTheDocument();
  });
});
