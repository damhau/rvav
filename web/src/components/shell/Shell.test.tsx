import { act, cleanup, screen, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { afterEach, describe, expect, it, vi } from "vitest";
import { applyBranding, rvavBranding } from "@/lib/branding";
import { i18n } from "@/lib/i18n";
import { customRoles } from "@/mocks/roles";
import { db, resetDb } from "@/mocks/seed";
import { renderApp } from "@/test/render";
import { adminGroups, adminLabel, mainLabel, mainPages } from "./nav";

afterEach(() => localStorage.removeItem("rvav.mock.as"));

async function mainNav() {
  return screen.findByRole("navigation", { name: "Main" }, { timeout: 5000 });
}

/** Each group of the Administration menu, by its heading, with its links' names. */
function adminMenuOf(nav: HTMLElement) {
  const admin = within(nav).queryByRole("group", { name: "Administration" });
  const menu: Record<string, string[]> = {};
  for (const list of admin ? within(admin).getAllByRole("list") : []) {
    const heading = document.getElementById(list.getAttribute("aria-labelledby") ?? "")?.textContent ?? "";
    menu[heading] = within(list)
      .getAllByRole("link")
      .map((a) => a.textContent ?? "");
  }
  return menu;
}

describe("app shell", () => {
  it("names no vCenter or AWX of the mockup", async () => {
    // The sidebar ended with vc01.corp.example and the catalog said AWX jobs
    // ran on awx.corp.example, on every install: a new one set up from the UI
    // has its own (and rvav can have several vCenters).
    renderApp("/catalog");
    expect(await screen.findByRole("link", { name: "Create database user" }, { timeout: 5000 })).toBeInTheDocument();
    const main = screen.getByRole("navigation", { name: "Main" });
    expect(main).not.toHaveTextContent(/corp\.example/);
    expect(document.body).not.toHaveTextContent(/vc01\.corp\.example|awx\.corp\.example/);
  });
});

describe("sidebar", () => {
  it("counts more than a page of running requests as 50+", async () => {
    const project = db.me.projects[0]!.project.id;
    const first = db.requests.find((r) => r.request.project.id === project)!;
    for (let i = 0; i < 55; i++)
      db.requests.push({
        ...first,
        request: { ...first.request, id: `req_many_${i}`, number: `REQ-9${i}`, status: "running" },
      });
    renderApp("/");
    const nav = await mainNav();
    expect(await within(nav).findByText("50+ running")).toBeInTheDocument();
  });

  it("lists an administrator's pages in four groups, after the main menu", async () => {
    renderApp("/");
    const nav = await mainNav();
    const lists = within(nav).getAllByRole("list");
    expect(
      within(lists[0]!)
        .getAllByRole("link")
        .map((a) => a.textContent),
    ).toEqual([
      "Overview",
      "Catalog",
      "Deployments",
      "Resources",
      expect.stringMatching(/^Requests/),
      expect.stringMatching(/^Approvals/),
      "Reports",
    ]);
    expect(adminMenuOf(nav)).toEqual({
      Catalog: [
        "Catalog items",
        "Property groups",
        "Variables",
        "Form library",
        "Day-2 actions",
        "Custom workflows",
        "Resource types",
        "Subscriptions",
      ],
      Governance: ["Projects", "Roles", "Policies"],
      Infrastructure: ["Integrations", "Templates and networks", "Onboarding", "vRA import"],
      Operations: ["Setup", "Branding", "Notification templates", "Audit log", "Audit forwarding", "System"],
    });
    // Every entry has its icon, and the groups' headings aren't stops on the way.
    for (const link of within(nav).getAllByRole("link").slice(1))
      expect(link.querySelector("svg[aria-hidden]")).not.toBeNull();
    expect(within(nav).queryAllByRole("button")).toHaveLength(0);
  });

  it("shows a project admin only the pages they may open", async () => {
    localStorage.setItem("rvav.mock.as", "project-admin");
    resetDb();
    renderApp("/");
    expect(adminMenuOf(await mainNav())).toEqual({
      Governance: ["Projects", "Policies"],
      Operations: ["Audit log"],
    });
  });

  it("says so on an administration page someone may not open, its items' pages too", async () => {
    localStorage.setItem("rvav.mock.as", "project-admin");
    resetDb();
    renderApp("/admin/catalog-items/rhel9-server");
    expect(
      await screen.findByRole("heading", { level: 1, name: "You can't open this page" }, { timeout: 5000 }),
    ).toBeInTheDocument();
    expect(screen.getByText("Catalog items needs a role you don't have")).toBeInTheDocument();
    expect(screen.getByRole("link", { name: "Go to the overview" })).toHaveAttribute("href", "/");
    expect(screen.queryByRole("button", { name: /New draft/ })).toBeNull();
  });

  it("finds no page at an address under Administration that isn't one", async () => {
    renderApp("/admin/nonsense");
    expect(
      await screen.findByRole("heading", { level: 1, name: "Page not found" }, { timeout: 5000 }),
    ).toBeInTheDocument();
    expect(screen.getByRole("link", { name: "Go to the overview" })).toHaveAttribute("href", "/");
  });

  it("shows a custom role only its groups and pages", async () => {
    // A VM operator holds nothing of the administration; a custom role adds
    // what workflows, subscriptions, the shared variables and the templates
    // take, read only.
    localStorage.setItem("rvav.mock.as", "vm-operator");
    resetDb();
    expect(adminMenuOf(await renderAndFindNav())).toEqual({});
    const permissions = ["extensibility.read", "infrastructure.read"];
    customRoles(db).push({ key: "reader", name: "Reader", description: "", permissions, built_in: false });
    db.me.permissions = [...db.me.permissions, ...permissions];
    db.me.roles = [...db.me.roles, { key: "reader", name: "Reader" }];
    cleanup();
    expect(adminMenuOf(await renderAndFindNav())).toEqual({
      Catalog: ["Variables", "Custom workflows", "Resource types", "Subscriptions"],
      Infrastructure: ["Templates and networks"],
    });
  });

  it("is an icon rail with tooltips between a phone's width and a desktop's", async () => {
    const media = vi.spyOn(window, "matchMedia").mockImplementation(
      (query) =>
        ({
          matches: query === "(width < 64rem)",
          media: query,
          addEventListener: () => {},
          removeEventListener: () => {},
        }) as unknown as MediaQueryList,
    );
    try {
      renderApp("/admin/policies");
      const nav = await mainNav();
      const link = await within(nav).findByRole("link", { name: "Policies" });
      expect(link).toHaveAttribute("aria-current", "page");
      expect(link.querySelector(".sr-only")).toHaveTextContent("Policies");
      expect(adminMenuOf(nav)).toHaveProperty("Governance", ["Projects", "Roles", "Policies"]);
      act(() => link.focus());
      expect(await screen.findByRole("tooltip")).toHaveTextContent("Policies");
    } finally {
      media.mockRestore();
    }
  });

  it("names the portal in a tooltip on the rail, where only its logo shows", async () => {
    const media = vi.spyOn(window, "matchMedia").mockImplementation(
      (query) =>
        ({
          matches: query === "(width < 64rem)",
          media: query,
          addEventListener: () => {},
          removeEventListener: () => {},
        }) as unknown as MediaQueryList,
    );
    const name = "Plateforme cloud du canton de Genève, GE";
    act(() => applyBranding({ ...rvavBranding, name }));
    try {
      renderApp("/admin/policies");
      const home = within(await mainNav()).getAllByRole("link")[0]!;
      expect(home).toHaveAccessibleName(`${name} GVA`);
      act(() => home.focus());
      expect(await screen.findByRole("tooltip")).toHaveTextContent(name);
    } finally {
      media.mockRestore();
      act(() => applyBranding(rvavBranding));
    }
  });
});

async function renderAndFindNav() {
  renderApp("/");
  return mainNav();
}

describe("pages and the menu", () => {
  const pages = [
    ...mainPages.map((p) => ({ to: p.to, label: mainLabel(p.key) })),
    ...adminGroups.flatMap((g) => g.pages.map((p) => ({ to: p.to, label: adminLabel(p) }))),
  ];

  it.each(pages)("$to is titled $label, and its menu entry is the current page", async ({ to, label }) => {
    renderApp(to);
    expect(await screen.findByRole("heading", { level: 1, name: label }, { timeout: 5000 })).toBeInTheDocument();
    const current = within(await mainNav())
      .getAllByRole("link")
      .filter((a) => a.getAttribute("aria-current") === "page");
    expect(current.map((a) => a.getAttribute("href"))).toContain(to);
    expect(current.every((a) => a.getAttribute("href") === to)).toBe(true);
  });
});

describe("command palette", () => {
  it("finds the administration pages in the sidebar's groups", async () => {
    renderApp("/");
    await mainNav();
    const user = userEvent.setup();
    await user.keyboard("{Control>}k{/Control}");
    const input = await screen.findByRole("combobox");
    expect(screen.queryByRole("group", { name: "Governance" })).not.toBeInTheDocument();
    await user.type(input, "sizes");
    const infra = await screen.findByRole("group", { name: "Infrastructure" });
    expect(
      within(infra)
        .getAllByRole("option")
        .map((o) => o.textContent),
    ).toEqual(["Templates and networks"]);
    await user.clear(input);
    await user.type(input, "governance");
    const governance = await screen.findByRole("group", { name: "Governance" });
    expect(
      within(governance)
        .getAllByRole("option")
        .map((o) => o.textContent),
    ).toEqual(["Projects", "Roles", "Policies"]);
  });

  it("finds a page by its words in French, and in English too", async () => {
    db.me.locale = "fr";
    await i18n.changeLanguage("fr");
    renderApp("/");
    await screen.findByRole("navigation", { name: "Principale" });
    const user = userEvent.setup();
    await user.keyboard("{Control>}k{/Control}");
    const input = await screen.findByRole("combobox");
    for (const words of ["couleur", "accent colour"]) {
      await user.clear(input);
      await user.type(input, words);
      const operations = await screen.findByRole("group", { name: "Exploitation" });
      expect(within(operations).getByRole("option", { name: "Image de marque" })).toBeInTheDocument();
    }
    await user.clear(input);
    await user.type(input, "courriel");
    expect(await screen.findByRole("option", { name: "Modèles de notification" })).toBeInTheDocument();
  });
});
