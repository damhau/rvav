import { screen, within } from "@testing-library/react";
import { describe, expect, it } from "vitest";
import type { Step } from "@/lib/api/types";
import { i18n } from "@/lib/i18n";
import { db } from "@/mocks/seed";
import { renderApp } from "@/test/render";

const stored = (number: string) => db.requests.find((r) => r.request.number === number)!;
const step = (key: string, system: Step["system"], status: Step["status"], error?: string): Step => ({
  id: `stp_${key}`,
  key,
  title: key,
  system,
  status,
  has_log: false,
  children: [],
  ...(error ? { error } : {}),
});

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
    expect(within(header).getByText(/^From ServiceNow,/)).toHaveTextContent(
      "From ServiceNow, RITM0010032 (opens in a new tab), ordered by Claire Dubois for Léa Martin",
    );
    expect(within(header).getByRole("link", { name: "RITM0010032 (opens in a new tab)" })).toHaveAttribute(
      "href",
      "https://corp.service-now.com/sc_req_item.do?sys_id=9d385017c611228701d22104cc95c371",
    );
  });
});

describe("a request's AWX job output", () => {
  it("shows a composite's AWX job output", async () => {
    renderApp("/requests/REQ-10484");
    const output = await screen.findByRole("region", { name: "AWX job output" }, { timeout: 5000 });
    expect(
      await within(output).findByRole("log", { name: "Output of The database's owner · pg-create-role" }),
    ).toHaveTextContent("permission denied to create role orders_owner");
  });

  it("shows the panel, waiting, for an AWX job that hasn't started", async () => {
    // As the server plans a VM's post-configuration at submit: its job has no log until it starts.
    stored("REQ-10477").steps.push(
      step("awx.inventory", "awx", "pending"),
      step("awx.job:linux-baseline", "awx", "pending"),
    );
    renderApp("/requests/REQ-10477");
    const output = await screen.findByRole("region", { name: "AWX job output" }, { timeout: 5000 });
    expect(within(output).getByText("No output yet")).toBeVisible();
    expect(output).toHaveTextContent("Its AWX job hasn't started. Its output shows here as it runs.");
  });

  it("shows no AWX job output on a composite that runs no AWX job, in English or French", async () => {
    // A composite of REST calls only, failed at its second.
    const r = stored("REQ-10484");
    r.steps = [
      step("submit", "rvav", "done"),
      step("policy", "policy", "done"),
      step("step:nodes", "rest", "done"),
      step("step:owner", "rest", "failed", "REST call: 503 Service Unavailable"),
      step("ready", "rvav", "pending"),
    ];
    r.logs = {};
    r.request.error = { step_id: "stp_step:owner", message: "The CMDB didn't answer: 503 Service Unavailable." };
    const { unmount } = renderApp("/requests/REQ-10484");
    // The failure shows once the steps are in.
    expect(
      (await screen.findAllByText("The CMDB didn't answer: 503 Service Unavailable.", {}, { timeout: 5000 }))[0],
    ).toBeVisible();
    expect(screen.queryByRole("region", { name: "AWX job output" })).not.toBeInTheDocument();
    expect(screen.queryByText("AWX job output")).not.toBeInTheDocument();
    unmount();

    db.me.locale = "fr";
    await i18n.changeLanguage("fr");
    renderApp("/requests/REQ-10484");
    expect(
      (await screen.findAllByText("The CMDB didn't answer: 503 Service Unavailable.", {}, { timeout: 5000 }))[0],
    ).toBeVisible();
    expect(screen.getByRole("region", { name: "Ce qui a été demandé" })).toBeVisible();
    expect(screen.queryByText("Sortie du job AWX")).not.toBeInTheDocument();
  });
});
