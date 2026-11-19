import { screen, within } from "@testing-library/react";
import { describe, expect, it } from "vitest";
import { db } from "@/mocks/seed";
import { renderApp } from "@/test/render";

describe("a request's page against the mock API", () => {
  it("says in its header what was asked, by whom and where it stands", async () => {
    renderApp("/requests/REQ-10477");
    const title = await screen.findByRole(
      "heading",
      { level: 1, name: "RHEL 9 server · web-prd-front004" },
      { timeout: 5000 },
    );
    const header = title.closest("header")!;
    expect(within(header).getByText("REQ-10477")).toBeVisible();
    expect(within(header).getByText("Requested by Thomas Nguyen")).toBeVisible();
    expect(within(header).getByRole("link", { name: "Requests" })).toHaveAttribute("href", "/requests");
    expect(within(header).getByRole("button", { name: "Cancel request" })).toBeVisible();

    const output = screen.getByRole("region", { name: "AWX job output" });
    expect(await within(output).findByText("No AWX job")).toBeVisible();
    const asked = screen.getByRole("region", { name: "What was asked for" });
    expect(within(asked).getByText("role")).toBeVisible();
    expect(within(asked).getByText("Reason")).toBeVisible();
  });

  it("shows a table's rows and a list's items in what was asked for", async () => {
    const r = db.requests.find((x) => x.request.number === "REQ-10477")!.request;
    r.inputs = {
      ...r.inputs,
      disks: [
        { size_gb: 50, tier: "gold" },
        { size_gb: 20, tier: "silver" },
      ],
      tags: ["web", "prd"],
    };
    renderApp("/requests/REQ-10477");
    const asked = await screen.findByRole("region", { name: "What was asked for" }, { timeout: 5000 });
    expect(within(asked).getByText("size_gb: 50, tier: gold; size_gb: 20, tier: silver")).toBeVisible();
    expect(within(asked).getByText("web, prd")).toBeVisible();
  });

  it("says whom a level asks besides the users it names", async () => {
    renderApp("/requests/REQ-10471");
    const levels = await screen.findByRole("list", { name: "Approval levels" }, { timeout: 5000 });
    const security = within(levels).getByText("Network security").closest("li")!;
    expect(security).toHaveTextContent("Asks whoever holds Supervisor in the project and the requester's manager");
    expect(security).toHaveTextContent("Waiting for Marc Keller");
  });

  it("leads to the deployment a finished request built", async () => {
    renderApp("/requests/REQ-10461");
    const title = await screen.findByRole("heading", { level: 1, name: /web-tst-search002/ }, { timeout: 5000 });
    const header = title.closest("header")!;
    expect(within(header).getByText(/^All \d+ steps done/)).toBeVisible();
    expect(within(header).getByRole("link", { name: "Open deployment" })).toHaveAttribute(
      "href",
      expect.stringMatching(/^\/deployments\//),
    );
    expect(within(header).getByRole("link", { name: "Request again" })).toBeVisible();
    expect(within(header).queryByRole("button", { name: "Cancel request" })).not.toBeInTheDocument();
  });
});
