import { screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { afterEach, describe, expect, it } from "vitest";
import { audited } from "@/mocks/governance";
import { customRoles } from "@/mocks/roles";
import { db, resetDb } from "@/mocks/seed";
import { renderApp } from "@/test/render";

afterEach(() => localStorage.removeItem("rvav.mock.fresh"));

describe("Audit log", () => {
  it("filters from its toolbar, says when nothing matches, and clears the filters", async () => {
    const user = userEvent.setup();
    renderApp("/admin/audit");
    const table = await screen.findByRole("table", { name: "Audit events" });
    expect(await within(table).findByText("catalog.version.publish")).toBeInTheDocument();
    expect(screen.queryByRole("button", { name: "Clear filters" })).toBeNull();

    await user.click(screen.getByRole("combobox", { name: "What" }));
    await user.click(await screen.findByRole("option", { name: "API tokens" }));
    await waitFor(() => expect(within(table).queryByText("catalog.version.publish")).toBeNull());
    expect(within(table).getByText("token.create")).toBeInTheDocument();

    await user.type(screen.getByRole("searchbox", { name: "Search" }), "nobody-did-this");
    expect(await screen.findByText("No event matches these filters.")).toBeInTheDocument();
    const clear = screen.getAllByRole("button", { name: "Clear filters" });
    await user.click(clear[clear.length - 1]!);
    expect(await screen.findByText("catalog.version.publish")).toBeInTheDocument();
    expect(screen.getByRole("searchbox", { name: "Search" })).toHaveValue("");
    expect(screen.getByRole("searchbox", { name: "Search" })).toHaveFocus();
    expect(screen.queryByRole("button", { name: "Clear filters" })).toBeNull();
  });

  it("opens filtered from its address, and drops a day it can't read", async () => {
    const { router } = renderApp("/admin/audit?kind=token.&from=yesterday&to=2026-13-45");
    const table = await screen.findByRole("table", { name: "Audit events" });
    expect(await within(table).findByText("token.create")).toBeInTheDocument();
    expect(within(table).queryByText("catalog.version.publish")).toBeNull();
    expect(screen.getByRole("combobox", { name: "What" })).toHaveTextContent("API tokens");
    expect(screen.getByLabelText("From")).toHaveValue("");
    expect(router.state.location.search).toEqual({ kind: "token." });
  });

  it("shows failed sign-ins only, from where, by nobody known", async () => {
    const user = userEvent.setup();
    renderApp("/admin/audit");
    const table = await screen.findByRole("table", { name: "Audit events" });
    expect(await within(table).findByText("auth.sign_in")).toBeInTheDocument();
    await user.click(screen.getByRole("combobox", { name: "What" }));
    await user.click(await screen.findByRole("option", { name: "Failed sign-ins" }));
    await waitFor(() => expect(within(table).queryByText("auth.sign_in")).toBeNull());
    const row = within(table).getByText("auth.sign_in_failed").closest("tr")!;
    expect(row).toHaveTextContent("Unknown");
    expect(row).toHaveTextContent("from 10.20.4.17");
    expect(within(table).queryByText("catalog.version.publish")).toBeNull();
  });

  it("opens an event in a side panel with its detail", async () => {
    const user = userEvent.setup();
    renderApp("/admin/audit");
    const table = await screen.findByRole("table", { name: "Audit events" });
    const row = (await within(table).findByText("member.set")).closest("tr")!;
    expect(row).toHaveTextContent("2 details");
    await user.click(within(row).getByRole("button", { name: /^Open the event from/ }));
    const sheet = await screen.findByRole("dialog", { name: "member.set" });
    expect(sheet).toHaveTextContent("Léa Martin");
    expect(sheet).toHaveTextContent("Web Platform");
    expect(sheet).toHaveTextContent("usr_tnguyen");
  });

  it("names what each event was on, opens what rvav still keeps, and says what it doesn't", async () => {
    const user = userEvent.setup();
    resetDb();
    const roles = customRoles(db);
    roles.push({
      key: "vm-operator",
      name: "VM operator",
      description: "",
      permissions: ["project.see"],
      built_in: false,
    });
    audited(db, "role.create", "role", "vm-operator");
    roles.splice(0, roles.length);
    audited(db, "role.delete", "role", "vm-operator", {}, "VM operator");
    renderApp("/admin/audit");
    const table = await screen.findByRole("table", { name: "Audit events" });
    const row = async (action: string) => (await within(table).findByText(action)).closest("tr")!;

    const published = await row("catalog.version.publish");
    expect(within(published).getByRole("link", { name: "RHEL 9 server" })).toHaveAttribute(
      "href",
      "/admin/catalog-items/itm_rhel9",
    );
    expect(published).not.toHaveTextContent("itm_rhel9");
    const created = await row("role.create");
    expect(created).toHaveTextContent("role VM operator (no longer exists)");
    expect(within(created).queryByRole("link")).toBeNull();
    expect(await row("role.delete")).toHaveTextContent("role VM operator (no longer exists)");
    expect(await row("token.create")).toHaveTextContent("API token GitLab pipeline");
    expect(await row("approval.timeout")).toHaveTextContent("approval REQ-10477 · Infrastructure leads");

    await user.click(within(published).getByRole("button", { name: /^Open the event from/ }));
    const sheet = await screen.findByRole("dialog", { name: "catalog.version.publish" });
    expect(sheet).toHaveTextContent("catalog item RHEL 9 server");
    expect(sheet).toHaveTextContent("itm_rhel9");
  });

  it("opens a deployment or a request from its event only for who sees all of its project's", async () => {
    resetDb();
    const dep = db.deployments.find((d) => d.project.id === "prj_web")!;
    const req = db.requests.find((r) => r.request.project.id === "prj_web")!.request;
    audited(db, "action.run", "deployment", dep.id);
    audited(db, "request.cancel", "request", req.id);
    const { unmount } = renderApp("/admin/audit");
    const table = await screen.findByRole("table", { name: "Audit events" });
    expect(await within(table).findByRole("link", { name: dep.name })).toHaveAttribute(
      "href",
      `/deployments/${dep.id}`,
    );
    expect(within(table).getByRole("link", { name: req.number })).toHaveAttribute("href", `/requests/${req.id}`);
    unmount();

    const readsAll = (ps: string[]) => ps.filter((p) => p !== "deployments.read_all");
    db.me = {
      ...db.me,
      permissions: readsAll(db.me.permissions),
      projects: db.me.projects.map((p) => ({ ...p, permissions: readsAll(p.permissions) })),
    };
    renderApp("/admin/audit");
    const refused = await screen.findByRole("table", { name: "Audit events" });
    const ran = (await within(refused).findByText("action.run")).closest("tr")!;
    expect(ran).toHaveTextContent(`deployment ${dep.name}`);
    expect(within(ran).queryByRole("link")).toBeNull();
    const cancelled = within(refused).getByText("request.cancel").closest("tr")!;
    expect(cancelled).toHaveTextContent(`request ${req.number}`);
    expect(within(cancelled).queryByRole("link")).toBeNull();
  });

  it("says nothing is recorded on a fresh install", async () => {
    localStorage.setItem("rvav.mock.fresh", "1");
    resetDb();
    renderApp("/admin/audit");
    expect(await screen.findByText("Nothing recorded yet")).toBeInTheDocument();
  });
});
