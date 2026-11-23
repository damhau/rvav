import { screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { afterEach, describe, expect, it } from "vitest";
import { adminState } from "@/mocks/administration";
import { customRoles } from "@/mocks/roles";
import { db, resetDb } from "@/mocks/seed";
import { renderApp } from "@/test/render";

// A project's members as its project admin sees them (ADR-0034): the
// rvav.mock.as persona, whose Web Platform has Thomas Nguyen holding VM
// operator, which they may not take away.
function asProjectAdmin() {
  localStorage.setItem("rvav.mock.as", "project-admin");
  resetDb();
}

afterEach(() => localStorage.removeItem("rvav.mock.as"));

describe("a project's members", () => {
  it("offers no removal the server would refuse, and says a refusal by the row", async () => {
    asProjectAdmin();
    const user = userEvent.setup();
    renderApp("/admin/projects/prj_web");
    const members = await screen.findByRole("region", { name: "Members" });
    expect(await within(members).findByRole("list", { name: "Roles of Thomas Nguyen" })).toHaveTextContent(
      "VM operator",
    );
    await user.click(await within(members).findByRole("button", { name: "Actions for Thomas Nguyen" }));
    expect(await screen.findByRole("menuitem", { name: "Change roles" })).toBeInTheDocument();
    expect(screen.queryByRole("menuitem", { name: "Remove" })).toBeNull();

    await user.click(screen.getByRole("menuitem", { name: "Change roles" }));
    expect(await screen.findByRole("dialog", { name: "Roles of Thomas Nguyen" })).toHaveTextContent(
      "VM operator(you may not take it away)",
    );
    await user.click(screen.getByRole("button", { name: "Cancel" }));

    await user.click(within(members).getByRole("button", { name: "Actions for Léa Martin" }));
    await user.click(await screen.findByRole("menuitem", { name: "Remove" }));
    const confirm = await screen.findByRole("alertdialog", { name: "Remove Léa Martin?" });
    await user.click(within(confirm).getByRole("button", { name: "Remove member" }));
    expect(await within(members).findByRole("alert")).toHaveTextContent(
      "Web Platform would have no project admin: make someone else one first.",
    );
    expect(screen.getAllByText(/would have no project admin/)).toHaveLength(1);
  });

  it("starts adding someone as a role the caller may give", async () => {
    asProjectAdmin();
    // Members managed through a role without request.submit: Member is not theirs to give.
    customRoles(db).push({
      key: "membership-manager",
      name: "Membership manager",
      description: "",
      permissions: ["project.members.manage", "project.see", "project.settings.read"],
      built_in: false,
    });
    db.me.projects = db.me.projects.map((p) =>
      p.project.id === "prj_web"
        ? {
            ...p,
            roles: [{ key: "membership-manager", name: "Membership manager" }],
            permissions: ["project.members.manage", "project.see", "project.settings.read"],
          }
        : p,
    );
    const user = userEvent.setup();
    renderApp("/admin/projects/prj_web");
    const members = await screen.findByRole("region", { name: "Members" });
    const as = await within(members).findByRole("combobox", { name: "As" });
    await waitFor(() => expect(as).toHaveTextContent("Viewer"));
    await user.type(within(members).getByRole("textbox", { name: "Add someone" }), "Claire");
    await user.click(await within(members).findByRole("button", { name: /Claire Dubois/ }));
    expect(await within(members).findByRole("list", { name: "Roles of Claire Dubois" })).toHaveTextContent("Viewer");
    expect(within(members).queryByRole("alert")).toBeNull();
  });
});

describe("a project's placement and quotas", () => {
  it("removes a placement rule after saying what is left", async () => {
    const user = userEvent.setup();
    renderApp("/admin/projects/prj_web?tab=placement");
    const rules = await screen.findByRole("region", { name: "Placement rules" });
    const menus = await within(rules).findAllByRole("button", { name: /^Actions for the rule of priority/ });
    await user.click(menus[0]!);
    await user.click(await screen.findByRole("menuitem", { name: "Remove" }));
    const confirm = await screen.findByRole("alertdialog", {
      name: /^Remove the rule of priority \d+ in Development\?$/,
    });
    expect(confirm).toHaveTextContent(/nowhere to go|other rules? stay/);
    await user.click(within(confirm).getByRole("button", { name: "Remove rule" }));
    expect(await screen.findByText("Placement rule removed")).toBeInTheDocument();
  });

  it("names a resource pool, hosts, a datastore cluster and a strategy in a rule (ADR-0036)", async () => {
    const user = userEvent.setup();
    renderApp("/admin/projects/prj_web?tab=placement");
    const rules = await screen.findByRole("region", { name: "Placement rules" });
    const menus = await within(rules).findAllByRole("button", { name: /^Actions for the rule of priority/ });
    await user.click(menus[0]!);
    await user.click(await screen.findByRole("menuitem", { name: "Change" }));
    const sheet = await screen.findByRole("dialog", { name: "Change placement rule for Web Platform in Development" });
    expect(within(sheet).getByRole("combobox", { name: "Strategy" })).toHaveTextContent("Most free memory");
    await user.type(within(sheet).getByLabelText("Resource pool"), "/rvav/dev/");
    await user.type(within(sheet).getByLabelText("Hosts"), "esx-gva-dev-0*");
    await user.clear(within(sheet).getByLabelText("Datastores"));
    await user.type(within(sheet).getByLabelText("Datastore cluster"), "dsc-gva-dev-01");
    await user.click(within(sheet).getByRole("combobox", { name: "Strategy" }));
    await user.click(await screen.findByRole("option", { name: /^Binpack/ }));
    await user.click(within(sheet).getByRole("button", { name: "Save rule" }));
    expect(await screen.findByText("Placement rule saved")).toBeInTheDocument();
    const saved = adminState(db).rules.find((r) => r.id === "plc_web_dev")!;
    expect(saved).toMatchObject({
      resource_pool: "rvav/dev",
      hosts: ["esx-gva-dev-0*"],
      datastores: [],
      datastore_cluster: "dsc-gva-dev-01",
      strategy: "binpack",
    });
    const row = await within(rules).findByRole("row", { name: /pool rvav\/dev/ });
    expect(row).toHaveTextContent("hosts esx-gva-dev-0*");
    expect(row).toHaveTextContent("cluster dsc-gva-dev-01");
    expect(row).toHaveTextContent("Binpack");
  });

  it("shows Any environment as chosen, and saves it as no environment", async () => {
    const user = userEvent.setup();
    renderApp("/admin/projects/prj_web?tab=placement");
    const directory = await screen.findByRole("region", { name: "Active Directory" });
    const row = await within(directory).findByRole("row", { name: /^Any environment/ });
    await user.click(within(row).getByRole("button", { name: /^Actions for the OU/ }));
    await user.click(await screen.findByRole("menuitem", { name: "Change" }));
    const sheet = await screen.findByRole("dialog", { name: "Change the Active Directory OU for Web Platform" });
    const environment = within(sheet).getByRole("combobox", { name: "Environment" });
    expect(environment).toHaveTextContent("Any environment");
    expect(environment).not.toHaveAttribute("data-placeholder");
    await user.click(environment);
    await user.click(await screen.findByRole("option", { name: "Development" }));
    await user.click(environment);
    await user.click(await screen.findByRole("option", { name: "Any environment" }));
    expect(environment).toHaveTextContent("Any environment");
    await user.click(within(sheet).getByRole("button", { name: "Save OU" }));
    expect(await screen.findByText("OU saved")).toBeInTheDocument();
    const anyRows = adminState(db).adPlacements.filter((r) => r.project_id === "prj_web" && r.environment === null);
    expect(anyRows).toHaveLength(1);
  });

  it("gives the project an OU in Active Directory for an environment, with groups (ADR-0064)", async () => {
    const user = userEvent.setup();
    renderApp("/admin/projects/prj_web?tab=placement");
    const directory = await screen.findByRole("region", { name: "Active Directory" });
    const table = await within(directory).findByRole("table", { name: "Active Directory OUs of Web Platform" });
    expect(await within(table).findByRole("row", { name: /^Any environment/ })).toHaveTextContent(
      "OU=Web,OU=Servers,DC=corp,DC=example",
    );
    // Every project's, shown and changed elsewhere.
    const shared = within(table).getByRole("row", { name: /^Every project in Production/ });
    expect(shared).toHaveTextContent("Set in Projects › Environments");

    await user.click(within(directory).getByRole("button", { name: "New OU" }));
    const sheet = await screen.findByRole("dialog", { name: "New Active Directory OU for Web Platform" });
    await user.click(within(sheet).getByRole("combobox", { name: "Environment" }));
    await user.click(await screen.findByRole("option", { name: "Development" }));
    await user.type(within(sheet).getByLabelText("OU"), "Servers");
    await user.click(within(sheet).getByRole("button", { name: "Create OU" }));
    expect(await within(sheet).findByText(/"Servers" is not a DN/)).toBeInTheDocument();

    await user.clear(within(sheet).getByLabelText("OU"));
    await user.type(within(sheet).getByLabelText("OU"), "OU=Dev,OU=Web");
    await user.type(within(sheet).getByLabelText("Groups"), "CN=Web servers,OU=Groups{Enter}CN=Dev,OU=Groups");
    await user.click(within(sheet).getByLabelText(/Blueprints may choose their OU/));
    await user.click(within(sheet).getByRole("button", { name: "Create OU" }));
    expect(await screen.findByText("OU saved")).toBeInTheDocument();
    const saved = adminState(db).adPlacements.find((r) => r.project_id === "prj_web" && r.environment === "dev")!;
    expect(saved).toMatchObject({
      ou_dn: "OU=Dev,OU=Web,DC=corp,DC=example",
      groups: ["CN=Web servers,OU=Groups", "CN=Dev,OU=Groups"],
      allow_blueprint_ou: true,
    });
    const row = await within(table).findByRole("row", { name: /^In Development/ });
    expect(row).toHaveTextContent("May choose their OU");

    await user.click(within(row).getByRole("button", { name: /^Actions for the OU/ }));
    await user.click(await screen.findByRole("menuitem", { name: "Remove" }));
    const confirm = await screen.findByRole("alertdialog", {
      name: "Stop making computer objects in OU=Dev,OU=Web,DC=corp,DC=example?",
    });
    expect(confirm).toHaveTextContent("Objects already made stay");
    await user.click(within(confirm).getByRole("button", { name: "Remove OU" }));
    expect(await screen.findByText("OU removed")).toBeInTheDocument();
  });

  it("keeps a role placing VMs in this project only to what every project's OU delegates, by the field", async () => {
    asProjectAdmin();
    db.me.projects = db.me.projects.map((p) =>
      p.project.id === "prj_web" ? { ...p, permissions: [...p.permissions, "placement.manage"] } : p,
    );
    const user = userEvent.setup();
    renderApp("/admin/projects/prj_web?tab=placement");
    const directory = await screen.findByRole("region", { name: "Active Directory" });
    await user.click(await within(directory).findByRole("button", { name: "New OU" }));
    const sheet = await screen.findByRole("dialog", { name: "New Active Directory OU for Web Platform" });
    await user.click(within(sheet).getByRole("combobox", { name: "Environment" }));
    await user.click(await screen.findByRole("option", { name: "Production" }));
    await user.type(within(sheet).getByLabelText("OU"), "OU=Web,OU=Servers");
    await user.type(within(sheet).getByLabelText("Groups"), "CN=Admins,OU=Groups");
    await user.click(within(sheet).getByRole("button", { name: "Create OU" }));
    expect(
      await within(sheet).findByText(/its OU goes under OU=Production,OU=Servers, the one set/),
    ).toBeInTheDocument();
    expect(within(sheet).getByLabelText("Groups")).toHaveAccessibleDescription(
      /CN=Admins,OU=Groups is not under the project's OU\./,
    );

    await user.clear(within(sheet).getByLabelText("OU"));
    await user.type(within(sheet).getByLabelText("OU"), "OU=Web,OU=Production,OU=Servers");
    await user.clear(within(sheet).getByLabelText("Groups"));
    await user.type(within(sheet).getByLabelText("Groups"), "CN=Web,OU=Web,OU=Production,OU=Servers");
    await user.click(within(sheet).getByRole("button", { name: "Create OU" }));
    expect(await screen.findByText("OU saved")).toBeInTheDocument();
  });

  it("says in a toast, not by a field, that Active Directory didn't answer", async () => {
    adminState(db).integrations.ad!.settings.url = "ldaps://unreachable.corp.example";
    const user = userEvent.setup();
    renderApp("/admin/projects/prj_web?tab=placement");
    const directory = await screen.findByRole("region", { name: "Active Directory" });
    await user.click(await within(directory).findByRole("button", { name: "New OU" }));
    const sheet = await screen.findByRole("dialog", { name: "New Active Directory OU for Web Platform" });
    await user.click(within(sheet).getByRole("combobox", { name: "Environment" }));
    await user.click(await screen.findByRole("option", { name: "Development" }));
    await user.type(within(sheet).getByLabelText("OU"), "OU=Dev,OU=Web");
    await user.click(within(sheet).getByRole("button", { name: "Create OU" }));
    expect(await screen.findByText("Active Directory did not answer")).toBeInTheDocument();
    expect(within(sheet).getByLabelText("OU")).not.toHaveAttribute("aria-invalid", "true");
  });

  it("adds a limit in a sheet, refuses a second one on the same resource and place, and shows what is taken", async () => {
    const user = userEvent.setup();
    renderApp("/admin/projects/prj_web?tab=quotas");
    const quotas = await screen.findByRole("region", { name: "Quotas" });
    const vcpu = await within(quotas).findByRole("row", { name: /^vCPU/ });
    await waitFor(() => expect(vcpu).toHaveTextContent("112 of 160"));
    await user.click(within(quotas).getByRole("button", { name: "Add a limit" }));
    const sheet = await screen.findByRole("dialog", { name: "Add a limit" });
    // Every resource has a project-wide limit: it opens on the first place free.
    expect(within(sheet).getByRole("combobox", { name: "Resource" })).toHaveTextContent("vCPU");
    expect(within(sheet).getByRole("combobox", { name: "In" })).toHaveTextContent("Development");
    const twin = "This resource has a limit there already: change that one.";
    expect(within(sheet).queryByText(twin)).toBeNull();
    await user.click(within(sheet).getByRole("combobox", { name: "In" }));
    await user.click(await screen.findByRole("option", { name: "The whole project" }));
    expect(within(sheet).getByText(twin)).toBeInTheDocument();
    expect(within(sheet).getByRole("button", { name: "Add limit" })).toBeDisabled();
    await user.click(within(sheet).getByRole("combobox", { name: "In" }));
    await user.click(await screen.findByRole("option", { name: "Development" }));
    await user.type(within(sheet).getByRole("spinbutton", { name: "Limit" }), "16");
    await user.click(within(sheet).getByRole("button", { name: "Add limit" }));
    expect(await screen.findByText("Limit saved")).toBeInTheDocument();
    expect(await within(quotas).findByRole("row", { name: /^vCPU Development 16/ })).toBeInTheDocument();
  });

  it("opens the tab the address names", async () => {
    renderApp("/admin/projects/prj_web?tab=details");
    expect(await screen.findByRole("region", { name: "Details" })).toBeInTheDocument();
    expect(screen.getByRole("tab", { name: "Details" })).toHaveAttribute("aria-selected", "true");
  });

  it("adds a custom property, says a bad key by its field, and removes one", { timeout: 15_000 }, async () => {
    const user = userEvent.setup();
    renderApp("/admin/projects/prj_web?tab=details");
    const details = await screen.findByRole("region", { name: "Details" });
    expect(await within(details).findByDisplayValue("backupPolicy")).toBeInTheDocument();
    await user.click(within(details).getByRole("button", { name: "Add property" }));
    const keys = within(details).getAllByRole("textbox", { name: "Key" });
    await user.click(keys[1]!);
    await user.paste("1st");
    await user.click(within(details).getAllByRole("textbox", { name: "Value" })[1]!);
    await user.paste("CC-1001");
    await user.click(within(details).getByRole("button", { name: "Save details" }));
    expect(await within(details).findByText(/A key of up to 64 letters/)).toBeInTheDocument();

    await user.clear(keys[1]!);
    await user.paste("costCenter");
    await user.click(within(details).getByRole("button", { name: "Remove property backupPolicy" }));
    await user.click(within(details).getByRole("button", { name: "Save details" }));
    expect(await screen.findByText("Web Platform saved")).toBeInTheDocument();
    expect(adminState(db).extra.prj_web!.properties).toEqual({ costCenter: "CC-1001" });
  });

  it("says a key given twice or left out, and the server's problem with a key typed with spaces", async () => {
    const user = userEvent.setup();
    renderApp("/admin/projects/prj_web?tab=details");
    const details = await screen.findByRole("region", { name: "Details" });
    await within(details).findByDisplayValue("backupPolicy");
    const add = async (key: string, value: string) => {
      await user.click(within(details).getByRole("button", { name: "Add property" }));
      const n = within(details).getAllByRole("textbox", { name: "Key" }).length - 1;
      if (key) {
        await user.click(within(details).getAllByRole("textbox", { name: "Key" })[n]!);
        await user.paste(key);
      }
      await user.click(within(details).getAllByRole("textbox", { name: "Value" })[n]!);
      await user.paste(value);
    };
    await add("backupPolicy", "weekly");
    await add("", "orphan");
    await user.click(within(details).getByRole("button", { name: "Save details" }));
    const keys = within(details).getAllByRole("textbox", { name: "Key" });
    expect(keys[1]).toHaveAccessibleDescription("backupPolicy is given twice.");
    expect(keys[2]).toHaveAccessibleDescription("Give the property a key.");
    expect(Object.keys(adminState(db).extra.prj_web!.properties ?? {})).toEqual(["backupPolicy"]);

    await user.click(within(details).getAllByRole("button", { name: "Remove property backupPolicy" })[1]!);
    await user.click(within(details).getByRole("button", { name: "Remove property 2" }));
    await add(" 1st ", "CC-1001");
    await user.click(within(details).getByRole("button", { name: "Save details" }));
    await waitFor(() =>
      expect(within(details).getAllByRole("textbox", { name: "Key" })[1]).toHaveAccessibleDescription(
        /^A key of up to 64 letters/,
      ),
    );
  }, 15_000);
});

describe("a project's deployment sharing", () => {
  it("lets its project admin share its deployments with their owners only", { timeout: 20_000 }, async () => {
    asProjectAdmin();
    const user = userEvent.setup();
    renderApp("/admin/projects/prj_web");
    const sharing = await screen.findByRole("region", { name: "Deployment sharing" });
    const save = within(sharing).getByRole("button", { name: "Save sharing" });
    expect(save).toBeDisabled();
    await user.click(within(sharing).getByRole("combobox", { name: "Who sees them" }));
    await user.click(await screen.findByRole("option", { name: "Owners only" }));
    expect(sharing).toHaveTextContent(
      "Project admins, supervisors, auditors and administrators still see all of them.",
    );
    await user.click(save);
    expect(await screen.findByText("Web Platform's deployments: owners only")).toBeInTheDocument();
    expect(db.projects.find((p) => p.id === "prj_web")?.deployment_sharing).toBe("owner_only");
    // Saved, the panel starts again from the project it read back, and the focus stays on the choice.
    await waitFor(() =>
      expect(
        within(screen.getByRole("region", { name: "Deployment sharing" })).getByRole("button", {
          name: "Save sharing",
        }),
      ).toBeDisabled(),
    );
    const choice = within(screen.getByRole("region", { name: "Deployment sharing" })).getByRole("combobox", {
      name: "Who sees them",
    });
    expect(choice).toHaveTextContent("Owners only");
    expect(choice).toHaveFocus();
  });
});
