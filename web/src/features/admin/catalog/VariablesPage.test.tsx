import { screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { afterEach, describe, expect, it } from "vitest";
import { api, call } from "@/lib/api/client";
import { itemBlueprints } from "@/mocks/catalogAdmin";
import { db, resetDb } from "@/mocks/seed";
import { renderApp } from "@/test/render";

type User = ReturnType<typeof userEvent.setup>;

async function openMenu(user: User, ref: string) {
  await user.click(await screen.findByRole("button", { name: `Actions for ${ref}` }));
}

async function chooseType(user: User, type: string) {
  await user.click(within(screen.getByRole("dialog")).getByRole("combobox", { name: "Type" }));
  await user.click(await screen.findByRole("option", { name: type }));
}

afterEach(() => {
  localStorage.removeItem("rvav.mock.as");
  resetDb();
});

describe("Variables", () => {
  it("lists each variable with its value, its type and what reads it", async () => {
    renderApp("/admin/variables");
    expect(await screen.findByRole("heading", { name: "Variables", level: 1 })).toBeInTheDocument();
    const table = await screen.findByRole("table", { name: "Variables" });
    const dns = await within(table).findByRole("row", { name: /vars\.network\.dns_servers/ });
    expect(dns).toHaveTextContent("10.20.0.53, 10.20.0.54");
    expect(dns).toHaveTextContent("List");
    expect(dns).toHaveTextContent("The resolvers every VM is given.");
    expect(dns).toHaveTextContent("Nothing");
    expect(dns).toHaveTextContent("by Marc Keller");
    expect(within(table).getByRole("row", { name: /vars\.cmdb\.site/ })).toHaveTextContent("Text");
  });

  it("adds a list, then changes it, and says when the JSON can't be read", { timeout: 15_000 }, async () => {
    const user = userEvent.setup();
    renderApp("/admin/variables");
    await user.click(await screen.findByRole("button", { name: "New variable" }));
    const sheet = await screen.findByRole("dialog", { name: "New variable" });
    await user.type(within(sheet).getByLabelText("Group"), "monitoring");
    await user.type(within(sheet).getByLabelText("Name"), "targets");
    expect(sheet).toHaveTextContent("Steps and conditions read it as vars.monitoring.targets.");
    await chooseType(user, "List or object (JSON)");
    await user.click(within(sheet).getByLabelText("Value"));
    await user.paste('["web", "db"');
    await user.click(within(sheet).getByRole("button", { name: "Add" }));
    expect(within(sheet).getByLabelText("Value")).toHaveAccessibleDescription(/^Not JSON/);
    await user.paste("]");
    await user.click(within(sheet).getByRole("button", { name: "Add" }));
    expect(await screen.findByText("vars.monitoring.targets added")).toBeInTheDocument();
    const row = await screen.findByRole("row", { name: /vars\.monitoring\.targets/ });
    expect(row).toHaveTextContent("web, db");

    await openMenu(user, "vars.monitoring.targets");
    await user.click(await screen.findByRole("menuitem", { name: "Change the value" }));
    const change = await screen.findByRole("dialog", { name: "Change vars.monitoring.targets" });
    expect(within(change).getByLabelText("Group")).toBeDisabled();
    await chooseType(user, "Number");
    await user.clear(within(change).getByLabelText("Value"));
    await user.type(within(change).getByLabelText("Value"), "3");
    await user.click(within(change).getByRole("button", { name: "Save" }));
    expect(await screen.findByText("vars.monitoring.targets saved")).toBeInTheDocument();
    await waitFor(() =>
      expect(screen.getByRole("row", { name: /vars\.monitoring\.targets/ })).toHaveTextContent("3Number"),
    );
  });

  it("says next to the name what the server refused", async () => {
    const user = userEvent.setup();
    renderApp("/admin/variables");
    await user.click(await screen.findByRole("button", { name: "New variable" }));
    const sheet = await screen.findByRole("dialog", { name: "New variable" });
    await user.type(within(sheet).getByLabelText("Group"), "cmdb");
    await user.type(within(sheet).getByLabelText("Name"), "admin_password");
    await user.type(within(sheet).getByLabelText("Value"), "hunter2");
    await user.click(within(sheet).getByRole("button", { name: "Add" }));
    await waitFor(() =>
      expect(within(sheet).getByLabelText("Name")).toHaveAccessibleDescription(
        /store a password, a token or a key as a secret/,
      ),
    );
  });

  it("removes one nothing reads, and not one an item reads", { timeout: 15_000 }, async () => {
    const reader = itemBlueprints(db).find((v) => v.slug === "postgres-cluster" && v.state === "published")!;
    const steps = (reader.blueprint as { steps: Record<string, unknown>[] }).steps;
    steps.push({ key: "cmdb", awx: { job_template: "cmdb-register" }, with: { site: "vars.cmdb.site" } });
    const user = userEvent.setup();
    renderApp("/admin/variables");
    const site = await screen.findByRole("row", { name: /vars\.cmdb\.site/ });
    expect(site).toHaveTextContent("item postgres-cluster");
    await openMenu(user, "vars.cmdb.site");
    expect(await screen.findByRole("menuitem", { name: "Remove: in use" })).toHaveAttribute("aria-disabled", "true");
    await user.keyboard("{Escape}");

    await openMenu(user, "vars.backup.s3_bucket");
    await user.click(await screen.findByRole("menuitem", { name: "Remove" }));
    const confirm = await screen.findByRole("alertdialog", { name: "Remove vars.backup.s3_bucket?" });
    await user.click(within(confirm).getByRole("button", { name: "Remove variable" }));
    expect(await screen.findByText("vars.backup.s3_bucket removed")).toBeInTheDocument();
    await waitFor(() => expect(screen.queryByRole("row", { name: /vars\.backup\.s3_bucket/ })).toBeNull());
  });

  it("names a request of an older version still running as a reader, and not one that failed", async () => {
    const old = itemBlueprints(db).find((v) => v.slug === "postgres-cluster" && v.state === "published")!;
    (old.blueprint as { steps: Record<string, unknown>[] }).steps.push({
      key: "cmdb",
      awx: { job_template: "cmdb-register" },
      with: { site: "vars.cmdb.site" },
    });
    const item = db.items.find((i) => i.slug === "postgres-cluster")!;
    await call(
      api.POST("/admin/catalog/items/{itemId}/versions/{versionId}/deprecate", {
        params: { path: { itemId: item.id, versionId: old.id } },
        body: {},
      }),
    );
    const [running, failed] = db.requests;
    running!.request = { ...running!.request, status: "running", version: { ...running!.request.version, id: old.id } };
    failed!.request = { ...failed!.request, status: "failed", version: { ...failed!.request.version, id: old.id } };
    renderApp("/admin/variables");
    const site = await screen.findByRole("row", { name: /vars\.cmdb\.site/ });
    expect(site).toHaveTextContent(`request ${running!.request.number}`);
    expect(site).not.toHaveTextContent(`request ${failed!.request.number}`);
  });

  it("shows an auditor the variables and what reads them, without changing any", async () => {
    localStorage.setItem("rvav.mock.as", "auditor");
    resetDb();
    renderApp("/admin/variables");
    const table = await screen.findByRole("table", { name: "Variables" });
    expect(await within(table).findByRole("row", { name: /vars\.network\.dns_servers/ })).toHaveTextContent(
      "10.20.0.53, 10.20.0.54",
    );
    expect(screen.queryByRole("button", { name: "New variable" })).toBeNull();
    expect(screen.queryByRole("button", { name: /^Actions for/ })).toBeNull();
  });
});
