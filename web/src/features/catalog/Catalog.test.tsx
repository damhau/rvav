import { screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { describe, expect, it } from "vitest";
import { db } from "@/mocks/seed";
import { renderApp } from "@/test/render";

describe("the catalog against the mock API", () => {
  it("shows each item's icon, kind and summary, and opens its page", async () => {
    const user = userEvent.setup();
    const { router } = renderApp("/catalog");
    const link = await screen.findByRole("link", { name: "RHEL 9 server" }, { timeout: 5000 });
    expect(screen.getByText("11 items")).toBeInTheDocument();
    expect(screen.getByText(/Red Hat Enterprise Linux 9\.4 with the corporate baseline/)).toBeInTheDocument();
    expect(screen.getByRole("link", { name: "Request RHEL 9 server" })).toHaveAttribute(
      "href",
      "/catalog/rhel9-server/request",
    );
    await user.click(link);
    await waitFor(() => expect(router.state.location.pathname).toBe("/catalog/rhel9-server"));
  });

  it("filters in the toolbar, and says what to do when nothing matches", async () => {
    const user = userEvent.setup();
    const { router } = renderApp("/catalog");
    await screen.findByRole("link", { name: "RHEL 9 server" }, { timeout: 5000 });
    await user.type(screen.getByRole("searchbox", { name: "Search the catalog" }), "nothing like this");
    expect(await screen.findByText("No items match")).toBeInTheDocument();
    await waitFor(() => expect(router.state.location.search).toEqual({ q: "nothing like this" }));
    await user.click(screen.getAllByRole("button", { name: "Clear filters" })[0]!);
    expect(await screen.findByText("11 items")).toBeInTheDocument();
    expect(screen.getByRole("searchbox", { name: "Search the catalog" })).toHaveValue("");
  });

  it("offers no Request to someone who may not request, and says so", async () => {
    db.me.permissions = db.me.permissions.filter((x) => x !== "request.submit");
    db.me.projects = db.me.projects.map((p) => ({
      ...p,
      permissions: p.permissions.filter((x) => x !== "request.submit"),
    }));
    renderApp("/catalog");
    expect(await screen.findByRole("link", { name: "RHEL 9 server" }, { timeout: 5000 })).toBeInTheDocument();
    expect(screen.queryByRole("link", { name: /^Request / })).toBeNull();
    expect(screen.getByText(/Your roles in Web Platform don't let you request/)).toBeInTheDocument();
  });

  it("says when nothing is shared with the project", async () => {
    db.items.forEach((i) => (i.projectIds = []));
    renderApp("/catalog");
    expect(await screen.findByText("Nothing is shared with Web Platform yet")).toBeInTheDocument();
  });
});

describe("a catalog item's page", () => {
  it("renders the description, the version and what it builds, with Request first", async () => {
    renderApp("/catalog/rhel9-server");
    expect(await screen.findByRole("heading", { name: "RHEL 9 server", level: 1 })).toBeInTheDocument();
    const about = screen.getByRole("region", { name: "About this item" });
    expect(within(about).getByRole("heading", { name: "What you get", level: 3 })).toBeInTheDocument();
    expect(within(about).getAllByRole("listitem")).toHaveLength(3);
    expect(within(about).getByText("tpl-rhel9.4-2026.10").tagName).toBe("CODE");
    expect(within(about).getByRole("link", { name: "RHEL build standard" })).toHaveAttribute("target", "_blank");

    const details = screen.getByRole("region", { name: "Details" });
    expect(within(details).getByText("Version").nextElementSibling).toHaveTextContent(/v3\.2 · published/);
    expect(within(details).getByText("Approval").nextElementSibling).toHaveTextContent("Needs approval in production");

    const builds = await screen.findByRole("region", { name: "What it builds" });
    expect(within(builds).getByText("Virtual machine")).toBeInTheDocument();
    expect(within(builds).getByText("Template").nextElementSibling).toHaveTextContent(/tpl-rhel9\.4-2026\.10/);

    expect(screen.getByRole("link", { name: "Request" })).toHaveAttribute("href", "/catalog/rhel9-server/request");
  });

  it("draws a composite's steps", async () => {
    renderApp("/catalog/postgres-cluster");
    const steps = await screen.findByRole("region", { name: "Steps, in the order they run" });
    expect(await within(steps).findByRole("region", { name: "Steps of PostgreSQL 16 cluster" })).toBeInTheDocument();
  });

  it("says what an AWX item launches, and leaves out what it hasn't got", async () => {
    renderApp("/catalog/db-user");
    const does = await screen.findByRole("region", { name: "What it does" });
    expect(does).toHaveTextContent("Launches the AWX job template pg-create-role with what you fill in.");
    expect(screen.queryByRole("region", { name: "About this item" })).not.toBeInTheDocument();
    const details = screen.getByRole("region", { name: "Details" });
    expect(within(details).queryByText("Tags")).not.toBeInTheDocument();
    for (const dd of details.querySelectorAll("dd")) expect(dd.textContent?.trim()).not.toBe("");
  });

  it("says why Request is off for someone who may not request", { timeout: 15_000 }, async () => {
    db.me.permissions = db.me.permissions.filter((x) => x !== "request.submit");
    db.me.projects = db.me.projects.map((p) => ({
      ...p,
      permissions: p.permissions.filter((x) => x !== "request.submit"),
    }));
    renderApp("/catalog/rhel9-server");
    const request = await screen.findByRole("button", { name: "Request" });
    expect(request).toBeDisabled();
    expect(request).toHaveAccessibleDescription("Your roles in Web Platform don't let you request");
  });
});
