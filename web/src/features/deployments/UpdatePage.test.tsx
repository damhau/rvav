import { screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { describe, expect, it } from "vitest";
import { api, call } from "@/lib/api/client";
import type { DeploymentDetail, RequestForm, UiField } from "@/lib/api/types";
import { adminState } from "@/mocks/administration";
import { limitState } from "@/mocks/limits";
import { db } from "@/mocks/seed";
import { renderApp } from "@/test/render";
import { startValues } from "./UpdatePage";

const named = (name: string) => db.deployments.find((d) => d.name === name)!;

describe("the update page against the mock API", () => {
  it("opens from the Actions menu, previews a change and applies it after asking", async () => {
    const user = userEvent.setup();
    const { router } = renderApp(`/deployments/${named("web-tst-search").id}`);
    await user.click(await screen.findByRole("button", { name: "Actions" }, { timeout: 5000 }));
    await user.click(await screen.findByRole("menuitem", { name: "Update" }));
    expect(await screen.findByRole("heading", { level: 1, name: "Update web-tst-search" })).toBeVisible();

    const summary = await screen.findByRole("region", { name: "What the update does" }, { timeout: 5000 });
    expect(await within(summary).findByText("Change an input to see what the update does.")).toBeVisible();
    expect(within(summary).getByRole("button", { name: "Review the update" })).toBeDisabled();

    await user.click(await screen.findByRole("radio", { name: /^X-Large/ }));
    const changes = await within(summary).findByRole("list", { name: "Changes" }, { timeout: 5000 });
    expect(
      within(changes)
        .getAllByRole("listitem")
        .map((li) => li.firstChild?.textContent),
    ).toEqual(["Set 8 vCPU and 48 GB on web-tst-search001", "Set 8 vCPU and 48 GB on web-tst-search002"]);
    expect(summary).toHaveTextContent("No approval needed");
    expect(summary).toHaveTextContent("Quota after this update");

    await user.click(within(summary).getByRole("button", { name: "Review the update" }));
    const dialog = await screen.findByRole("alertdialog", { name: "Update web-tst-search?" });
    expect(dialog).toHaveTextContent("rvav makes these 2 changes now.");
    await user.click(within(dialog).getByRole("button", { name: "Update" }));
    await waitFor(() => expect(router.state.location.pathname).toMatch(/^\/requests\/REQ-/));
    expect(await screen.findByRole("heading", { level: 1, name: "Update · web-tst-search" })).toBeVisible();
  }, 20_000);

  it("asks again, with the focus on the warning, when a soft policy changed since Review", async () => {
    const policy = {
      id: "d2p_update_soft",
      set_by_admin: true,
      name: "Updates in office hours",
      reason: "Tell the team before you update.",
      priority: 1,
      actions: ["update"],
      roles: [],
      condition: "true",
      effect: "deny" as const,
      enforcement: "soft" as const,
      levels: [],
      project_id: null,
      enabled: true,
    };
    adminState(db).day2.push(policy);
    const user = userEvent.setup();
    const { router } = renderApp(`/deployments/${named("web-tst-search").id}/update`);
    const summary = await screen.findByRole("region", { name: "What the update does" }, { timeout: 5000 });
    await user.click(await screen.findByRole("radio", { name: /^X-Large/ }));
    await within(summary).findByRole("list", { name: "Changes" }, { timeout: 5000 });
    await user.click(within(summary).getByRole("button", { name: "Review the update" }));
    const dialog = await screen.findByRole("alertdialog", { name: "Update web-tst-search?" });
    expect(within(dialog).getByRole("note", { name: "Policy warning" })).toHaveAccessibleDescription(
      "Tell the team before you update. (Updates in office hours)",
    );

    policy.reason = "Ask the change board first.";
    await user.click(within(dialog).getByRole("button", { name: "Update anyway" }));
    const note = await within(dialog).findByRole("note", { name: "Policy warning" });
    await waitFor(() =>
      expect(note).toHaveAccessibleDescription("Ask the change board first. (Updates in office hours)"),
    );
    await waitFor(() => expect(note).toHaveFocus());
    await user.click(within(dialog).getByRole("button", { name: "Update anyway" }));
    await waitFor(() => expect(router.state.location.pathname).toMatch(/^\/requests\/REQ-/));
    expect(db.requests[0]!.request.warnings).toEqual([
      expect.objectContaining({ message: "Ask the change board first." }),
    ]);
  }, 20_000);

  it("repairs a failed deployment with its own inputs: its jobs run again", async () => {
    renderApp(`/deployments/${named("web-dev-worker").id}/update`);
    const summary = await screen.findByRole("region", { name: "What the update does" }, { timeout: 5000 });
    const changes = await within(summary).findByRole("list", { name: "Changes" }, { timeout: 5000 });
    expect(
      within(changes)
        .getAllByRole("listitem")
        .map((li) => li.firstChild?.textContent),
    ).toEqual(["Run the AWX job linux-baseline", "Run the AWX job idm-join"]);
    expect(within(summary).getByRole("button", { name: "Review the update" })).toBeEnabled();
  }, 15_000);

  it("refuses a change it can't make in place, saying why", async () => {
    const user = userEvent.setup();
    renderApp(`/deployments/${named("web-dev-api").id}/update`);
    const summary = await screen.findByRole("region", { name: "What the update does" }, { timeout: 5000 });
    await user.click(await screen.findByRole("radio", { name: /^Large/ }));
    const refused = await within(summary).findByRole("list", { name: "Refused" }, { timeout: 5000 });
    expect(refused).toHaveTextContent(
      "web-dev-api004 has snapshots, and vCenter can't grow the disks of a VM with snapshots. Delete them first.",
    );
    expect(within(summary).getByRole("button", { name: "Review the update" })).toBeDisabled();
  }, 15_000);

  it("moves to the newest version the item offers, or keeps the deployment's while it is still offered", async () => {
    const d = named("web-tst-search");
    const item = db.items.find((i) => i.id === d.catalog_item.id)!;
    const newer = await call(
      api.POST("/admin/catalog/items/{itemId}/versions", { params: { path: { itemId: item.id } }, body: {} }),
    );
    await call(
      api.POST("/admin/catalog/items/{itemId}/versions/{versionId}/publish", {
        params: { path: { itemId: item.id, versionId: newer.id } },
        body: { keep_published: true },
      }),
    );
    const user = userEvent.setup();
    renderApp(`/deployments/${d.id}/update`);
    const summary = await screen.findByRole("region", { name: "What the update does" }, { timeout: 5000 });
    const version = await screen.findByRole("combobox", { name: "Version" }, { timeout: 5000 });
    expect(version).toHaveTextContent(`${newer.number} (newest)`);
    expect(version).toHaveAccessibleDescription("Its inputs move to this version.");
    await within(summary).findByText(`${d.version.number} → ${newer.number}`, {}, { timeout: 5000 });

    await user.click(version);
    await user.click(await screen.findByRole("option", { name: `${d.version.number} (this deployment's)` }));
    const own = await screen.findByRole("combobox", { name: "Version" });
    await waitFor(() => expect(own).toHaveAccessibleDescription("Its own version: the update changes its inputs."));
    // The form starts again on the version picked: a new summary.
    const again = await screen.findByRole("region", { name: "What the update does" });
    await within(again).findByText(`${d.version.number}, unchanged`, {}, { timeout: 5000 });
    expect(again).toHaveTextContent("Change an input to see what the update does.");
  }, 20_000);

  it("keeps the inputs changed, and the focus on Version, when another version is picked", async () => {
    const d = named("web-tst-search");
    const item = db.items.find((i) => i.id === d.catalog_item.id)!;
    const newer = await call(
      api.POST("/admin/catalog/items/{itemId}/versions", { params: { path: { itemId: item.id } }, body: {} }),
    );
    await call(
      api.POST("/admin/catalog/items/{itemId}/versions/{versionId}/publish", {
        params: { path: { itemId: item.id, versionId: newer.id } },
        body: { keep_published: true },
      }),
    );
    const user = userEvent.setup();
    renderApp(`/deployments/${d.id}/update`);
    const version = await screen.findByRole("combobox", { name: "Version" }, { timeout: 5000 });
    await user.click(await screen.findByRole("radio", { name: /^X-Large/ }));
    await user.click(version);
    await user.click(await screen.findByRole("option", { name: `${d.version.number} (this deployment's)` }));

    const own = await screen.findByRole("combobox", { name: "Version" });
    await waitFor(() => expect(own).toHaveAccessibleDescription("Its own version: the update changes its inputs."));
    await waitFor(() => expect(own).toHaveFocus());
    expect(screen.getByRole("radio", { name: /^X-Large/ })).toBeChecked();
    const summary = screen.getByRole("region", { name: "What the update does" });
    await within(summary).findByText("Set 8 vCPU and 48 GB on web-tst-search001", {}, { timeout: 5000 });
  }, 20_000);

  it("moves to a version still offered when the one picked was retired, saying why on Version", async () => {
    const d = named("web-tst-search");
    const item = db.items.find((i) => i.id === d.catalog_item.id)!;
    const publish = async () => {
      const v = await call(
        api.POST("/admin/catalog/items/{itemId}/versions", { params: { path: { itemId: item.id } }, body: {} }),
      );
      await call(
        api.POST("/admin/catalog/items/{itemId}/versions/{versionId}/publish", {
          params: { path: { itemId: item.id, versionId: v.id } },
          body: { keep_published: true },
        }),
      );
      return v;
    };
    const retired = await publish();
    const newest = await publish();
    const user = userEvent.setup();
    renderApp(`/deployments/${d.id}/update`);
    await user.click(await screen.findByRole("combobox", { name: "Version" }, { timeout: 5000 }));
    await user.click(await screen.findByRole("option", { name: retired.number }));
    await waitFor(() => expect(screen.getByRole("combobox", { name: "Version" })).toHaveTextContent(retired.number));
    await user.click(await screen.findByRole("radio", { name: /^X-Large/ }));
    await call(
      api.POST("/admin/catalog/items/{itemId}/versions/{versionId}/deprecate", {
        params: { path: { itemId: item.id, versionId: retired.id } },
        body: {},
      }),
    );

    const summary = screen.getByRole("region", { name: "What the update does" });
    await user.click(within(summary).getByRole("button", { name: "Review the update" }));
    const version = () => screen.getByRole("combobox", { name: "Version" });
    await waitFor(() => expect(version()).toHaveTextContent(`${newest.number} (newest)`), { timeout: 5000 });
    expect(version()).toHaveAccessibleDescription(
      `Version ${retired.number} of ${item.name} is no longer offered: request ${newest.number}.`,
    );
    await waitFor(() => expect(version()).toHaveFocus());
    expect(screen.getByRole("radio", { name: /^X-Large/ })).toBeChecked();
    expect(screen.queryByRole("alertdialog")).not.toBeInTheDocument();
  }, 20_000);

  it("holds a deployment to the size limits only where the update grows it", async () => {
    limitState(db).policies.push({
      id: "dlp_size",
      name: "Small deployments",
      project_id: null,
      environment: null,
      priority: 100,
      enabled: true,
      enforcement: "hard",
      deployments_per_user: 3,
      deployments_per_project: null,
      vcpu_per_deployment: 10,
      memory_gb_per_deployment: 80,
      set_by_admin: true,
    });
    const user = userEvent.setup();
    renderApp(`/deployments/${named("web-tst-search").id}/update`);
    const summary = await screen.findByRole("region", { name: "What the update does" }, { timeout: 5000 });
    await user.click(await screen.findByRole("radio", { name: /^X-Large/ }));
    await waitFor(() => expect(summary).toHaveTextContent("Memory per deployment96 / 80 GB"), { timeout: 5000 });
    expect(summary).toHaveTextContent("vCPU per deployment16 / 10");
    expect(summary).not.toHaveTextContent("Deployments per user");
    await user.click(within(summary).getByRole("button", { name: "Review the update" }));
    expect(
      await screen.findByText(
        "Deployments in Web Platform may have at most 80 GB of memory under Small deployments, and this one would have 96. Choose a smaller size, or ask an administrator to raise the limit.",
      ),
    ).toBeInTheDocument();
    expect(screen.queryByText(/at most 10 vCPU/)).toBeNull();
    expect(screen.queryByRole("alertdialog")).toBeNull();
  }, 15_000);

  it("refuses nothing on a deployment adopted without inputs, before any edit", async () => {
    const item = db.items.find((i) => i.slug === "rhel9-server")!;
    const adopted = await fetch("http://localhost/api/v1/admin/onboarding", {
      method: "POST",
      headers: { Authorization: "Bearer test", "Content-Type": "application/json" },
      body: JSON.stringify({
        vms: [{ vcenter: "vc01.corp.example", moref: "vm-9152" }],
        project_id: "prj_web",
        environment: "dev",
        owner_id: db.me.user.id,
        catalog_item_id: item.id,
      }),
    });
    expect(adopted.ok).toBe(true);
    renderApp(`/deployments/${named("app-old01").id}/update`);
    const summary = await screen.findByRole("region", { name: "What the update does" }, { timeout: 5000 });
    expect(
      await within(summary).findByText(
        "Nothing changes on the VMs: the deployment keeps the new inputs.",
        {},
        { timeout: 5000 },
      ),
    ).toBeVisible();
    expect(within(summary).queryByRole("list", { name: "Refused" })).not.toBeInTheDocument();
    expect(within(summary).getByRole("button", { name: "Review the update" })).toBeEnabled();
  }, 15_000);
});

describe("the update page with a wizard form", () => {
  const asWizard = (name: string) => {
    const form = db.forms[named(name).catalog_item.id]!;
    form.ui_schema.layout.kind = "wizard";
  };
  const toReview = async (user: ReturnType<typeof userEvent.setup>, steps: number) => {
    for (let i = 0; i < steps; i++) await user.click(await screen.findByRole("button", { name: "Next" }));
  };
  const wizardReview = () => {
    const summary = screen.getByRole("region", { name: "What the update does" });
    return screen
      .getAllByRole("button", { name: "Review the update" })
      .find((b) => !summary.contains(b)) as HTMLElement;
  };

  it("says why it stops at Review when nothing changes, instead of asking", async () => {
    asWizard("web-prd-front");
    const user = userEvent.setup();
    renderApp(`/deployments/${named("web-prd-front").id}/update`);
    const summary = await screen.findByRole("region", { name: "What the update does" }, { timeout: 5000 });
    await within(summary).findByText("Change an input to see what the update does.", {}, { timeout: 5000 });
    await toReview(user, 4);
    await user.click(wizardReview());
    const alert = await screen.findByRole("alert");
    expect(alert).toHaveTextContent(
      "web-prd-front already has this version and these inputs: change an input to update it.",
    );
    expect(alert).toHaveFocus();
    expect(screen.queryByRole("alertdialog")).not.toBeInTheDocument();
  }, 20_000);

  it("lists what it refuses at Review", async () => {
    asWizard("web-dev-api");
    const user = userEvent.setup();
    renderApp(`/deployments/${named("web-dev-api").id}/update`);
    const summary = await screen.findByRole("region", { name: "What the update does" }, { timeout: 5000 });
    await toReview(user, 1);
    await user.click(await screen.findByRole("radio", { name: /^Large/ }));
    await within(summary).findByRole("list", { name: "Refused" }, { timeout: 5000 });
    await toReview(user, 3);
    await user.click(wizardReview());
    const alert = await screen.findByRole("alert");
    expect(alert).toHaveTextContent("These changes can't be made in place.");
    expect(alert).toHaveTextContent("web-dev-api004 has snapshots");
    expect(alert).toHaveFocus();
    expect(screen.queryByRole("alertdialog")).not.toBeInTheDocument();
  }, 20_000);
});

describe("the values an update starts from", () => {
  const ui = (f: Partial<UiField>): UiField => ({ widget: "text", label: "x", ...f });
  const form = (version: string): RequestForm =>
    ({
      version_id: version,
      schema: { properties: { size: {}, owner: {}, token: {}, added: {} } },
      initial_values: { added: "new" },
      ui_schema: {
        layout: { kind: "page", sections: [] },
        fields: {
          size: ui({}),
          owner: ui({ rules: { default: "user.email" } }),
          token: ui({ secret: true }),
          added: ui({ rules: { default: "'x'" } }),
        },
      },
    }) as unknown as RequestForm;
  const d = {
    version: { id: "ver_1", number: "1" },
    inputs: { size: "M", token: "••••", gone: "old" },
  } as unknown as DeploymentDetail;

  it("keep a field the requester emptied empty on the same version, and leave out secrets", () => {
    expect(startValues(form("ver_1"), d)).toEqual({ size: "M", owner: null, added: null });
  });

  it("start a newer version's new fields as on a new request", () => {
    expect(startValues(form("ver_2"), d)).toEqual({ size: "M", added: "new" });
  });

  it("start a deployment adopted without inputs as a new request, emptying nothing", () => {
    expect(startValues(form("ver_1"), { ...d, inputs: {} })).toEqual({ added: "new" });
  });
});
