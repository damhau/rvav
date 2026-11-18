import { screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { describe, expect, it } from "vitest";
import { db } from "@/mocks/seed";
import { renderApp } from "@/test/render";

const front = "RHEL 9 server · web-prd-front004";
const spark = "Ubuntu 24.04 server · data-prd-spark003";

async function waiting() {
  return screen.findByRole("table", { name: "Waiting for you" }, { timeout: 5000 });
}

describe("approvals against the mock API", () => {
  it("lists what waits for me, oldest first, with the badge", async () => {
    renderApp("/approvals");
    const table = await waiting();
    const rows = (await within(table).findAllByRole("row")).slice(1);
    expect(rows.map((r) => within(r).getByRole("link").textContent)).toEqual([front, spark]);
    expect(rows[0]).toHaveTextContent("REQ-10477");
    expect(rows[0]).toHaveTextContent("Production VM above 32 GB RAM");
    // My own firewall request waits for someone else: not in my list.
    expect(within(table).queryByText(/Open firewall flow/)).not.toBeInTheDocument();
    expect(await screen.findByLabelText("2 waiting for you")).toBeInTheDocument();
    expect(screen.queryByRole("dialog")).not.toBeInTheDocument();
  });

  it("asks for a comment before rejecting", async () => {
    const user = userEvent.setup();
    renderApp("/approvals");
    await user.click(await within(await waiting()).findByRole("link", { name: front }));
    const sheet = await screen.findByRole("dialog", { name: front });
    await user.click(await within(sheet).findByRole("button", { name: "Reject" }));
    const comment = within(sheet).getByLabelText("Comment");
    expect(comment).toHaveAccessibleDescription(/Say why you reject it/);
    expect(comment).toHaveFocus();
    expect(db.approvals.find((a) => a.request.number === "REQ-10477")?.status).toBe("pending");
  });

  it("rejects from a row's menu, with the reason, in the side panel", async () => {
    const user = userEvent.setup();
    renderApp("/approvals");
    const table = await waiting();
    await user.click(await within(table).findByRole("button", { name: "Actions for REQ-10479" }));
    const menu = await screen.findByRole("menu");
    expect(within(menu).getByRole("menuitem", { name: "Approve" })).not.toHaveAttribute("data-disabled");
    await user.click(within(menu).getByRole("menuitem", { name: "Reject…" }));

    const sheet = await screen.findByRole("dialog", { name: spark });
    const comment = await within(sheet).findByLabelText("Comment");
    await waitFor(() => expect(comment).toHaveFocus());
    await user.type(comment, "Run it on the shared cluster.");
    await user.click(within(sheet).getByRole("button", { name: "Reject" }));

    await waitFor(() => expect(screen.queryByRole("dialog")).not.toBeInTheDocument());
    const level = db.approvals.find((a) => a.request.number === "REQ-10479")!;
    expect(level.status).toBe("rejected");
    await waitFor(() => expect(within(table).queryByRole("link", { name: spark })).not.toBeInTheDocument());
  });

  it("approves from a row's menu, and the request starts", async () => {
    const user = userEvent.setup();
    renderApp("/approvals");
    const table = await waiting();
    await user.click(await within(table).findByRole("button", { name: "Actions for REQ-10477" }));
    await user.click(await screen.findByRole("menuitem", { name: "Approve" }));

    const stored = db.requests.find((r) => r.request.number === "REQ-10477")!;
    await waitFor(() => expect(stored.request.status).toBe("running"));
    await waitFor(() => expect(within(table).queryByRole("link", { name: front })).not.toBeInTheDocument());
    expect(within(table).getByRole("link", { name: spark })).toHaveFocus();
  });

  it("approves with a lower size, and the request starts", async () => {
    const user = userEvent.setup();
    renderApp("/approvals");
    await user.click(await within(await waiting()).findByRole("link", { name: front }));
    const sheet = await screen.findByRole("dialog", { name: front });
    expect(await within(sheet).findByText(/Why this needs you/)).toBeInTheDocument();
    const size = within(sheet).getByText("Size", { selector: "dt" });
    expect(size.nextElementSibling).toHaveTextContent("8 vCPU · 48 GB · 120 GB");

    await user.click(within(sheet).getByRole("radio", { name: /^Large/ }));
    await user.type(within(sheet).getByLabelText("Comment"), "32 GB is enough for the JVM.");
    await user.click(within(sheet).getByRole("button", { name: "Approve with Large" }));

    // The panel closes, and REQ-10477 provisions with 32 GB.
    await waitFor(() => expect(screen.queryByRole("dialog")).not.toBeInTheDocument());
    const stored = db.requests.find((r) => r.request.number === "REQ-10477")!;
    await waitFor(() => expect(stored.request.status).toBe("running"));
    expect(stored.request.inputs?.size).toBe("L");
    const level = db.approvals.find((a) => a.request.number === "REQ-10477")!;
    expect(level.outcome).toBe("Approved by Léa Martin");
    expect(level.votes[0]?.changes).toEqual([{ field: "size", label: "Size", from: "XL", to: "L" }]);
    expect(stored.steps[1]?.title).toBe("Approved by Léa Martin");
    expect(stored.steps[1]?.detail).toMatch(/Size changed from XL to L by Léa Martin/);
  });

  it("opens the approval a link names, in the side panel", async () => {
    const id = db.approvals.find((a) => a.request.number === "REQ-10479")!.id;
    renderApp(`/approvals/${id}`);
    expect(await screen.findByRole("dialog", { name: spark }, { timeout: 5000 })).toBeVisible();
  });

  it("says when nothing is decided yet", async () => {
    db.approvals.forEach((a) => {
      if (a.status !== "pending") a.status = "pending";
    });
    renderApp("/approvals?tab=decided");
    expect(await screen.findByText("Nothing decided yet", {}, { timeout: 5000 })).toBeVisible();
  });

  it("works for an approver who is in no project", async () => {
    db.me.projects = [];
    const app = renderApp("/catalog");
    expect(await screen.findByText("You are not in a project yet")).toBeInTheDocument();
    await app.router.navigate({ to: "/approvals" });
    expect(await within(await waiting()).findByRole("link", { name: front })).toBeInTheDocument();
  });
});
