import { screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { HttpResponse, http } from "msw";
import { afterEach, describe, expect, it } from "vitest";
import { adminState } from "@/mocks/administration";
import { server } from "@/mocks/node";
import { db, resetDb } from "@/mocks/seed";
import { renderApp } from "@/test/render";

const idOf = (name: string) => db.deployments.find((d) => d.name === name)!.id;

describe("a deployment's page against the mock API", () => {
  it("names the ServiceNow request it was ordered in", async () => {
    renderApp(`/deployments/${idOf("web-tst-search")}`);
    const details = await screen.findByRole("region", { name: "Details" }, { timeout: 5000 });
    const ritm = within(details).getByRole("link", { name: "RITM0010032 (opens in a new tab)" });
    expect(ritm).toHaveAttribute(
      "href",
      "https://corp.service-now.com/sc_req_item.do?sys_id=9d385017c611228701d22104cc95c371",
    );
    expect(ritm.closest("dd")!.previousElementSibling).toHaveTextContent("ServiceNow request");
  });

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

  it("edits its name and description, refusing a name another of the project's deployments has", async () => {
    const user = userEvent.setup();
    renderApp(`/deployments/${idOf("web-dev-api")}`);
    await user.click(await screen.findByRole("button", { name: "Actions" }, { timeout: 5000 }));
    await user.click(within(await screen.findByRole("menu")).getByRole("menuitem", { name: "Edit deployment" }));
    const dialog = await screen.findByRole("dialog", { name: "Edit deployment · web-dev-api" });
    const name = within(dialog).getByRole("textbox", { name: "Name" });
    expect(name).toHaveValue("web-dev-api");
    await user.clear(name);
    await user.type(name, "web-prd-front");
    await user.click(within(dialog).getByRole("button", { name: "Edit deployment" }));
    expect(await within(dialog).findByText("Web Platform already has a deployment named web-prd-front.")).toBeVisible();
    expect(name).toHaveFocus();

    await user.clear(name);
    await user.type(name, "API gateway");
    await user.type(within(dialog).getByRole("textbox", { name: "Description" }), "Routes the public API.");
    await user.click(within(dialog).getByRole("button", { name: "Edit deployment" }));
    await waitFor(() => expect(screen.queryByRole("dialog")).toBeNull());
    expect(db.requests[0]!.request).toMatchObject({ title: "Edit deployment · web-dev-api", action: { key: "edit" } });
    expect(db.requests[0]!.steps.map((s) => s.title)).toContain("Rename web-dev-api to API gateway");
    expect(await screen.findByRole("heading", { level: 1, name: "API gateway" }, { timeout: 5000 })).toBeVisible();
    expect(screen.getByText("Routes the public API.")).toBeVisible();
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

  it("says a lease's limits only warn under a soft lease policy", async () => {
    const hard = renderApp(`/deployments/${idOf("web-dev-api")}`);
    const lease = await screen.findByRole("region", { name: "Lease" }, { timeout: 5000 });
    expect(lease).toHaveTextContent("1 of 2 extensions used; up to 90 days without approval.");
    expect(lease).not.toHaveTextContent("soft");
    hard.unmount();

    adminState(db).leases.find((l) => l.environment === "dev" && !l.project_id)!.enforcement = "soft";
    renderApp(`/deployments/${idOf("web-dev-api")}`);
    const soft = await screen.findByRole("region", { name: "Lease" }, { timeout: 5000 });
    await waitFor(() =>
      expect(soft).toHaveTextContent(
        "1 of 2 extensions used; up to 90 days. The lease policy is soft: going beyond warns you, and needs no approval.",
      ),
    );
  });

  it("says which lease rule a deployment is under, and how long it may still live", async () => {
    const dep = db.deployments.find((d) => d.name === "web-dev-api")!;
    dep.created_at = new Date(Date.now() - (50 * 24 - 1) * 3_600_000).toISOString();
    adminState(db).leaseRules.push({
      id: "lsr_rhel",
      name: "RHEL builds",
      condition: "true",
      environment: "dev",
      project_id: null,
      priority: 10,
      default_days: 14,
      max_days: 30,
      max_extensions: 2,
      grace_days: 3,
      expiry_action: "delete",
      enforcement: "hard",
      max_lifetime_days: 60,
      set_by_admin: true,
    });
    adminState(db).leaseRuleOf[dep.id] = "lsr_rhel";
    const user = userEvent.setup();
    renderApp(`/deployments/${dep.id}`);
    const lease = await screen.findByRole("region", { name: "Lease" }, { timeout: 5000 });
    await waitFor(() => expect(lease).toHaveTextContent("Set by RHEL builds."));
    expect(lease).toHaveTextContent("1 of 2 extensions used; up to 30 days without approval.");
    expect(lease).toHaveTextContent(
      /It lives 60 days at most, until .+: 10 days left\. Extending it past that needs approval\./,
    );

    await user.click(within(lease).getByRole("button", { name: "Extend the lease" }));
    const dialog = await screen.findByRole("dialog", { name: "Extend the lease · web-dev-api" });
    const days = within(dialog).getByRole("spinbutton", { name: "Days from now" });
    expect(days).toHaveValue(10);
    expect(days).toHaveAccessibleDescription(
      "Up to 30 days without approval. 1 of 2 extensions used. 10 days of its 60-day lifetime left.",
    );
    await user.clear(days);
    await user.type(days, "20");
    await user.click(within(dialog).getByRole("button", { name: "Extend the lease" }));
    await waitFor(() => expect(db.requests[0]?.request.action?.key).toBe("extend_lease"));
    expect(db.requests[0]!.request.status).toBe("pending_approval");
  }, 15000);

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

  it("shows the computer objects rvav made in Active Directory, with their groups", async () => {
    const dep = db.deployments.find((d) => d.name === "web-dev-api")!;
    const dn = "CN=WEB-DEV-API001,OU=Web,DC=corp,DC=example";
    dep.resources.push({
      id: "res_computer",
      type: "ad.computer",
      name: "web-dev-api001",
      sync_state: "in_sync",
      computer: {
        vm: "web-dev-api001",
        dn,
        sam_account_name: "WEB-DEV-API001$",
        dns_host_name: "web-dev-api001.corp.example",
        groups: ["CN=Web servers,OU=Web,DC=corp,DC=example"],
      },
    });
    renderApp(`/deployments/${dep.id}`);
    const panel = await screen.findByRole("region", { name: "Active Directory" }, { timeout: 5000 });
    expect(within(panel).getByText("WEB-DEV-API001$")).toBeVisible();
    expect(within(panel).getByText(dn)).toBeVisible();
    expect(within(panel).getByText("Groups: CN=Web servers,OU=Web,DC=corp,DC=example")).toBeVisible();
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

  it(
    "asks again, with the focus on the warning, when it changed since the menu was listed",
    { timeout: 20_000 },
    async () => {
      localStorage.setItem("rvav.mock.as", "member");
      resetDb();
      const sent: unknown[] = [];
      server.use(
        http.post("*/api/v1/deployments/:id/actions", async ({ request }) => {
          sent.push(await request.clone().json());
          return undefined;
        }),
      );
      const user = userEvent.setup();
      renderApp(`/deployments/${idOf("web-tst-search")}`);
      await user.click(await screen.findByRole("button", { name: "Actions" }, { timeout: 5000 }));
      const menu = await screen.findByRole("menu");
      const vm = within(menu).getByRole("menuitem", { name: "web-tst-search001" });
      await user.click(vm);
      await waitFor(() => expect(vm).toHaveAttribute("aria-expanded", "true"));
      const sub = screen.getAllByRole("menu").find((m) => m !== menu)!;
      within(sub)
        .getByRole("menuitem", { name: /^Power off/ })
        .focus();
      await user.keyboard("{Enter}");
      const dialog = await screen.findByRole("dialog", { name: "Power off · web-tst-search001" });
      adminState(db).day2.find((p) => p.name === "Powering off web VMs")!.reason = "Ask the on-call team first.";

      await user.click(within(dialog).getByRole("button", { name: "Power off anyway" }));
      const note = within(dialog).getByRole("note", { name: "Policy warnings" });
      await waitFor(() => expect(note).toHaveFocus());
      expect(note).toHaveAccessibleDescription(/Ask the on-call team first\. \(Powering off web VMs\)/);
      expect(db.requests.some((r) => r.request.kind === "day2")).toBe(false);

      await user.click(within(dialog).getByRole("button", { name: "Power off anyway" }));
      await waitFor(() => expect(screen.queryByRole("dialog")).toBeNull());
      expect(sent).toHaveLength(2);
      expect(sent[1]).toMatchObject({ accepted_warnings: [expect.any(String), expect.any(String)] });
      expect(db.requests[0]!.request.warnings).toEqual([
        expect.objectContaining({ message: "Ask the on-call team first." }),
      ]);
    },
  );

  it("shows a member of an owners-only project their own deployments only", { timeout: 20_000 }, async () => {
    localStorage.setItem("rvav.mock.as", "member");
    resetDb();
    renderApp(`/deployments/${idOf("web-dev-api")}`);
    expect(await screen.findByText("Deployment not found", {}, { timeout: 5000 })).toBeInTheDocument();
  });
});
