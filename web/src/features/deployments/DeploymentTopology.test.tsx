import { fireEvent, screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { describe, expect, it, vi } from "vitest";
import type { VmResource } from "@/lib/api/types";
import { db } from "@/mocks/seed";
import { renderApp, renderWithQuery } from "@/test/render";
import DeploymentTopology from "./DeploymentTopology";

const search = () => db.deployments.find((d) => d.name === "web-tst-search")!;
const card = (canvas: HTMLElement, name: RegExp) =>
  within(canvas)
    .getAllByRole("button", { name })
    .find((b) => b.classList.contains("react-flow__node"))!;

describe("a deployment's Topology tab", () => {
  it("opens a VM's properties from its node, by click and by keyboard", { timeout: 15_000 }, async () => {
    const user = userEvent.setup();
    renderApp(`/deployments/${search().id}?tab=topology`);
    const canvas = await screen.findByRole("application", { name: "Topology of web-tst-search" }, { timeout: 5000 });
    expect(screen.getByRole("tab", { name: "Topology", selected: true })).toBeInTheDocument();
    expect(card(canvas, /^Segment rvav-dep-.*-app\. Subnet: 10\.41\.10\.0\/26/)).toBeInTheDocument();
    expect(card(canvas, /^Group Search\./)).toBeInTheDocument();

    const second = card(canvas, /^web-tst-search002\./);
    expect(second).toHaveAccessibleName(/1 problem: Its NSX tags changed outside rvav\./);
    fireEvent.click(second);
    expect(await screen.findByRole("dialog", { name: "web-tst-search002" })).toHaveTextContent("rvav/tst");
    await user.keyboard("{Escape}");
    await waitFor(() => expect(screen.queryByRole("dialog")).not.toBeInTheDocument());
    expect(screen.getByRole("region", { name: "Selected: web-tst-search002" })).toBeInTheDocument();

    // The node last visited is the graph's Tab stop; Home moves to the first.
    second.focus();
    await user.keyboard("{Home}");
    const first = card(canvas, /^web-tst-search001\./);
    await waitFor(() => expect(first).toHaveFocus());
    await user.keyboard("{Enter}");
    expect(await screen.findByRole("dialog", { name: "web-tst-search001" })).toBeInTheDocument();
    await user.keyboard("{Escape}");
    await waitFor(() => expect(first).toHaveFocus());

    await user.click(screen.getByRole("button", { name: "Show the properties of web-tst-search001" }));
    expect(await screen.findByRole("dialog", { name: "web-tst-search001" })).toBeInTheDocument();
  });

  it("lists the VMs of a component drawn as one node, each opening its properties", () => {
    const base = search().resources.find((r): r is VmResource => r.type === "vsphere.vm")!;
    const vms = Array.from({ length: 6 }, (_, i): VmResource => ({
      ...base,
      id: `res_w${i + 1}`,
      name: `shop-dev-worker0${i + 1}`,
    }));
    const onOpen = vi.fn();
    renderWithQuery(
      <DeploymentTopology deployment={{ ...search(), name: "shop-dev", resources: vms }} onOpen={onOpen} />,
    );
    const canvas = screen.getByRole("application", { name: "Topology of shop-dev" });
    fireEvent.click(card(canvas, /^6 VMs shop-dev\./));
    const listed = screen.getByRole("region", { name: "VMs of shop-dev" });
    expect(within(listed).getAllByRole("button")).toHaveLength(6);
    fireEvent.click(within(listed).getByRole("button", { name: "shop-dev-worker05 properties" }));
    expect(onOpen).toHaveBeenCalledWith(vms[4]);
  });

  it("says when there is nothing to draw", () => {
    renderWithQuery(<DeploymentTopology deployment={{ ...search(), resources: [] }} onOpen={vi.fn()} />);
    expect(screen.queryByRole("application")).not.toBeInTheDocument();
    expect(screen.getByText(/^Nothing to draw: the deployment holds no VMs or NSX resources\.$/)).toBeInTheDocument();
  });
});
