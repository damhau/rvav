import { screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { afterEach, beforeEach, describe, expect, it } from "vitest";
import { adminState } from "@/mocks/administration";
import { server } from "@/mocks/node";
import { customRoles } from "@/mocks/roles";
import { db, resetDb } from "@/mocks/seed";
import { renderApp } from "@/test/render";

// Léa, a project admin of Web Platform, is given a role holding a page's
// own permissions for all of rvav, and not project.settings.read, which
// listing projects with their settings takes (#126). QA is a project she
// doesn't see, Old Platform an archived one: their names come only from
// GET /admin/project-names.
function holding(...permissions: string[]) {
  localStorage.setItem("rvav.mock.as", "project-admin");
  resetDb();
  customRoles(db).push({ key: "holder", name: "Holder", description: "", permissions, built_in: false });
  db.me.permissions = [...db.me.permissions, ...permissions];
  db.me.roles = [...db.me.roles, { key: "holder", name: "Holder" }];
  const project = (id: string, code: string, name: string) =>
    db.projects.push({ id, code, name, description: "", cost_center: "", environments: ["dev"] });
  project("prj_qa", "qa", "QA");
  project("prj_old", "old", "Old Platform");
  const admin = adminState(db);
  admin.extra.prj_old!.archived_at = new Date().toISOString();
  return admin;
}

let refused: string[] = [];
const seen = ({ request, response }: { request: Request; response: Response }) => {
  const path = new URL(request.url).pathname;
  if (/\/admin\/project(s|-names)$/.test(path) && response.status === 403) refused.push(path);
};
beforeEach(() => {
  refused = [];
  server.events.on("response:mocked", seen);
});
afterEach(() => {
  server.events.removeListener("response:mocked", seen);
  localStorage.removeItem("rvav.mock.as");
});

/** The options a select offers, once it offers the one named. */
async function optionsOf(combobox: HTMLElement, once = "QA") {
  await userEvent.setup().click(combobox);
  await screen.findByRole("option", { name: once });
  return screen.getAllByRole("option").map((o) => o.textContent);
}

describe("Administration pages for who reads them alone", () => {
  it("Policies names a policy's project", async () => {
    holding("policies.read").approvals[0]!.project_id = "prj_qa";
    renderApp("/admin/policies");
    const region = await screen.findByRole("region", { name: "Approval policies" });
    expect(await within(region).findByText(/^QA, VM blueprints$/)).toBeInTheDocument();
    expect(within(region).queryByText(/prj_qa/)).toBeNull();
    expect(refused).toEqual([]);
  });

  it("Templates and networks names a template's project", async () => {
    holding("infrastructure.read").templates[1]!.offers = [{ project_id: "prj_qa", environment: null }];
    renderApp("/admin/infrastructure");
    const region = await screen.findByRole("region", { name: "VM templates" });
    const row = (await within(region).findByText("Windows Server 2025")).closest("tr")!;
    expect(await within(row).findByText("QA")).toBeInTheDocument();
    expect(within(row).queryByText(/prj_qa/)).toBeNull();
    expect(refused).toEqual([]);
  });

  it("Custom workflows shows its list", async () => {
    holding("extensibility.read");
    renderApp("/admin/workflows");
    const region = await screen.findByRole("region", { name: "Workflows" });
    expect(await within(region).findAllByRole("row")).not.toHaveLength(0);
    expect(screen.queryByRole("alert")).toBeNull();
  });
});

describe("project pickers for who holds a page's permissions alone", () => {
  const every = ["Web Platform", "Data Engineering", "QA"];

  it("Templates and networks offers a template to any project", async () => {
    holding("infrastructure.read", "infrastructure.manage");
    renderApp("/admin/infrastructure");
    const region = await screen.findByRole("region", { name: "VM templates" });
    await userEvent.setup().click(await within(region).findByRole("button", { name: "New template" }));
    const dialog = await screen.findByRole("dialog", { name: "New VM template" });
    const options = await optionsOf(within(dialog).getByRole("combobox", { name: "Offer 1: project" }));
    expect(options).toEqual(["All projects", ...every]);
    expect(refused).toEqual([]);
  });

  it("Policies applies a policy in any project", async () => {
    holding("policies.read", "policies.manage");
    renderApp("/admin/policies");
    const region = await screen.findByRole("region", { name: "Approval policies" });
    await userEvent.setup().click(await within(region).findByRole("button", { name: "New approval policy" }));
    const dialog = await screen.findByRole("dialog", { name: "New approval policy" });
    expect(await optionsOf(within(dialog).getByRole("combobox", { name: "Project" }))).toEqual([
      "Every project",
      ...every,
    ]);
    expect(refused).toEqual([]);
  });

  it("Custom workflows offers a workflow to any project", async () => {
    holding("extensibility.read", "extensibility.manage");
    renderApp("/admin/workflows");
    await userEvent.setup().click(await screen.findByRole("button", { name: "Add a workflow" }));
    const dialog = await screen.findByRole("dialog", { name: "Add a workflow" });
    for (const name of every) expect(await within(dialog).findByRole("checkbox", { name })).toBeInTheDocument();
    expect(within(dialog).queryByRole("checkbox", { name: "Old Platform" })).toBeNull();
    expect(refused).toEqual([]);
  });

  it("Identity provider groups map a group in any project", async () => {
    holding("access.read", "access.manage");
    renderApp("/admin/projects?tab=groups");
    const region = await screen.findByRole("region", { name: "Identity provider groups" });
    await userEvent.setup().click(await within(region).findByRole("button", { name: "Map a group" }));
    const sheet = await screen.findByRole("dialog", { name: "Map a group" });
    expect(await optionsOf(within(sheet).getByRole("combobox", { name: "In project" }))).toEqual([
      "For all of rvav",
      ...every,
    ]);
    expect(refused).toEqual([]);
  });

  it("Onboarding adopts VMs into any project", async () => {
    holding("import.manage");
    const user = userEvent.setup();
    renderApp("/admin/onboarding");
    await user.click(await screen.findByRole("checkbox", { name: "Select all shown" }));
    await user.click(screen.getByRole("button", { name: /^Adopt \d+ VMs…$/ }));
    const dialog = await screen.findByRole("dialog", { name: /^Adopt \d+ VMs$/ });
    expect(await optionsOf(within(dialog).getByRole("combobox", { name: "Project" }))).toEqual(every);
    expect(refused).toEqual([]);
  });

  it("vRA import maps vRA's projects to any project, and by name", async () => {
    holding("import.manage");
    const user = userEvent.setup();
    renderApp("/admin/vra-import");
    await user.click(await screen.findByRole("tab", { name: "Projects" }));
    const table = await screen.findByRole("table", { name: "vRA projects" });
    const web = within(table).getByRole("combobox", { name: "rvav project for Web Platform" });
    await waitFor(() => expect(web).toHaveTextContent("By name: Web Platform"));
    expect(await optionsOf(within(table).getByRole("combobox", { name: "rvav project for Legacy Apps" }))).toEqual([
      "By name: none, so it stays in vRA",
      "Stays in vRA",
      ...every,
    ]);
    expect(screen.queryByText(/projects couldn't be loaded/)).toBeNull();
    expect(refused).toEqual([]);
  });

  it("Audit log filters by any project, archived ones too", async () => {
    holding("audit.read");
    renderApp("/admin/audit");
    const filter = await screen.findByRole("combobox", { name: "Project" });
    expect(await optionsOf(filter, "Old Platform (archived)")).toEqual([
      "All projects",
      ...every,
      "Old Platform (archived)",
    ]);
    expect(refused).toEqual([]);
  });
});
