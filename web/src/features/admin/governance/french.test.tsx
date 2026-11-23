import { screen, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { beforeEach, describe, expect, it } from "vitest";
import { i18n } from "@/lib/i18n";
import { db } from "@/mocks/seed";
import { renderApp } from "@/test/render";

beforeEach(async () => {
  db.me.locale = "fr";
  await i18n.changeLanguage("fr");
});

describe("templates and networks, in French", () => {
  it("names the tabs, and says what removing a template stops", { timeout: 15_000 }, async () => {
    const user = userEvent.setup();
    renderApp("/admin/infrastructure");
    expect(await screen.findByRole("heading", { level: 1, name: "Modèles et réseaux" })).toBeInTheDocument();
    const tabs = screen.getByRole("tablist", { name: "Sections de l'infrastructure" });
    expect(
      within(tabs)
        .getAllByRole("tab")
        .map((tab) => tab.textContent),
    ).toEqual(["Modèles", "Tailles", "Réseaux", "Plages IP", "Objets vCenter"]);

    const region = await screen.findByRole("region", { name: "Modèles de VM" });
    expect(await within(region).findByRole("row", { name: /RHEL 9.4/ })).toHaveTextContent("1 vCenter");
    await user.click(await within(region).findByRole("button", { name: "Actions pour RHEL 9.4" }));
    await user.click(await screen.findByRole("menuitem", { name: "Retirer" }));
    const confirm = await screen.findByRole("alertdialog", { name: "Retirer RHEL 9.4 ?" });
    expect(confirm).toHaveAccessibleDescription(
      "Les formulaires de VM cessent de proposer rhel9 dans leur sélecteur de modèle.",
    );
    expect(within(confirm).getByRole("button", { name: "Retirer le modèle" })).toBeInTheDocument();
  });

  it("counts a range's free addresses and a cluster's hosts as French does", { timeout: 15_000 }, async () => {
    const user = userEvent.setup();
    renderApp("/admin/infrastructure?tab=ip-ranges");
    const ranges = await screen.findByRole("region", { name: "Plages IP" });
    const lab = await within(ranges).findByRole("row", { name: /Test lab/ });
    expect(lab).toHaveTextContent("10.41.10.0/24 via 10.41.10.1, 2 exclues");
    expect(lab).toHaveTextContent("29 libres");

    await user.click(screen.getByRole("tab", { name: "Objets vCenter" }));
    const objects = await screen.findByRole("region", { name: "Objets vCenter" });
    const prd = await within(objects).findByRole("row", { name: /^GVA-PRD-CL01 Cluster/ });
    // toHaveTextContent reads every space as one: a group separator too.
    const grouped = "\\d{1,3}(?: \\d{3})*";
    expect(prd).toHaveTextContent(new RegExp(`\\d+ hôtes, ${grouped} Go de mémoire libre, dont 32 Go réservés`));
    // Sizes of a thousand and more are grouped as French groups them.
    const datastore = within(objects)
      .getAllByRole("row")
      .find((r) => /Go libres sur/.test(r.textContent) && !/Storage DRS/.test(r.textContent));
    expect(datastore).toHaveTextContent(new RegExp(`\\d \\d{3} Go libres sur ${grouped}`));
  });
});

describe("projects, in French", () => {
  it("names the tabs, and counts what uses an environment", { timeout: 15_000 }, async () => {
    const user = userEvent.setup();
    renderApp("/admin/projects");
    expect(await screen.findByRole("heading", { level: 1, name: "Projets" })).toBeInTheDocument();
    const tabs = screen.getByRole("tablist", { name: "Sections des projets" });
    expect(
      within(tabs)
        .getAllByRole("tab")
        .map((tab) => tab.textContent),
    ).toEqual(["Projets", "Environnements", "Groupes du fournisseur d'identité"]);
    expect(await screen.findByRole("columnheader", { name: /^Quotas utilisés/ })).toBeInTheDocument();

    await user.click(within(tabs).getByRole("tab", { name: "Environnements" }));
    const envs = await screen.findByRole("region", { name: "Environnements" });
    expect((await within(envs).findAllByText(/^\d+ projets?, \d+ déploiements?$/)).length).toBeGreaterThan(0);
  });

  it("shows a project's members, and where its VMs' computer objects go", { timeout: 15_000 }, async () => {
    const user = userEvent.setup();
    renderApp("/admin/projects/prj_web");
    expect(await screen.findByRole("heading", { level: 1, name: "Web Platform" })).toBeInTheDocument();
    expect(screen.getByText(/^\d+ membres?$/)).toBeInTheDocument();
    const tabs = screen.getByRole("tablist", { name: "Sections du projet" });
    expect(
      within(tabs)
        .getAllByRole("tab")
        .map((tab) => tab.textContent),
    ).toEqual(["Membres", "Placement", "Quotas", "Détails"]);
    const members = await screen.findByRole("region", { name: "Membres" });
    expect(await within(members).findByRole("list", { name: "Rôles de Thomas Nguyen" })).toHaveTextContent(
      /donné ici|par /,
    );

    await user.click(within(tabs).getByRole("tab", { name: "Placement" }));
    const directory = await screen.findByRole("region", { name: "Active Directory" });
    const table = await within(directory).findByRole("table", { name: "OU Active Directory de Web Platform" });
    expect(await within(table).findByRole("row", { name: /^Tout environnement/ })).toBeInTheDocument();
    expect(within(table).getByRole("row", { name: /^Tous les projets dans Production/ })).toHaveTextContent(
      "Définie dans Projets › Environnements",
    );
  });
});
