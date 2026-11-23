import { screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { afterEach, describe, expect, it } from "vitest";
import { adminState } from "@/mocks/administration";
import { server } from "@/mocks/node";
import { db, resetDb } from "@/mocks/seed";
import { renderApp } from "@/test/render";

const rows = (region: HTMLElement) =>
  within(region)
    .getAllByRole("row")
    .slice(1)
    .map((r) => within(r).getAllByRole("cell")[0]!.textContent);

describe("Policies", () => {
  it("opens the tab its address names", async () => {
    renderApp("/admin/policies?tab=lease");
    expect(await screen.findByRole("tab", { name: "Lease", selected: true })).toBeInTheDocument();
    expect(await screen.findByRole("region", { name: "Lease policies" })).toBeInTheDocument();
  });

  it("moves, turns off and sets the priority of a day-2 policy from its row menu", async () => {
    const user = userEvent.setup();
    const { router } = renderApp("/admin/policies");
    await user.click(await screen.findByRole("tab", { name: "Day-2" }));
    expect(router.state.location.search).toEqual({ tab: "day2" });
    const region = await screen.findByRole("region", { name: "Day-2 policies" });
    await within(region).findByRole("row", { name: /Members operate their VMs/ });
    expect(rows(region)[2]).toMatch(/^Members operate their VMs/);

    await user.click(within(region).getByRole("button", { name: "Actions for Members operate their VMs" }));
    const down = await screen.findByRole("menuitem", { name: /^Move down/ });
    expect(down).toHaveAttribute("aria-disabled", "true");
    expect(down).toHaveTextContent("It comes last already");
    await user.click(screen.getByRole("menuitem", { name: "Move up" }));
    await waitFor(() => expect(rows(region)[1]).toMatch(/^Members operate their VMs/));

    await user.click(within(region).getByRole("button", { name: "Actions for Administrators" }));
    await user.click(await screen.findByRole("menuitem", { name: "Turn off" }));
    expect(await screen.findByText("Administrators turned off")).toBeInTheDocument();
    expect(await within(region).findByRole("row", { name: /Administrators/ })).toHaveTextContent("Off");

    await user.click(within(region).getByRole("button", { name: "Actions for Administrators" }));
    await user.click(await screen.findByRole("menuitem", { name: "Set priority" }));
    const dialog = await screen.findByRole("dialog", { name: "Priority of Administrators" });
    const priority = within(dialog).getByRole("spinbutton", { name: "Priority" });
    await user.clear(priority);
    await user.type(priority, "99");
    await user.click(within(dialog).getByRole("button", { name: "Set priority" }));
    await waitFor(() => expect(screen.queryByRole("dialog")).toBeNull());
    await waitFor(() => expect(rows(region)[2]).toMatch(/^Administrators/));
  });

  it("names the roles a day-2 policy is for, and ticks them by name", async () => {
    const user = userEvent.setup();
    renderApp("/admin/policies?tab=day2");
    const region = await screen.findByRole("region", { name: "Day-2 policies" });
    const members = await within(region).findByRole("row", { name: /Members operate their VMs/ });
    expect(members).toHaveTextContent(/Owner, Member,? or Project admin/);
    expect(members).not.toHaveTextContent("project-admin");

    await user.click(within(region).getByRole("button", { name: "New day-2 policy" }));
    const sheet = await screen.findByRole("dialog", { name: "New day-2 policy" });
    const roles = within(sheet).getByRole("group", { name: "Roles" });
    expect(roles).toHaveAccessibleDescription(
      "The policy applies to whoever holds one of these roles on the deployment, or to anyone when none is ticked.",
    );
    for (const name of ["Owner", "Viewer", "Member", "Project admin", "Supervisor", "Administrator"])
      expect(within(roles).getByRole("checkbox", { name })).not.toBeChecked();
    await user.type(within(sheet).getByRole("textbox", { name: "Name" }), "Owners and supervisors");
    await user.click(within(roles).getByRole("checkbox", { name: "Owner" }));
    await user.click(within(roles).getByRole("checkbox", { name: "Supervisor" }));
    await user.click(within(sheet).getByRole("button", { name: "Create policy" }));
    expect(await screen.findByText("Owners and supervisors saved")).toBeInTheDocument();
    expect(adminState(db).day2.find((p) => p.name === "Owners and supervisors")).toMatchObject({
      roles: ["owner", "supervisor"],
    });
    expect(await within(region).findByRole("row", { name: /Owners and supervisors/ })).toHaveTextContent(
      "Owner or Supervisor",
    );
  });

  it("removes an approval policy after saying what changes", async () => {
    const user = userEvent.setup();
    renderApp("/admin/policies");
    const region = await screen.findByRole("region", { name: "Approval policies" });
    const menu = await within(region).findByRole("button", { name: "Actions for Production VMs" });
    await user.click(menu);
    await user.click(await screen.findByRole("menuitem", { name: "Remove" }));
    const confirm = await screen.findByRole("alertdialog", { name: "Remove Production VMs?" });
    expect(confirm).toHaveAccessibleDescription(
      "Requests stop waiting for rvav-infra-leads, Marc Keller because of it.",
    );
    expect(confirm).toHaveTextContent("Requests already waiting keep the approvals they were given.");
    await user.click(within(confirm).getByRole("button", { name: "Cancel" }));
    await waitFor(() => expect(menu).toHaveFocus());

    await user.click(menu);
    await user.click(await screen.findByRole("menuitem", { name: "Remove" }));
    await user.click(within(await screen.findByRole("alertdialog")).getByRole("button", { name: "Remove policy" }));
    expect(await screen.findByText("Production VMs removed")).toBeInTheDocument();
    expect(await within(region).findByText("No approval policy")).toBeInTheDocument();
    expect(within(region).getByRole("button", { name: "New approval policy" })).toBeInTheDocument();
  });

  it("makes an approval policy in a side panel", async () => {
    const user = userEvent.setup();
    renderApp("/admin/policies");
    const region = await screen.findByRole("region", { name: "Approval policies" });
    await user.click(await within(region).findByRole("button", { name: "New approval policy" }));
    const sheet = await screen.findByRole("dialog", { name: "New approval policy" });
    expect(within(sheet).getByRole("textbox", { name: "Name" })).toHaveFocus();
    await user.type(within(sheet).getByRole("textbox", { name: "Name" }), "Big VMs");
    await user.type(within(sheet).getByRole("textbox", { name: "OIDC group" }), "rvav-capacity");
    await user.click(within(sheet).getByRole("button", { name: "Create policy" }));
    expect(await screen.findByText("Big VMs saved")).toBeInTheDocument();
    expect(await within(region).findByRole("row", { name: /Big VMs/ })).toHaveTextContent("rvav-capacity");
  });

  it("asks project roles and the requester's manager on a level", async () => {
    const user = userEvent.setup();
    renderApp("/admin/policies");
    const region = await screen.findByRole("region", { name: "Approval policies" });
    await user.click(await within(region).findByRole("button", { name: "New approval policy" }));
    const sheet = await screen.findByRole("dialog", { name: "New approval policy" });
    await user.type(within(sheet).getByRole("textbox", { name: "Name" }), "Supervised");
    const roles = within(sheet).getByRole("group", { name: "Project roles" });
    expect(roles).toHaveAccessibleDescription(/Whoever holds one in the request's project/);
    // Only the roles given in projects: no Administrator.
    expect(within(roles).queryByRole("checkbox", { name: "Administrator" })).toBeNull();
    await user.click(within(roles).getByRole("checkbox", { name: "Supervisor" }));
    await user.click(within(roles).getByRole("checkbox", { name: "Project admin" }));
    await user.click(within(roles).getByRole("checkbox", { name: "Project admin" }));
    await user.click(within(sheet).getByRole("checkbox", { name: "The requester's manager" }));
    await user.click(within(sheet).getByRole("button", { name: "Create policy" }));
    expect(await screen.findByText("Supervised saved")).toBeInTheDocument();
    expect(await within(region).findByRole("row", { name: /Supervised/ })).toHaveTextContent(
      "Supervisor, the requester's manager",
    );
  });

  it("says a level needs someone to ask", async () => {
    const user = userEvent.setup();
    renderApp("/admin/policies");
    const region = await screen.findByRole("region", { name: "Approval policies" });
    await user.click(await within(region).findByRole("button", { name: "New approval policy" }));
    const sheet = await screen.findByRole("dialog", { name: "New approval policy" });
    await user.type(within(sheet).getByRole("textbox", { name: "Name" }), "Nobody");
    await user.click(within(sheet).getByRole("button", { name: "Create policy" }));
    expect(
      await within(sheet).findByText("Give an OIDC group, approvers, project roles or the requester's manager."),
    ).toBeInTheDocument();
  });

  it("removes a project's own lease, and only that", async () => {
    const user = userEvent.setup();
    renderApp("/admin/policies?tab=lease");
    const region = await screen.findByRole("region", { name: "Lease policies" });
    await user.click(await within(region).findByRole("button", { name: "Actions for the lease in Development" }));
    expect(await screen.findByRole("menuitem", { name: "Change" })).toBeInTheDocument();
    expect(screen.queryByRole("menuitem", { name: "Remove" })).toBeNull();
    await user.keyboard("{Escape}");

    await user.click(within(region).getByRole("button", { name: "New lease policy" }));
    const sheet = await screen.findByRole("dialog", { name: "New lease policy" });
    await user.click(within(sheet).getByRole("combobox", { name: "Project" }));
    await user.click(await screen.findByRole("option", { name: "Web Platform" }));
    await user.click(within(sheet).getByRole("button", { name: "Create lease policy" }));
    await waitFor(() => expect(screen.queryByRole("dialog")).toBeNull());

    await user.click(
      await within(region).findByRole("button", { name: "Actions for the lease in Development, Web Platform" }),
    );
    await user.click(await screen.findByRole("menuitem", { name: "Remove" }));
    const confirm = await screen.findByRole("alertdialog", { name: "Remove the lease in Development, Web Platform?" });
    expect(confirm).toHaveTextContent("Deployments keep the lease they have.");
    await user.click(within(confirm).getByRole("button", { name: "Remove lease policy" }));
    await waitFor(() => expect(within(region).queryByRole("row", { name: /Web Platform/ })).toBeNull());
  });

  it(
    "makes a lease rule for small VMs, whose lifetime is no shorter than its maximum",
    { timeout: 15_000 },
    async () => {
      const user = userEvent.setup();
      renderApp("/admin/policies?tab=lease");
      const region = await screen.findByRole("region", { name: "Lease rules" });
      await user.click(await within(region).findByRole("button", { name: "New lease rule" }));
      const sheet = await screen.findByRole("dialog", { name: "New lease rule" });
      await user.click(within(sheet).getByRole("textbox", { name: "Name" }));
      await user.paste("Small VMs");
      await user.click(within(sheet).getByRole("textbox", { name: "Applies when (CEL)" }));
      await user.paste('inputs.size == "S"');
      await user.click(within(sheet).getByRole("combobox", { name: "Environment" }));
      await user.click(await screen.findByRole("option", { name: "Development" }));
      const lifetime = within(sheet).getByRole("spinbutton", { name: "Lifetime, in days" });
      await user.type(lifetime, "20");
      await user.click(within(sheet).getByRole("button", { name: "Create lease rule" }));
      await waitFor(() =>
        expect(lifetime).toHaveAccessibleDescription("The maximum can't be longer than the lifetime."),
      );
      await user.clear(lifetime);
      await user.type(lifetime, "60");
      await user.click(within(sheet).getByRole("button", { name: "Create lease rule" }));
      expect(await screen.findByText("Small VMs saved")).toBeInTheDocument();

      const row = await within(region).findByRole("row", { name: /Small VMs/ });
      expect(row).toHaveTextContent("Development, every project");
      expect(row).toHaveTextContent('inputs.size == "S"');
      expect(row).toHaveTextContent("7 days, up to 30");
      expect(row).toHaveTextContent("Lives 60 days at most");
      expect(adminState(db).leaseRules[0]).toMatchObject({
        environment: "dev",
        max_lifetime_days: 60,
        set_by_admin: true,
      });
    },
  );

  it("names the day-2 actions it explains, their keys aside", async () => {
    const user = userEvent.setup();
    renderApp("/admin/policies?tab=explain");
    await user.click(await screen.findByRole("combobox", { name: "Day-2 action" }));
    const power = await screen.findByRole("option", { name: /^Power on/ });
    expect(power).toHaveTextContent("Power onpower_on");
    const unregister = screen.getAllByRole("option", { name: /^Unregister/ }).map((o) => o.textContent);
    expect(unregister).toEqual(["Unregistervm_unregister", "Unregisterunregister"]);
    expect(screen.queryByRole("option", { name: /^power_on/ })).toBeNull();
    await user.click(power);
    expect(screen.getByRole("combobox", { name: "Day-2 action" })).toHaveTextContent(/^Power on$/);
  });

  it("explains which lease rule decides, read with the inputs given", { timeout: 15_000 }, async () => {
    adminState(db).leaseRules.push({
      id: "lsr_small",
      name: "Small VMs",
      condition: 'inputs.size == "S"',
      environment: "dev",
      project_id: null,
      priority: 10,
      default_days: 7,
      max_days: 30,
      max_extensions: 1,
      grace_days: 7,
      expiry_action: "power_off_then_delete",
      enforcement: "hard",
      max_lifetime_days: 60,
      set_by_admin: true,
    });
    const user = userEvent.setup();
    renderApp("/admin/policies?tab=explain");
    await user.click(await screen.findByRole("combobox", { name: "Environment" }));
    await user.click(await screen.findByRole("option", { name: "Development" }));
    await user.click(screen.getByRole("button", { name: "Explain" }));
    const lease = await screen.findByRole("region", { name: "Lease" });
    expect(lease).toHaveTextContent("Decided by: Development");
    expect(lease).toHaveTextContent(/Small VMs: Its condition needs inputs not given here/);

    const inputs = screen.getByRole("textbox", { name: "Inputs (JSON)" });
    await user.click(inputs);
    await user.paste("[1]");
    await user.click(screen.getByRole("button", { name: "Explain" }));
    expect(await screen.findByText('An object, such as {"size": "S"}.')).toBeInTheDocument();
    expect(inputs).toHaveFocus();
    expect(inputs).toHaveAccessibleDescription(/An object, such as \{"size": "S"\}\./);
    await user.clear(inputs);
    await user.paste('{"size": "S"}');
    await user.click(screen.getByRole("button", { name: "Explain" }));
    await waitFor(() =>
      expect(screen.getByRole("region", { name: "Lease" })).toHaveTextContent("Decided by: Small VMs"),
    );
    expect(screen.getByRole("region", { name: "Lease" })).toHaveTextContent(
      "Deployments get 7 days, up to 30 with up to 1 extensions; at the end: power off then delete. They live 60 days at most: an extension past that needs approval.",
    );
  });

  it(
    "previews and saves a naming policy whose counter starts at 100 and moves by 10",
    { timeout: 15_000 },
    async () => {
      const user = userEvent.setup();
      renderApp("/admin/policies?tab=naming");
      const region = await screen.findByRole("region", { name: "Naming policies" });
      await within(region).findByText("No naming policy");
      await user.click(within(region).getByRole("button", { name: "New naming policy" }));
      const sheet = await screen.findByRole("dialog", { name: "New naming policy" });
      await user.type(within(sheet).getByRole("textbox", { name: "Name" }), "Hundreds");
      const start = within(sheet).getByRole("spinbutton", { name: "Start at" });
      await user.clear(start);
      await user.paste("100");
      const increment = within(sheet).getByRole("spinbutton", { name: "Increment" });
      await user.clear(increment);
      await user.paste("10");
      await user.click(within(sheet).getByRole("button", { name: "Preview names" }));
      expect(await within(sheet).findByText("web-dev-app110, web-dev-app120, web-dev-app130")).toBeInTheDocument();
      await user.click(within(sheet).getByRole("button", { name: "Create naming policy" }));
      expect(await screen.findByText("Hundreds saved")).toBeInTheDocument();
      expect(await within(region).findByRole("row", { name: /Hundreds/ })).toHaveTextContent("From 100, by 10");
    },
  );

  it("says a cleared Start at instead of sending it as null", { timeout: 15_000 }, async () => {
    const user = userEvent.setup();
    const sent: unknown[] = [];
    server.events.on("request:start", ({ request }) => {
      if (request.method === "POST" && request.url.includes("/admin/naming")) sent.push(request.url);
    });
    renderApp("/admin/policies?tab=naming");
    const region = await screen.findByRole("region", { name: "Naming policies" });
    await user.click(await within(region).findByRole("button", { name: "New naming policy" }));
    const sheet = await screen.findByRole("dialog", { name: "New naming policy" });
    await user.type(within(sheet).getByRole("textbox", { name: "Name" }), "Blank");
    const start = within(sheet).getByRole("spinbutton", { name: "Start at" });
    await user.clear(start);
    await user.click(within(sheet).getByRole("button", { name: "Preview names" }));
    expect(start).toHaveAccessibleDescription(/Enter a whole number, such as 1\./);
    await user.click(within(sheet).getByRole("button", { name: "Create naming policy" }));
    expect(start).toHaveAccessibleDescription(/Enter a whole number, such as 1\./);
    expect(sent).toEqual([]);
    server.events.removeAllListeners();
  });
});

// A project admin's page (ADR-0045): every project's policies to read, their
// projects' own to write, and no naming policies.
describe("Policies as a project admin", () => {
  afterEach(() => localStorage.removeItem("rvav.mock.as"));

  it("names the approvers of the policies they read, and gives the ID of one rvav doesn't know", async () => {
    localStorage.setItem("rvav.mock.as", "project-admin");
    resetDb();
    adminState(db).approvals.push({
      id: "pol_web_leavers",
      name: "Web leavers",
      reason: "",
      condition: "true",
      item_type: null,
      project_id: "prj_web",
      levels: [{ name: "Former lead", approvers: ["usr_gone"], mode: "any" }],
      priority: 120,
      enabled: true,
      set_by_admin: false,
    });
    renderApp("/admin/policies");
    const region = await screen.findByRole("region", { name: "Approval policies" });
    const production = await within(region).findByRole("row", { name: /Production VMs/ });
    expect(production).toHaveTextContent("rvav-infra-leads, Marc Keller");
    expect(production).not.toHaveTextContent("usr_mkeller");
    expect(within(region).getByRole("row", { name: /Web leavers/ })).toHaveTextContent("usr_gone");
  });

  it("writes a soft day-2 policy for their project, and only reads every project's", async () => {
    localStorage.setItem("rvav.mock.as", "project-admin");
    resetDb();
    const user = userEvent.setup();
    renderApp("/admin/policies?tab=naming");
    expect(await screen.findByRole("tab", { name: "Approval", selected: true })).toBeInTheDocument();
    expect(screen.queryByRole("tab", { name: "Naming" })).toBeNull();

    await user.click(screen.getByRole("tab", { name: "Day-2" }));
    const region = await screen.findByRole("region", { name: "Day-2 policies" });
    await within(region).findByRole("row", { name: /Members operate their VMs/ });
    expect(within(region).queryByRole("button", { name: /^Actions for/ })).toBeNull();

    await user.click(within(region).getByRole("button", { name: "New day-2 policy" }));
    const sheet = await screen.findByRole("dialog", { name: "New day-2 policy" });
    await user.type(within(sheet).getByRole("textbox", { name: "Name" }), "Powering off web VMs");
    const priority = within(sheet).getByRole("spinbutton", { name: "Priority" });
    await user.clear(priority);
    await user.type(priority, "30");
    await user.click(within(sheet).getByRole("combobox", { name: "Effect" }));
    await user.click(await screen.findByRole("option", { name: "Deny" }));
    await user.click(within(sheet).getByRole("combobox", { name: "Enforcement" }));
    await user.click(await screen.findByRole("option", { name: "Soft: it warns, and the requester may go on" }));
    const actions = within(sheet).getByRole("textbox", { name: "Actions" });
    await user.clear(actions);
    await user.type(actions, "power_off");
    await user.type(within(sheet).getByRole("textbox", { name: "Reason people see" }), "Tell the on-call team.");
    await user.click(within(sheet).getByRole("combobox", { name: "Project" }));
    expect((await screen.findAllByRole("option")).map((o) => o.textContent)).toEqual(["Web Platform"]);
    await user.keyboard("{Escape}");
    await user.click(within(sheet).getByRole("button", { name: "Create policy" }));
    expect(await screen.findByText("Powering off web VMs saved")).toBeInTheDocument();

    const row = await within(region).findByRole("row", { name: /Powering off web VMs/ });
    expect(row).toHaveTextContent("Soft: warns");
    await user.click(within(row).getByRole("button", { name: "Actions for Powering off web VMs" }));
    const up = await screen.findByRole("menuitem", { name: /^Move up/ });
    expect(up).toHaveAttribute("aria-disabled", "true");
    expect(up).toHaveTextContent("It renumbers the policies of every project: set its priority instead");
  });

  it("reads the policies an administrator set for their project, and keeps an override within the environment's", async () => {
    localStorage.setItem("rvav.mock.as", "project-admin");
    resetDb();
    adminState(db).day2.push({
      id: "d2p_web_snapshots",
      name: "Snapshots in Web Platform",
      reason: "Ask the infrastructure team.",
      priority: 40,
      actions: ["snapshot_revert"],
      roles: ["member"],
      condition: "true",
      effect: "deny",
      project_id: "prj_web",
      enabled: true,
      set_by_admin: true,
    });
    adminState(db).leases.push({
      environment: "tst",
      project_id: "prj_web",
      default_days: 14,
      max_days: 60,
      max_extensions: 1,
      grace_days: 7,
      expiry_action: "power_off_then_delete",
      set_by_admin: true,
    });
    const user = userEvent.setup();
    renderApp("/admin/policies?tab=day2");
    const day2 = await screen.findByRole("region", { name: "Day-2 policies" });
    const set = await within(day2).findByRole("row", { name: /Snapshots in Web Platform/ });
    expect(set).toHaveTextContent("Set by an administrator");
    expect(within(set).queryByRole("button", { name: /^Actions for/ })).toBeNull();

    await user.click(screen.getByRole("tab", { name: "Lease" }));
    const leases = await screen.findByRole("region", { name: "Lease policies" });
    const override = await within(leases).findByRole("row", { name: /Test.*Web Platform/ });
    expect(override).toHaveTextContent("Set by an administrator");
    expect(within(override).queryByRole("button", { name: /^Actions for/ })).toBeNull();

    await user.click(within(leases).getByRole("button", { name: "New lease policy" }));
    const sheet = await screen.findByRole("dialog", { name: "New lease policy" });
    const fill = async (name: string, value: string) => {
      const box = within(sheet).getByRole("spinbutton", { name });
      await user.clear(box);
      await user.type(box, value);
    };
    await fill("Extensions", "5");
    await fill("Grace, in days", "30");
    await user.click(within(sheet).getByRole("combobox", { name: "When it expires" }));
    await user.click(await screen.findByRole("option", { name: "Only tell the owner" }));
    await user.click(within(sheet).getByRole("button", { name: "Create lease policy" }));
    expect(await within(sheet).findByText("Development allows 2 extensions at most.")).toBeInTheDocument();
    expect(within(sheet).getByText("Development gives 7 grace days at most.")).toBeInTheDocument();
    expect(within(sheet).getByText("Development's leases end with power off then delete.")).toBeInTheDocument();
  });

  it("keeps their lease rule within the environment's lease, and reads the rules an administrator set", async () => {
    localStorage.setItem("rvav.mock.as", "project-admin");
    resetDb();
    adminState(db).leaseRules.push({
      id: "lsr_gpu",
      name: "GPU servers",
      condition: 'item.slug == "gpu-server"',
      environment: null,
      project_id: "prj_web",
      priority: 10,
      default_days: 14,
      max_days: 30,
      max_extensions: 0,
      grace_days: 3,
      expiry_action: "delete",
      enforcement: "hard",
      max_lifetime_days: 90,
      set_by_admin: true,
    });
    const user = userEvent.setup();
    renderApp("/admin/policies?tab=lease");
    const region = await screen.findByRole("region", { name: "Lease rules" });
    const set = await within(region).findByRole("row", { name: /GPU servers/ });
    expect(set).toHaveTextContent("Set by an administrator");
    expect(set).toHaveTextContent("Lives 90 days at most");
    expect(within(set).queryByRole("button", { name: /^Actions for/ })).toBeNull();

    await user.click(within(region).getByRole("button", { name: "New lease rule" }));
    const sheet = await screen.findByRole("dialog", { name: "New lease rule" });
    await user.click(within(sheet).getByRole("textbox", { name: "Name" }));
    await user.paste("Long builds");
    await user.click(within(sheet).getByRole("textbox", { name: "Applies when (CEL)" }));
    await user.paste("true");
    await user.click(within(sheet).getByRole("combobox", { name: "Environment" }));
    await user.click(await screen.findByRole("option", { name: "Development" }));
    const max = within(sheet).getByRole("spinbutton", { name: "At most, in days" });
    await user.clear(max);
    await user.type(max, "120");
    await user.click(within(sheet).getByRole("button", { name: "Create lease rule" }));
    expect(await within(sheet).findByText("Development allows 90 days at most.")).toBeInTheDocument();
    await waitFor(() => expect(max).toHaveFocus());
    await user.clear(max);
    await user.type(max, "60");
    await user.click(within(sheet).getByRole("button", { name: "Create lease rule" }));
    expect(await screen.findByText("Long builds saved")).toBeInTheDocument();
    const mine = await within(region).findByRole("row", { name: /Long builds/ });
    expect(mine).not.toHaveTextContent("Set by an administrator");
    expect(within(mine).getByRole("button", { name: "Actions for Long builds" })).toBeInTheDocument();
  });
});

// An auditor who is also a project admin reads every project's policies,
// and writes their projects' own only.
describe("Policies as an auditor and a project admin", () => {
  afterEach(() => localStorage.removeItem("rvav.mock.as"));

  it("tells them what decides when nothing is set, not to add a rule or a limit", async () => {
    localStorage.setItem("rvav.mock.as", "auditor");
    resetDb();
    renderApp("/admin/policies?tab=lease");
    const rules = await screen.findByRole("region", { name: "Lease rules" });
    expect(
      await within(rules).findByText("Every deployment gets its environment's lease, or its project's."),
    ).toBeInTheDocument();
    expect(rules).not.toHaveTextContent("You change your projects' own");
    expect(within(rules).queryByRole("button", { name: "New lease rule" })).toBeNull();

    renderApp("/admin/policies?tab=limits");
    const limits = (await screen.findAllByRole("region", { name: "Deployment limits" })).at(-1)!;
    expect(
      await within(limits).findByText(
        "Users and projects may have as many deployments, each as large, as their quotas allow.",
      ),
    ).toBeInTheDocument();
    expect(limits).not.toHaveTextContent("You change your projects' own");
  });

  it("offers only the projects whose policies they write", async () => {
    localStorage.setItem("rvav.mock.as", "auditor");
    resetDb();
    db.me.projects.find((p) => p.project.id === "prj_web")!.permissions.push("project.policies.manage");
    const user = userEvent.setup();
    renderApp("/admin/policies?tab=day2");
    const region = await screen.findByRole("region", { name: "Day-2 policies" });
    await user.click(await within(region).findByRole("button", { name: "New day-2 policy" }));
    const sheet = await screen.findByRole("dialog", { name: "New day-2 policy" });
    await user.click(within(sheet).getByRole("combobox", { name: "Project" }));
    expect((await screen.findAllByRole("option")).map((o) => o.textContent)).toEqual(["Web Platform"]);
  });
});
