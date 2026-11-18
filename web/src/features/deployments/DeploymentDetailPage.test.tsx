import { screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { describe, expect, it } from "vitest";
import { db } from "@/mocks/seed";
import { renderApp } from "@/test/render";

const idOf = (name: string) => db.deployments.find((d) => d.name === name)!.id;

describe("a deployment's page against the mock API", () => {
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

  it("asks before a risky action, saying what it changes", async () => {
    const user = userEvent.setup();
    renderApp(`/deployments/${idOf("web-dev-api")}`);
    await user.click(await screen.findByRole("button", { name: "Actions" }, { timeout: 5000 }));
    await user.click(await screen.findByRole("menuitem", { name: "Delete" }));
    const dialog = await screen.findByRole("dialog", { name: "Delete · web-dev-api" });
    expect(dialog).toHaveTextContent("Deletes the VMs and releases their IP addresses and DNS records.");
    const confirm = within(dialog).getByRole("button", { name: "Delete" });
    expect(confirm).toBeDisabled();
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
    expect(await screen.findByRole("region", { name: "Inputs" })).toHaveTextContent("role: worker");
  });
});
