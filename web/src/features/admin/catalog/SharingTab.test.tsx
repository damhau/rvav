import { screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { HttpResponse, http } from "msw";
import { describe, expect, it } from "vitest";
import type { CatalogEntitlements } from "@/lib/api/types";
import { server } from "@/mocks/node";
import { renderApp } from "@/test/render";
import { shareErrors } from "./SharingTab";

describe("the item's Sharing tab", () => {
  it("shares an item in a project with a group, saying who can request it before saving", async () => {
    const u = userEvent.setup();
    renderApp("/admin/catalog-items/ubuntu-server?tab=entitlements");
    const shares = () => screen.getByRole("table", { name: "Shares" });
    expect(
      within(await screen.findByRole("table", { name: "Shares" })).getByRole("row", { name: /Web Platform/ }),
    ).toHaveTextContent("All members");
    await u.click(screen.getByRole("button", { name: "Share" }));
    const sheet = await screen.findByRole("dialog", { name: "Share Ubuntu 24.04 server" });
    await u.click(within(sheet).getByRole("combobox", { name: "Project" }));
    await u.click(screen.getByRole("option", { name: /Web Platform/ }));
    expect(await within(sheet).findByText("2 people in Web Platform can request it.")).toBeInTheDocument();

    await u.click(within(sheet).getByRole("radio", { name: /Chosen people/ }));
    expect(within(sheet).getByRole("button", { name: "Share" })).toBeDisabled();
    await u.type(within(sheet).getByRole("textbox", { name: "OIDC group" }), "rvav-web{Enter}");
    expect(within(sheet).getByRole("list", { name: "Groups" })).toHaveTextContent("rvav-web");
    const can = await within(sheet).findByRole("list", { name: "Can request it" });
    await waitFor(() => expect(can).toHaveTextContent("Thomas Nguyen · in rvav-web"));
    expect(can).not.toHaveTextContent("Léa Martin");
    expect(within(sheet).getByText(/^Left out: .*Léa Martin/)).toBeInTheDocument();

    await u.click(within(sheet).getByRole("button", { name: "Share" }));
    expect(await screen.findByText("Shared in Web Platform")).toBeInTheDocument();
    await waitFor(() =>
      expect(within(shares()).getByRole("row", { name: /Web Platform/ })).toHaveTextContent("group rvav-web"),
    );
    expect(within(shares()).getByRole("row", { name: /Data Engineering/ })).toHaveTextContent("All members");
  }, 15_000);

  it("opens a share's people from its row, and puts it back to all members", async () => {
    const u = userEvent.setup();
    renderApp("/admin/catalog-items/ubuntu-server?tab=entitlements");
    await u.click(await screen.findByRole("button", { name: "Actions for Data Engineering" }));
    await u.click(await screen.findByRole("menuitem", { name: "Change who may request it" }));
    const sheet = await screen.findByRole("dialog", { name: "Share Ubuntu 24.04 server" });
    expect(within(sheet).getByRole("combobox", { name: "Project" })).toHaveTextContent("Data Engineering");
    await u.click(within(sheet).getByRole("radio", { name: /Chosen people/ }));
    await u.click(within(sheet).getByRole("checkbox", { name: "Project admin" }));
    await u.click(within(sheet).getByRole("button", { name: "Share" }));
    expect(await screen.findByText("Shared in Data Engineering")).toBeInTheDocument();
    const shares = () => screen.getByRole("table", { name: "Shares" });
    await waitFor(() =>
      expect(within(shares()).getByRole("row", { name: /Data Engineering/ })).toHaveTextContent("Project admin"),
    );

    await u.click(screen.getByRole("button", { name: "Actions for Data Engineering" }));
    await u.click(await screen.findByRole("menuitem", { name: "Change who may request it" }));
    const again = await screen.findByRole("dialog", { name: "Share Ubuntu 24.04 server" });
    expect(within(again).getByRole("checkbox", { name: "Project admin" })).toBeChecked();
    await u.click(within(again).getByRole("radio", { name: /All members/ }));
    await u.click(within(again).getByRole("button", { name: "Share" }));
    await waitFor(() =>
      expect(within(shares()).getByRole("row", { name: /Data Engineering/ })).toHaveTextContent("All members"),
    );
  }, 15_000);

  it("keeps each project's own share when the item goes to every project", async () => {
    const bodies: CatalogEntitlements[] = [];
    server.use(
      http.put("*/api/v1/admin/catalog/items/:id/entitlements", async ({ request }) => {
        bodies.push((await request.clone().json()) as CatalogEntitlements);
      }),
    );
    const u = userEvent.setup();
    renderApp("/admin/catalog-items/ubuntu-server?tab=entitlements");
    const shares = () => screen.getByRole("table", { name: "Shares" });
    await u.click(await screen.findByRole("button", { name: "Share" }));
    const sheet = await screen.findByRole("dialog", { name: "Share Ubuntu 24.04 server" });
    await u.click(within(sheet).getByRole("combobox", { name: "Project" }));
    await u.click(screen.getByRole("option", { name: /Web Platform/ }));
    await u.click(within(sheet).getByRole("radio", { name: /Chosen people/ }));
    await u.type(within(sheet).getByRole("textbox", { name: "OIDC group" }), "rvav-web{Enter}");
    await u.click(within(sheet).getByRole("button", { name: "Share" }));
    await waitFor(() =>
      expect(within(shares()).getByRole("row", { name: /Web Platform/ })).toHaveTextContent("group rvav-web"),
    );
    const before = bodies[0]!.project_ids;

    await u.click(screen.getByRole("radio", { name: /Every project/ }));
    expect(screen.getByText(/projects? keeps? (its|their) own share/)).toBeInTheDocument();
    await u.click(screen.getByRole("button", { name: "Save projects" }));
    await waitFor(() => expect(bodies).toHaveLength(2));
    expect(bodies[1]).toEqual({ all_projects: true, project_ids: before });
    await waitFor(() => expect(within(shares()).getByRole("row", { name: /Every project/ })).toBeInTheDocument());
    expect(within(shares()).getByRole("row", { name: /Web Platform/ })).toHaveTextContent("group rvav-web");

    await u.click(screen.getByRole("button", { name: "Save projects" }));
    await waitFor(() => expect(bodies).toHaveLength(3));
    expect(bodies[2]).toEqual({ all_projects: true, project_ids: before });
  }, 20_000);

  it("puts a refusal's pointers on the person, group or role sent", () => {
    const sent = { user_ids: ["usr_a", "usr_b"], groups: ["web"], roles: ["member", "gone"] };
    const { placed, unplaced } = shareErrors(
      [
        { field: "/audiences/1/user_ids/1", code: "unknown", message: "There is no user usr_b." },
        { field: "/audiences/1/roles/1", code: "unknown", message: "There is no role gone given in projects." },
        { field: "/audiences/1/project_id", code: "not_shared", message: "Not shared." },
        { field: "/audiences/0/groups/0", code: "required", message: "Another share's." },
      ],
      "/audiences/1",
      sent,
    );
    expect(placed).toEqual({
      project: "Not shared.",
      users: { usr_b: "There is no user usr_b." },
      groups: {},
      roles: { gone: "There is no role gone given in projects." },
    });
    expect(unplaced.map((e) => e.field)).toEqual(["/audiences/0/groups/0"]);
    expect(
      shareErrors([{ field: "/everywhere/groups/0", message: "Give the group's name." }], "/everywhere", sent).placed
        .groups,
    ).toEqual({
      web: "Give the group's name.",
    });
  });

  it("counts the every-project share in who can request, and keeps the focus in the chips", async () => {
    const u = userEvent.setup();
    renderApp("/admin/catalog-items/ubuntu-server?tab=entitlements");
    await u.click(await screen.findByRole("radio", { name: /Every project/ }));
    await u.click(screen.getByRole("button", { name: "Save projects" }));
    expect(await screen.findByText("Projects saved")).toBeInTheDocument();
    await waitFor(() =>
      expect(
        within(screen.getByRole("table", { name: "Shares" })).getByRole("row", { name: /Every project/ }),
      ).toBeInTheDocument(),
    );

    await u.click(screen.getByRole("button", { name: "Actions for Web Platform" }));
    await u.click(await screen.findByRole("menuitem", { name: "Change who may request it" }));
    const sheet = await screen.findByRole("dialog", { name: "Share Ubuntu 24.04 server" });
    await u.click(within(sheet).getByRole("radio", { name: /Chosen people/ }));
    const group = within(sheet).getByRole("textbox", { name: "OIDC group" });
    await u.type(group, "rvav-web{Enter}");
    await u.type(group, "rvav-data{Enter}");
    expect(await within(sheet).findByText(/Also shared with every project: all members\./)).toBeInTheDocument();
    const can = within(sheet).getByRole("list", { name: "Can request it" });
    await waitFor(() => expect(can).toHaveTextContent("Léa Martin · shared with every project"));
    expect(within(sheet).queryByText(/^Left out/)).not.toBeInTheDocument();

    await u.click(within(sheet).getByRole("button", { name: "Remove group rvav-web" }));
    expect(within(sheet).getByRole("button", { name: "Remove group rvav-data" })).toHaveFocus();
    await u.click(within(sheet).getByRole("button", { name: "Remove group rvav-data" }));
    expect(group).toHaveFocus();
  }, 20_000);

  it("puts a refused group on its field, and moves the focus there", async () => {
    server.use(
      http.put("*/api/v1/admin/catalog/items/:id/entitlements", async ({ request }) => {
        const body = (await request.json()) as CatalogEntitlements;
        const i = (body.audiences ?? []).findIndex((a) => a.project_id === "prj_web");
        return HttpResponse.json(
          {
            type: "about:blank",
            title: "Some inputs need attention",
            status: 422,
            request_id: "req",
            errors: [{ field: `/audiences/${i}/groups/0`, code: "unknown", message: "Check its name." }],
          },
          { status: 422, headers: { "Content-Type": "application/problem+json" } },
        );
      }),
    );
    const u = userEvent.setup();
    renderApp("/admin/catalog-items/ubuntu-server?tab=entitlements");
    await u.click(await screen.findByRole("button", { name: "Share" }));
    const sheet = await screen.findByRole("dialog", { name: "Share Ubuntu 24.04 server" });
    await u.click(within(sheet).getByRole("combobox", { name: "Project" }));
    await u.click(screen.getByRole("option", { name: /Web Platform/ }));
    await u.click(within(sheet).getByRole("radio", { name: /Chosen people/ }));
    const group = within(sheet).getByRole("textbox", { name: "OIDC group" });
    await u.type(group, "rvav-web{Enter}");
    await u.click(within(sheet).getByRole("button", { name: "Share" }));
    await waitFor(() => expect(group).toHaveFocus());
    expect(group).toHaveAccessibleDescription("rvav-web: Check its name.");
    expect(within(sheet).getByRole("button", { name: "Remove group rvav-web" })).toHaveAccessibleDescription(
      "rvav-web: Check its name.",
    );
    expect(screen.queryByText("Sharing failed")).not.toBeInTheDocument();

    await u.click(within(sheet).getByRole("checkbox", { name: "Member" }));
    expect(group).not.toHaveAccessibleDescription(/Check its name/);
  }, 20_000);
});
