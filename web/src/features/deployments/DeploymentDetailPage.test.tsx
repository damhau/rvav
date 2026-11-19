import { screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { HttpResponse, http } from "msw";
import { afterEach, describe, expect, it } from "vitest";
import { server } from "@/mocks/node";
import { db, resetDb } from "@/mocks/seed";
import { renderApp } from "@/test/render";

const idOf = (name: string) => db.deployments.find((d) => d.name === name)!.id;

describe("a deployment's page against the mock API", () => {
  it("has every action of a one-VM deployment in its Actions menu, each it can't run with the reason", async () => {
    const user = userEvent.setup();
    renderApp(`/deployments/${idOf("web-dev-api")}`);
    expect(await screen.findByRole("heading", { level: 1, name: "web-dev-api" }, { timeout: 5000 })).toBeVisible();
    expect(screen.getByText("Owner Thomas Nguyen")).toBeInTheDocument();

    await user.click(await screen.findByRole("button", { name: "Actions" }));
    const menu = await screen.findByRole("menu");
    for (const group of ["Power", "Snapshots", "Lifecycle"]) expect(within(menu).getByText(group)).toBeVisible();
    const powerOn = within(menu).getByRole("menuitem", { name: /^Power on/ });
    expect(powerOn).toHaveAttribute("data-disabled");
    expect(powerOn).toHaveTextContent("The VM is already powered on.");
    expect(within(menu).getByRole("menuitem", { name: /^Take a snapshot/ })).not.toHaveAttribute("data-disabled");
    expect(within(menu).getByRole("menuitem", { name: "Delete" })).not.toHaveAttribute("data-disabled");
  });

  it("says when its actions couldn't be read, and reads them again", async () => {
    const user = userEvent.setup();
    let fail = true;
    server.use(
      http.get("*/api/v1/deployments/:id/actions", () =>
        fail
          ? HttpResponse.json(
              { type: "about:blank", title: "Unavailable", status: 503, request_id: "req" },
              { status: 503, headers: { "Content-Type": "application/problem+json" } },
            )
          : undefined,
      ),
    );
    renderApp(`/deployments/${idOf("web-dev-api")}`);
    await user.click(await screen.findByRole("button", { name: "Actions" }, { timeout: 5000 }));
    expect(await screen.findByText("The actions could not be loaded", {}, { timeout: 5000 })).toBeInTheDocument();
    fail = false;
    await user.click(screen.getByRole("menuitem", { name: "Try again" }));
    await user.click(await screen.findByRole("button", { name: "Actions" }));
    expect(await screen.findByRole("menuitem", { name: "Delete" })).toBeInTheDocument();
  });

  it("asks before a risky action, saying what it changes", async () => {
    const user = userEvent.setup();
    renderApp(`/deployments/${idOf("web-dev-api")}`);
    await user.click(await screen.findByRole("button", { name: "Actions" }, { timeout: 5000 }));
    await user.click(await screen.findByRole("menuitem", { name: "Delete" }));
    const dialog = await screen.findByRole("dialog", { name: "Delete · web-dev-api" });
    expect(dialog).toHaveTextContent("Deletes the VMs and releases their IP addresses and DNS records.");
    const confirm = within(dialog).getByRole("button", { name: "Delete" });
    expect(confirm).toBeDisabled();
    // What it asks has the focus, not Close.
    await waitFor(() => expect(within(dialog).getByRole("textbox", { name: /to confirm/ })).toHaveFocus());
    await user.type(within(dialog).getByRole("textbox", { name: /to confirm/ }), "web-dev-api");
    expect(confirm).toBeEnabled();
  });

  it("puts each VM of a multi-VM deployment in a submenu of its own", async () => {
    const user = userEvent.setup();
    renderApp(`/deployments/${idOf("web-prd-front")}`);
    await user.click(await screen.findByRole("button", { name: "Actions" }, { timeout: 5000 }));
    const menu = await screen.findByRole("menu");
    expect(within(menu).getByText("Virtual machines")).toBeVisible();
    expect(within(menu).queryByRole("menuitem", { name: /^Power off/ })).not.toBeInTheDocument();
    const vm = within(menu).getByRole("menuitem", { name: "web-prd-front002" });
    await user.click(vm);
    await waitFor(() => expect(vm).toHaveAttribute("aria-expanded", "true"));
    const sub = screen.getAllByRole("menu").find((m) => m !== menu)!;
    expect(within(sub).getByRole("menuitem", { name: /^Power off/ })).toBeInTheDocument();
  });

  it("keeps its tab in the address, and lists only the details it has", async () => {
    const user = userEvent.setup();
    const { router } = renderApp(`/deployments/${idOf("web-dev-worker")}?tab=history`);
    expect(await screen.findByRole("tab", { name: "History", selected: true }, { timeout: 5000 })).toBeVisible();
    expect(await screen.findByRole("region", { name: "History" })).toBeVisible();

    await user.click(screen.getByRole("tab", { name: "Overview" }));
    await waitFor(() => expect(router.state.location.search).toEqual({}));
    const details = await screen.findByRole("region", { name: "Details" });
    expect(within(details).getByRole("link", { name: "REQ-10482" })).toHaveAttribute("href", "/requests/REQ-10482");
    expect(within(details).getByRole("link", { name: "RHEL 9 server" })).toHaveAttribute(
      "href",
      "/catalog/rhel9-server",
    );
    for (const dd of details.querySelectorAll("dd")) expect(dd.textContent?.trim()).not.toBe("");

    await user.click(screen.getByRole("tab", { name: "Inputs" }));
    await waitFor(() => expect(router.state.location.search).toEqual({ tab: "inputs" }));
    const inputs = await screen.findByRole("region", { name: "Inputs" });
    expect(inputs).toHaveTextContent("role: worker");
    // An update, a join or an unregister changes them since: no request is named.
    expect(inputs).toHaveTextContent("What it was requested or last updated with");
    expect(inputs).not.toHaveTextContent("REQ-10482");
  });

  it("shows the address a VM on DHCP reports, once it has one", async () => {
    const dep = db.deployments.find((d) => d.name === "web-prd-edge")!;
    const second = dep.resources.find((r) => r.name === "web-prd-edge002")!;
    if (second.type !== "vsphere.vm") throw new Error("no VM");
    second.vm.nics[0]!.ip_addresses = [];
    renderApp(`/deployments/${dep.id}`);
    const network = await screen.findByRole("region", { name: "Network and addresses" }, { timeout: 5000 });
    const rows = await within(network).findAllByRole("listitem");
    expect(rows.map((r) => r.textContent)).toEqual([
      "10.21.10.12DHCP · web-prd-edge001",
      "Address not known yetDHCP · web-prd-edge002",
    ]);
  });
});

// The "member" persona: Web Platform shares its deployments with their
// owners only, and a soft policy of its own warns before a VM powers off
// (ADR-0045).
describe("a soft day-2 policy", () => {
  afterEach(() => localStorage.removeItem("rvav.mock.as"));

  it("warns a member in the action's dialog, and runs the action once they go on", { timeout: 20_000 }, async () => {
    localStorage.setItem("rvav.mock.as", "member");
    resetDb();
    const user = userEvent.setup();
    renderApp(`/deployments/${idOf("web-tst-search")}`);
    await user.click(await screen.findByRole("button", { name: "Actions" }, { timeout: 5000 }));
    const menu = await screen.findByRole("menu");
    const vm = within(menu).getByRole("menuitem", { name: "web-tst-search001" });
    await user.click(vm);
    await waitFor(() => expect(vm).toHaveAttribute("aria-expanded", "true"));
    const sub = screen.getAllByRole("menu").find((m) => m !== menu)!;
    const off = within(sub).getByRole("menuitem", { name: /^Power off/ });
    expect(off).toHaveTextContent("warns");
    // Under jsdom a click on a submenu's item doesn't select it; Enter does.
    off.focus();
    await user.keyboard("{Enter}");

    const dialog = await screen.findByRole("dialog", { name: "Power off · web-tst-search001" });
    expect(within(dialog).getByRole("note", { name: "Policy warning" })).toHaveTextContent(
      "Web Platform's VMs serve the checkout: tell the on-call team before you power one off. (Powering off web VMs)",
    );
    await user.click(within(dialog).getByRole("button", { name: "Power off anyway" }));
    await waitFor(() => expect(screen.queryByRole("dialog")).toBeNull());
    expect(db.requests[0]!.request).toMatchObject({
      kind: "day2",
      warnings: [expect.objectContaining({ policy: "Powering off web VMs" })],
    });
  });

  it("shows a member of an owners-only project their own deployments only", { timeout: 20_000 }, async () => {
    localStorage.setItem("rvav.mock.as", "member");
    resetDb();
    renderApp(`/deployments/${idOf("web-dev-api")}`);
    expect(await screen.findByText("Deployment not found", {}, { timeout: 5000 })).toBeInTheDocument();
  });
});
