import { QueryClient } from "@tanstack/react-query";
import { screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { describe, expect, it } from "vitest";
import { renderApp } from "@/test/render";

// Identity provider groups on the Projects page, with the app's own staleTime
// (main.tsx): what a mapping changes elsewhere is fetched again.
describe("a group mapping", () => {
  it("shows at once in the roles' holders", async () => {
    const client = new QueryClient({
      defaultOptions: { queries: { retry: false, staleTime: 30_000 }, mutations: { retry: false } },
    });
    const user = userEvent.setup();
    const { router } = renderApp("/admin/roles", client);
    expect(await screen.findByRole("row", { name: /^Auditor/ })).toHaveTextContent("Nobody");

    await router.navigate({ to: "/admin/projects", search: { tab: "groups" } });
    const groups = await screen.findByRole("region", { name: "Identity provider groups" });
    await user.click(await within(groups).findByRole("button", { name: "Map a group" }));
    const sheet = await screen.findByRole("dialog", { name: "Map a group" });
    expect(within(sheet).getByRole("textbox", { name: "OIDC group" })).toHaveFocus();
    await user.type(within(sheet).getByRole("textbox", { name: "OIDC group" }), "rvav-audit");
    await user.click(within(sheet).getByRole("button", { name: "Map group" }));
    expect(await within(groups).findByRole("cell", { name: "rvav-audit" })).toBeInTheDocument();

    await router.navigate({ to: "/admin/roles" });
    expect(await screen.findByRole("row", { name: /^Auditor/ })).toHaveTextContent("1 time");
  });

  it("is removed after saying who loses the role", async () => {
    const user = userEvent.setup();
    renderApp("/admin/projects?tab=groups");
    const groups = await screen.findByRole("region", { name: "Identity provider groups" });
    await user.click(await within(groups).findByRole("button", { name: "Actions for rvav-web" }));
    await user.click(await screen.findByRole("menuitem", { name: "Remove" }));
    const confirm = await screen.findByRole("alertdialog", { name: "Remove the mapping of rvav-web?" });
    expect(confirm).toHaveTextContent(/People in rvav-web no longer get Member in Web Platform/);
    await user.click(within(confirm).getByRole("button", { name: "Remove mapping" }));
    expect(await screen.findByText("The mapping of rvav-web removed")).toBeInTheDocument();
    await waitFor(() => expect(within(groups).queryByRole("cell", { name: "rvav-web" })).toBeNull());
  });
});

describe("the projects list", () => {
  it("says each project's quota use, and archives one after asking", async () => {
    const user = userEvent.setup();
    renderApp("/admin/projects");
    const list = await screen.findByRole("region", { name: "Projects" });
    const web = await within(list).findByRole("row", { name: /Web Platform/ });
    await waitFor(() => expect(web).toHaveTextContent("vCPU 112 of 160"));
    await user.click(within(list).getByRole("button", { name: "Actions for Data Engineering" }));
    await user.click(await screen.findByRole("menuitem", { name: "Archive" }));
    const confirm = await screen.findByRole("alertdialog", { name: "Archive Data Engineering?" });
    expect(confirm).toHaveTextContent("Its deployment goes on running, and its members keep it.");
    await user.click(within(confirm).getByRole("button", { name: "Archive project" }));
    expect(await screen.findByText("Data Engineering archived")).toBeInTheDocument();
    await waitFor(() => expect(within(list).queryByRole("link", { name: "Data Engineering" })).toBeNull());

    await user.click(within(list).getByRole("checkbox", { name: "Archived ones too" }));
    await user.click(await within(list).findByRole("button", { name: "Actions for Data Engineering" }));
    await user.click(await screen.findByRole("menuitem", { name: "Put back in use" }));
    await user.click(
      within(await screen.findByRole("alertdialog", { name: "Put Data Engineering back in use?" })).getByRole(
        "button",
        { name: "Put back in use" },
      ),
    );
    expect(await screen.findByText("Data Engineering back in use")).toBeInTheDocument();
  });

  it("makes a project in a sheet, then opens it", async () => {
    const user = userEvent.setup();
    const { router } = renderApp("/admin/projects");
    await user.click(await screen.findByRole("button", { name: "New project" }));
    const sheet = await screen.findByRole("dialog", { name: "New project" });
    expect(within(sheet).getByRole("textbox", { name: "Code" })).toHaveFocus();
    await user.type(within(sheet).getByRole("textbox", { name: "Code" }), "pay");
    await user.type(within(sheet).getByRole("textbox", { name: "Name" }), "Payments");
    await user.click(within(sheet).getByRole("button", { name: "Create" }));
    await waitFor(() => expect(router.state.location.pathname).toMatch(/^\/admin\/projects\/prj_/));
    expect(await screen.findByRole("heading", { name: "Payments", level: 1 })).toBeInTheDocument();
  });
});

describe("environments", () => {
  it("are added in a sheet, and one nothing uses is removed after asking", async () => {
    const user = userEvent.setup();
    renderApp("/admin/projects?tab=environments");
    const envs = await screen.findByRole("region", { name: "Environments" });
    await user.click(await within(envs).findByRole("button", { name: "New environment" }));
    const sheet = await screen.findByRole("dialog", { name: "New environment" });
    await user.type(within(sheet).getByRole("textbox", { name: "Key" }), "qa");
    await user.type(within(sheet).getByRole("textbox", { name: "Name" }), "Quality");
    await user.click(within(sheet).getByRole("button", { name: "Add environment" }));
    expect(await screen.findByText("Quality added")).toBeInTheDocument();
    expect(await within(envs).findByRole("row", { name: /Quality/ })).toHaveTextContent("0 projects, 0 deployments");

    await user.click(within(envs).getByRole("button", { name: "Actions for Production" }));
    expect(await screen.findByRole("menuitem", { name: "Remove: projects deploy to it" })).toHaveAttribute(
      "aria-disabled",
      "true",
    );
    await user.keyboard("{Escape}");
    await user.click(within(envs).getByRole("button", { name: "Actions for Quality" }));
    await user.click(await screen.findByRole("menuitem", { name: "Remove" }));
    await user.click(
      within(await screen.findByRole("alertdialog", { name: "Remove Quality?" })).getByRole("button", {
        name: "Remove environment",
      }),
    );
    expect(await screen.findByText("Quality removed")).toBeInTheDocument();
    await waitFor(() => expect(within(envs).queryByRole("row", { name: /Quality/ })).toBeNull());
  });
});
