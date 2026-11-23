import { screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { http, HttpResponse } from "msw";
import { describe, expect, it } from "vitest";
import { server } from "@/mocks/node";
import { renderApp } from "@/test/render";

describe("Integrations", () => {
  it("lists the connections with their state, and opens one's page from its row", async () => {
    const user = userEvent.setup();
    const { router } = renderApp("/admin/integrations");
    const table = await screen.findByRole("table", { name: "Connections" });
    await within(table).findByRole("link", { name: "Identity provider" });
    expect(within(within(table).getAllByRole("row")[1]!).getByRole("link")).toHaveTextContent("Identity provider");
    expect(within(table).getByRole("row", { name: /^vCenter/ })).toHaveTextContent("Configured");
    expect(within(table).queryByRole("link", { name: "Console" })).toBeNull();

    await user.click(within(table).getByRole("row", { name: /^AWX/ }));
    expect(await screen.findByRole("heading", { level: 1, name: "AWX" })).toBeInTheDocument();
    expect(router.state.location.pathname).toBe("/admin/integrations/awx");
    expect(await screen.findByRole("region", { name: "AWX connection" })).toBeInTheDocument();
    expect(screen.getByRole("region", { name: "Inventory token" })).toBeInTheDocument();
  });

  it("keeps the test and the save in each form's footer", async () => {
    renderApp("/admin/integrations/vcenter");
    for (const name of ["vCenter connection", "Console"]) {
      const panel = await screen.findByRole("region", { name });
      const buttons = within(panel).getAllByRole("button").slice(-2);
      expect(buttons.map((b) => b.textContent)).toEqual([
        name === "Console" ? "Test console" : "Test connection",
        "Save settings",
      ]);
    }
  });

  it("opens an integration's page from the address it had as a tab", async () => {
    const { router } = renderApp("/admin/integrations?tab=console");
    expect(await screen.findByRole("heading", { level: 1, name: "vCenter" })).toBeInTheDocument();
    await waitFor(() => expect(router.state.location.pathname).toBe("/admin/integrations/vcenter"));
  });

  it("runs the EfficientIP reconcile check in its own tab", async () => {
    const user = userEvent.setup();
    const { router } = renderApp("/admin/integrations");
    await user.click(await screen.findByRole("tab", { name: "Checks" }));
    expect(router.state.location.search).toEqual({ tab: "checks" });
    const panel = await screen.findByRole("region", { name: "EfficientIP reconcile" });
    await user.click(within(panel).getByRole("button", { name: "Run the check" }));
    const orphans = await within(panel).findByRole("table", { name: "Addresses no deployment owns" });
    expect(orphans).toHaveTextContent("10.31.10.61");
    expect(within(panel).getByRole("button", { name: "Check again" })).toBeInTheDocument();
    expect(within(panel).getByRole("status")).toHaveTextContent("Checked: 6 to reconcile.");
    // rvav's IP ranges are reconciled where they are managed.
    expect(panel).toHaveTextContent(
      "rvav's IP ranges: 1 address of VMs rvav no longer has, 2 VMs on an address their range didn't give.",
    );
    await user.click(within(panel).getByRole("link", { name: "Reconcile them on the IP ranges tab" }));
    expect(await screen.findByRole("region", { name: "Reconcile the IP ranges" })).toBeInTheDocument();
  });

  it("shows only the error when the check fails again, not the report before it", async () => {
    const user = userEvent.setup();
    renderApp("/admin/integrations?tab=checks");
    const panel = await screen.findByRole("region", { name: "EfficientIP reconcile" });
    await user.click(within(panel).getByRole("button", { name: "Run the check" }));
    expect(await within(panel).findByRole("table", { name: "Addresses no deployment owns" })).toBeInTheDocument();

    server.use(
      http.get("*/api/v1/admin/ipam/reconcile", () =>
        HttpResponse.json(
          { type: "about:blank", title: "EfficientIP didn't answer", status: 502, request_id: "req_test" },
          { status: 502, headers: { "Content-Type": "application/problem+json" } },
        ),
      ),
    );
    await user.click(within(panel).getByRole("button", { name: "Check again" }));
    expect(await within(panel).findByRole("alert")).toHaveTextContent("EfficientIP didn't answer");
    expect(within(panel).queryByRole("table", { name: "Addresses no deployment owns" })).toBeNull();
    expect(within(panel).getByRole("status")).toBeEmptyDOMElement();
    expect(panel).not.toHaveTextContent("rvav's IP ranges:");
  });

  it("says when there is no such integration", async () => {
    renderApp("/admin/integrations/ldap");
    expect(await screen.findByText("No such integration")).toBeInTheDocument();
    expect(screen.getByRole("link", { name: "All integrations" })).toHaveAttribute("href", "/admin/integrations");
  });
});
