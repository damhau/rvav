import { screen, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { afterEach, describe, expect, it } from "vitest";
import { api, call } from "@/lib/api/client";
import { resetDb } from "@/mocks/seed";
import { renderApp } from "@/test/render";

// The setup checklist (U1.5, ADR-0030), against the mock API; rvav.mock.fresh
// makes it a new install.
function freshInstall() {
  localStorage.setItem("rvav.mock.fresh", "1");
  resetDb();
}

afterEach(() => localStorage.removeItem("rvav.mock.fresh"));

describe("setup checklist", () => {
  it("is where a new install's administrator lands, with nothing done", async () => {
    freshInstall();
    renderApp("/");
    expect(await screen.findByRole("heading", { name: "Set up rvav" })).toBeInTheDocument();
    expect(await screen.findByText("0 of 8 done")).toBeInTheDocument();
    expect(screen.getByRole("progressbar", { name: "Steps done" })).toHaveAttribute("aria-valuenow", "0");
    const steps = within(screen.getByRole("list", { name: "Steps" })).getAllByRole("listitem");
    expect(steps.map((s) => s.getAttribute("aria-label"))).toEqual([
      "Change the administrator's password: to do",
      "Set the identity provider: to do",
      "Map a group to admin: to do",
      "Connect vCenter: to do",
      "Add environments and projects: to do",
      "Connect EfficientIP: to do",
      "Connect AWX: to do",
      "Publish a first catalog item: to do",
    ]);
    expect(steps[1]).toHaveTextContent("Now: None: only the local administrator can sign in.");
    expect(steps[4]).toHaveTextContent("Now: 0 environments, 0 projects");
    // Signed in with the provider (the mock's identity), not as admin.
    expect(steps[0]).toHaveTextContent("Sign in as admin to change it.");
  });

  it("ticks a step once its page has what it needs, and links each to its page", async () => {
    freshInstall();
    await call(
      api.PUT("/admin/integrations/{kind}", {
        params: { path: { kind: "oidc" } },
        body: { settings: { issuer: "https://login.corp.example/realms/rvav", client_id: "rvav" } },
      }),
    );
    const user = userEvent.setup();
    renderApp("/admin/setup");
    const provider = await screen.findByRole("listitem", { name: "Set the identity provider: done" });
    expect(provider).toHaveTextContent("Done: https://login.corp.example/realms/rvav");
    expect(screen.getByText("1 of 8 done")).toBeInTheDocument();

    const awx = screen.getByRole("listitem", { name: "Connect AWX: to do" });
    await user.click(within(awx).getByRole("link", { name: "AWX" }));
    expect(await screen.findByRole("tab", { name: "AWX", selected: true })).toBeInTheDocument();
    expect(await screen.findByRole("region", { name: "AWX connection" })).toBeInTheDocument();
  });

  it("can be kept from opening at sign-in, and stays in Administration", async () => {
    freshInstall();
    const user = userEvent.setup();
    renderApp("/admin/setup");
    await user.click(await screen.findByRole("button", { name: "Don't open it at sign-in" }));
    expect(await screen.findByText("The checklist won't open at sign-in")).toBeInTheDocument();
    await user.click(screen.getByRole("button", { name: "Open it at sign-in again" }));
    expect(await screen.findByText("The checklist opens at sign-in again")).toBeInTheDocument();
    expect(screen.getByRole("link", { name: "Setup" })).toHaveAttribute("href", "/admin/setup");
  });

  it("is offered to an administrator in no project, once landed on it", async () => {
    // The first test landed on it: this page load doesn't again.
    freshInstall();
    renderApp("/");
    expect(await screen.findByText("You are not in a project yet")).toBeInTheDocument();
    expect(screen.getByRole("link", { name: "Set up rvav" })).toHaveAttribute("href", "/admin/setup");
    expect(screen.queryByRole("heading", { name: "Set up rvav" })).not.toBeInTheDocument();
  });

  it("doesn't open on a seeded install", async () => {
    renderApp("/");
    expect(await screen.findByRole("heading", { name: "Overview" })).toBeInTheDocument();
    expect(await screen.findByRole("region", { name: "Needs your attention" })).toBeInTheDocument();
    expect(screen.queryByRole("region", { name: "Setup" })).not.toBeInTheDocument();
    expect(screen.queryByRole("heading", { name: "Set up rvav" })).not.toBeInTheDocument();
  });
});
