import { screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { describe, expect, it } from "vitest";
import { db } from "@/mocks/seed";
import { renderApp } from "@/test/render";

const names = (table: HTMLElement) =>
  within(table)
    .getAllByRole("row")
    .slice(1)
    .map((r) => within(r).getAllByRole("button")[0]!.textContent);

const pick = async (user: ReturnType<typeof userEvent.setup>, select: string, option: string) => {
  await user.click(screen.getByRole("combobox", { name: select }));
  await user.click(await screen.findByRole("option", { name: option }));
};

describe("the Resources page against the mock API", () => {
  it("lists the project's VMs with their addresses, placement and size", async () => {
    renderApp("/resources");
    expect(await screen.findByRole("heading", { level: 1, name: "Resources" }, { timeout: 5000 })).toBeInTheDocument();
    const table = await screen.findByRole("table", { name: "VMs" });
    await within(table).findByText("web-prd-front001");
    expect(names(table)).toHaveLength(12);
    expect(within(table).queryByText("data-prd-pgledger001")).not.toBeInTheDocument();

    const row = within(table).getByRole("button", { name: "web-tst-search001" }).closest("tr")!;
    expect(row).toHaveTextContent("10.41.10.23");
    expect(row).toHaveTextContent("vc01.corp.example");
    expect(row).toHaveTextContent("GVA-TST-CL01");
    expect(row).toHaveTextContent("32 GB");
    expect(row).toHaveTextContent("1 · 250 GB");
    expect(row).toHaveTextContent(/rvav-dep-.+-app/);
    expect(screen.getByText("12 VMs · 10 powered on")).toBeInTheDocument();
  });

  it("filters by power, cluster and network, each kept in the address", async () => {
    const user = userEvent.setup();
    const { router } = renderApp("/resources");
    const table = await screen.findByRole("table", { name: "VMs" }, { timeout: 5000 });
    await within(table).findByText("web-prd-front001");

    await pick(user, "Power", "Powered off");
    await waitFor(() => expect(names(table)).toEqual(["web-dev-legacy001", "web-tst-search002"]));
    expect(router.state.location.search).toEqual({ power: "off" });

    await pick(user, "Power", "Any power state");
    await pick(user, "Cluster", "GVA-DEV-CL01");
    await waitFor(() => expect(names(table)).toEqual(["web-dev-api004", "web-dev-legacy001", "web-dev-worker002"]));

    await pick(user, "Cluster", "All clusters");
    await pick(user, "Network", "dvpg-web-prd-vl210");
    await waitFor(() =>
      expect(names(table)).toEqual([
        "web-prd-cache001",
        "web-prd-cache002",
        "web-prd-edge001",
        "web-prd-edge002",
        "web-prd-front001",
        "web-prd-front002",
        "web-prd-front003",
      ]),
    );
    expect(router.state.location.search).toEqual({ network: "dvpg-web-prd-vl210" });
  }, 15_000);

  it("finds VMs by IP address, and says so when none match", async () => {
    const user = userEvent.setup();
    renderApp("/resources?q=10.21.10.8");
    const table = await screen.findByRole("table", { name: "VMs" }, { timeout: 5000 });
    await waitFor(() => expect(names(table)).toEqual(["web-prd-cache001", "web-prd-cache002"]));

    await user.clear(screen.getByRole("searchbox", { name: "Search VMs" }));
    await user.type(screen.getByRole("searchbox", { name: "Search VMs" }), "no-such-vm");
    expect(await screen.findByText("No VMs match")).toBeInTheDocument();
  });

  it("sorts the whole list by a column's header", async () => {
    const user = userEvent.setup();
    const { router } = renderApp("/resources");
    const table = await screen.findByRole("table", { name: "VMs" }, { timeout: 5000 });
    await within(table).findByText("web-prd-front001");

    await user.click(within(table).getByRole("button", { name: "Disks" }));
    await user.click(within(table).getByRole("button", { name: "Disks" }));
    await waitFor(() => expect(router.state.location.search).toEqual({ sort: "-disk_gb" }));
    const sizes = () =>
      within(table)
        .getAllByRole("row")
        .slice(1)
        .map((r) => Number(/· (\d+) GB/.exec(r.textContent ?? "")![1]));
    await waitFor(() => expect(sizes()).toEqual([...sizes()].sort((a, b) => b - a)));
    expect(sizes()[0]).toBeGreaterThan(sizes().at(-1)!);
    expect(within(table).getByRole("columnheader", { name: /Disks/ })).toHaveAttribute("aria-sort", "descending");
  }, 15_000);

  it("opens a VM's properties in place, and its deployment from the row", async () => {
    const user = userEvent.setup();
    const { router } = renderApp("/resources");
    const table = await screen.findByRole("table", { name: "VMs" }, { timeout: 5000 });
    await user.click(await within(table).findByRole("button", { name: "web-prd-front001" }));
    const sheet = await screen.findByRole("dialog", { name: "web-prd-front001" });
    expect(within(sheet).getByText("Network adapters")).toBeInTheDocument();
    expect(router.state.location.pathname).toBe("/resources");
    await user.keyboard("{Escape}");
    await waitFor(() => expect(within(table).getByRole("button", { name: "web-prd-front001" })).toHaveFocus());

    const row = within(table).getByRole("button", { name: "web-prd-front001" }).closest("tr")!;
    await user.click(within(row).getByRole("link", { name: "web-prd-front" }));
    const id = db.deployments.find((d) => d.name === "web-prd-front")!.id;
    await waitFor(() => expect(router.state.location.pathname).toBe(`/deployments/${id}`));
  }, 15_000);

  it("lists the VMs of every project of the person's, each with its project", async () => {
    const user = userEvent.setup();
    const { router } = renderApp("/resources");
    const table = await screen.findByRole("table", { name: "VMs" }, { timeout: 5000 });
    await within(table).findByText("web-prd-front001");

    await user.click(screen.getByRole("button", { name: "All my projects" }));
    expect(await within(table).findByText("data-prd-pgledger001")).toBeInTheDocument();
    expect(router.state.location.search).toEqual({ project: "all" });
    const row = within(table).getByRole("button", { name: "data-prd-pgledger001" }).closest("tr")!;
    expect(row).toHaveTextContent("Data Engineering");
  });

  it("opens from the command palette with what was typed", async () => {
    const user = userEvent.setup();
    const { router } = renderApp("/");
    await user.click(await screen.findByRole("button", { name: /Search VMs, IPs, requests/ }, { timeout: 5000 }));
    await user.type(await screen.findByPlaceholderText("Search VMs, IPs, requests, or type a command"), "10.41");
    await user.click(await screen.findByRole("option", { name: /Find “10.41” in Resources/ }));
    await waitFor(() => expect(router.state.location.pathname).toBe("/resources"));
    expect(router.state.location.search).toEqual({ q: "10.41" });
    const table = await screen.findByRole("table", { name: "VMs" });
    await waitFor(() => expect(names(table)).toEqual(["web-tst-search001", "web-tst-search002"]));
  }, 15_000);
});
