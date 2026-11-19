import { screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { afterEach, describe, expect, it } from "vitest";
import type { components } from "@/lib/api/schema";
import { limitState } from "@/mocks/limits";
import { db, resetDb } from "@/mocks/seed";
import { renderApp } from "@/test/render";

type Policy = components["schemas"]["DeploymentLimitPolicy"];

afterEach(() => localStorage.removeItem("rvav.mock.as"));

const policy = (p: Partial<Policy> & Pick<Policy, "id" | "name">): Policy => ({
  project_id: null,
  environment: null,
  priority: 100,
  enabled: true,
  enforcement: "hard",
  deployments_per_user: null,
  deployments_per_project: null,
  vcpu_per_deployment: null,
  memory_gb_per_deployment: null,
  set_by_admin: true,
  ...p,
});

describe("deployment limits", () => {
  it("asks for a limit, then saves, turns off and removes a global policy", async () => {
    const user = userEvent.setup();
    renderApp("/admin/policies?tab=limits");
    expect(await screen.findByRole("tab", { name: "Limits", selected: true })).toBeInTheDocument();
    const region = await screen.findByRole("region", { name: "Deployment limits" });
    await user.click(await within(region).findByRole("button", { name: "New deployment limit policy" }));
    const sheet = await screen.findByRole("dialog", { name: "New deployment limit policy" });
    await user.type(within(sheet).getByRole("textbox", { name: "Name" }), "Small deployments");
    await user.click(within(sheet).getByRole("button", { name: "Save policy" }));
    expect(
      await within(sheet).findByText(
        "Set at least one limit: deployments per user or per project, or vCPU or memory per deployment.",
      ),
    ).toBeInTheDocument();

    await user.type(within(sheet).getByRole("spinbutton", { name: "vCPU per deployment" }), "8");
    await user.type(within(sheet).getByRole("spinbutton", { name: "Deployments per user" }), "5");
    await user.click(within(sheet).getByRole("button", { name: "Save policy" }));
    expect(await screen.findByText("Small deployments saved")).toBeInTheDocument();
    const row = await within(region).findByRole("row", { name: /Small deployments/ });
    await waitFor(() =>
      expect(within(region).getByRole("button", { name: "New deployment limit policy" })).toHaveFocus(),
    );
    expect(row).toHaveTextContent("Deployments per user: 5");
    expect(row).toHaveTextContent("vCPU per deployment: 8");
    expect(row).toHaveTextContent("Hard: refuses");
    expect(limitState(db).policies).toEqual([
      expect.objectContaining({ deployments_per_user: 5, vcpu_per_deployment: 8, memory_gb_per_deployment: null }),
    ]);

    await user.click(within(row).getByRole("button", { name: "Actions for Small deployments" }));
    await user.click(await screen.findByRole("menuitem", { name: "Turn off" }));
    expect(await screen.findByText("Small deployments turned off")).toBeInTheDocument();
    await waitFor(() =>
      expect(within(region).getByRole("row", { name: /Small deployments/ })).toHaveTextContent("Off"),
    );

    await user.click(within(region).getByRole("button", { name: "Actions for Small deployments" }));
    await user.click(await screen.findByRole("menuitem", { name: "Remove" }));
    const confirm = await screen.findByRole("alertdialog", { name: "Remove Small deployments?" });
    expect(confirm).toHaveTextContent("It stops limiting deployments per user and vCPU per deployment.");
    await user.click(within(confirm).getByRole("button", { name: "Remove policy" }));
    expect(await within(region).findByText("No deployment limit")).toBeInTheDocument();
  });

  it("lets a project admin write their project's own, and leave an administrator's alone", async () => {
    localStorage.setItem("rvav.mock.as", "project-admin");
    resetDb();
    limitState(db).policies.push(
      policy({ id: "dlp_web", name: "Web sizes", project_id: "prj_web", memory_gb_per_deployment: 32 }),
    );
    const user = userEvent.setup();
    renderApp("/admin/policies?tab=limits");
    const region = await screen.findByRole("region", { name: "Deployment limits" });
    const set = await within(region).findByRole("row", { name: /Web sizes/ });
    expect(set).toHaveTextContent("Set by an administrator");
    expect(set).toHaveTextContent("Memory per deployment: 32 GB");
    expect(within(set).queryByRole("button", { name: /^Actions for/ })).toBeNull();

    await user.click(within(region).getByRole("button", { name: "New deployment limit policy" }));
    const sheet = await screen.findByRole("dialog", { name: "New deployment limit policy" });
    await user.type(within(sheet).getByRole("textbox", { name: "Name" }), "Web count");
    await user.click(within(sheet).getByRole("combobox", { name: "Project" }));
    expect((await screen.findAllByRole("option")).map((o) => o.textContent)).toEqual(["Web Platform"]);
    await user.keyboard("{Escape}");
    await user.click(within(sheet).getByRole("combobox", { name: "Enforcement" }));
    await user.click(await screen.findByRole("option", { name: "Soft: it warns, and the requester may go on" }));
    await user.type(within(sheet).getByRole("spinbutton", { name: "Deployments per project" }), "20");
    await user.click(within(sheet).getByRole("button", { name: "Save policy" }));
    expect(await screen.findByText("Web count saved")).toBeInTheDocument();
    const mine = await within(region).findByRole("row", { name: /Web count/ });
    expect(mine).toHaveTextContent("Soft: warns");
    expect(mine).not.toHaveTextContent("Set by an administrator");
    expect(within(mine).getByRole("button", { name: "Actions for Web count" })).toBeInTheDocument();
    expect(limitState(db).policies.find((p) => p.name === "Web count")).toMatchObject({
      project_id: "prj_web",
      enforcement: "soft",
      deployments_per_project: 20,
      set_by_admin: false,
    });
  });

  it("says in What applies here which policy decides each limit", async () => {
    limitState(db).policies.push(
      policy({ id: "dlp_all", name: "rvav limits", vcpu_per_deployment: 2, deployments_per_project: 100 }),
      policy({ id: "dlp_web", name: "Web limits", project_id: "prj_web", priority: 10, vcpu_per_deployment: 8 }),
      policy({ id: "dlp_off", name: "Old limits", enabled: false, deployments_per_user: 1 }),
    );
    const user = userEvent.setup();
    renderApp("/admin/policies?tab=explain");
    await user.click(await screen.findByRole("button", { name: "Explain" }));
    const panel = await screen.findByRole("region", { name: "Deployment limits" });
    expect(panel).toHaveTextContent(/Deployments per project: \d+ of 100, by rvav limits/);
    expect(panel).toHaveTextContent("vCPU per deployment: at most 2, by rvav limits");
    expect(panel).toHaveTextContent(
      "Web limits: Its vCPU per deployment gives way to the global rvav limits, hard and stricter.",
    );
    expect(panel).toHaveTextContent("Old limits: Disabled.");
  });
});
