import { screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { afterEach, describe, expect, it } from "vitest";
import { db, resetDb } from "@/mocks/seed";
import { renderApp } from "@/test/render";

const edgeId = () => db.deployments.find((d) => d.name === "web-prd-edge")!.id;

afterEach(() => resetDb());

describe("a deployment's custom resources", () => {
  it("shows each with its type and its properties by their titles, and runs its type's actions", async () => {
    const user = userEvent.setup();
    renderApp(`/deployments/${edgeId()}`);
    const lb = await screen.findByRole("listitem", { name: "web-prd-edge-lb" }, { timeout: 5000 });
    expect(lb).toHaveTextContent("Load balancer");
    expect(lb).toHaveTextContent("lb");
    const facts = Object.fromEntries(
      within(lb)
        .getAllByRole("term")
        .map((t) => [t.textContent, t.nextElementSibling?.textContent]),
    );
    expect(facts).toEqual({
      "Virtual IP": "10.21.10.200",
      Pool: "pool-web-prd-edge",
      Port: "443",
      members: "web-prd-edge001, web-prd-edge002",
    });

    await user.click(within(lb).getByRole("button", { name: "More actions for web-prd-edge-lb" }));
    await user.click(await screen.findByRole("menuitem", { name: "Drain" }));
    expect(await screen.findByText("Drain · web-prd-edge-lb")).toBeInTheDocument();
    await waitFor(() =>
      expect(db.requests.find((r) => r.request.action?.key === "wf.lb-drain")?.request.resource).toMatchObject({
        name: "web-prd-edge-lb",
      }),
    );
  });

  it("says one is being created, without the properties it doesn't have yet", async () => {
    const edge = db.deployments.find((d) => d.name === "web-prd-edge")!;
    const lb = edge.resources.find((r) => r.type === "custom")!;
    Object.assign(lb, { creating: true, properties: {} });
    renderApp(`/deployments/${edge.id}`);
    const item = await screen.findByRole("listitem", { name: "web-prd-edge-lb" }, { timeout: 5000 });
    expect(item).toHaveTextContent("Being created");
    expect(item).toHaveTextContent("what it made isn't known yet");
    expect(within(item).queryAllByRole("term")).toHaveLength(0);
  });

  it("lists them apart from the VMs in the Actions menu", async () => {
    const user = userEvent.setup();
    renderApp(`/deployments/${edgeId()}`);
    await screen.findByRole("heading", { level: 1, name: "web-prd-edge" }, { timeout: 5000 });
    await user.click(await screen.findByRole("button", { name: "Actions" }));
    const menu = await screen.findByRole("menu");
    expect(within(menu).getByText("Virtual machines")).toBeVisible();
    expect(within(menu).getByText("Custom resources")).toBeVisible();
    const vms = within(menu).getByRole("menuitem", { name: "web-prd-edge001" });
    expect(vms).toBeVisible();
    await user.click(within(menu).getByRole("menuitem", { name: "web-prd-edge-lb" }));
    expect(await screen.findByRole("menuitem", { name: "Drain" })).toBeVisible();
  });
});
