import { screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { beforeEach, describe, expect, it } from "vitest";
import { i18n } from "@/lib/i18n";
import { db } from "@/mocks/seed";
import { renderApp } from "@/test/render";

beforeEach(async () => {
  db.me.locale = "fr";
  await i18n.changeLanguage("fr");
});

describe("the catalog's items, categories and secrets, in French", () => {
  it("counts an item's projects and the files that need you as French does", { timeout: 15_000 }, async () => {
    const user = userEvent.setup();
    renderApp("/admin/catalog-items");
    const items = await screen.findByRole("region", { name: "Éléments" });
    await user.click(await within(items).findByRole("button", { name: "Actions pour Windows Server 2025" }));
    await user.click(await screen.findByRole("menuitem", { name: "Supprimer" }));
    const confirm = await screen.findByRole("alertdialog", { name: /^Supprimer Windows Server 2025.\?$/ });
    expect(confirm).toHaveTextContent("Il est ouvert à 2 projets, et quitte leur catalogue.");
    expect(within(confirm).getByRole("button", { name: "Supprimer l'élément" })).toBeInTheDocument();
    await user.click(within(confirm).getByRole("button", { name: "Annuler" }));

    const banner = screen.getByRole("region", { name: "Dépôt Git" });
    expect(banner).toHaveTextContent(/\d fichiers ont besoin de vous/);
    await user.click(within(banner).getByRole("button", { name: "Détails du dépôt Git" }));
    const sheet = await screen.findByRole("dialog", { name: "Dépôt Git" });
    expect(within(sheet).getByRole("list", { name: "Fichiers qui ont besoin de vous" })).toHaveTextContent(
      "Conflit dans Git",
    );
    expect(within(sheet).getByRole("button", { name: "Lire le dépôt maintenant" })).toBeInTheDocument();
  });

  it("names a secret's kinds", { timeout: 15_000 }, async () => {
    const user = userEvent.setup();
    renderApp("/admin/catalog-items?tab=secrets");
    const region = await screen.findByRole("region", { name: "Secrets" });
    expect(await within(region).findByRole("columnheader", { name: /^Utilisé par/ })).toBeInTheDocument();
    await user.click(within(region).getByRole("button", { name: "Nouveau secret" }));
    const sheet = await screen.findByRole("dialog", { name: "Nouveau secret" });
    expect(sheet).toHaveTextContent('les données utilisateur cloud-init le lisent comme {{ secret "name" }}');
    expect(within(sheet).getByRole("combobox", { name: "Type" })).toHaveTextContent("Jeton porteur (bearer)");
  });
});

describe("an item's sharing, in French", () => {
  it("counts who can request it in a project", { timeout: 15_000 }, async () => {
    const user = userEvent.setup();
    renderApp("/admin/catalog-items/ubuntu-server?tab=entitlements");
    const shares = await screen.findByRole("table", { name: "Partages" });
    expect(within(shares).getByRole("row", { name: /Web Platform/ })).toHaveTextContent("Tous les membres");
    await user.click(screen.getByRole("button", { name: "Partager" }));
    const sheet = await screen.findByRole("dialog", { name: "Partager Ubuntu 24.04 server" });
    await user.click(within(sheet).getByRole("combobox", { name: "Projet" }));
    await user.click(screen.getByRole("option", { name: /Web Platform/ }));
    expect(await within(sheet).findByText("2 personnes dans Web Platform peuvent le demander.")).toBeInTheDocument();
    await user.click(within(sheet).getByRole("radio", { name: /Personnes choisies/ }));
    await user.type(within(sheet).getByRole("textbox", { name: "Groupe OIDC" }), "rvav-web{Enter}");
    const can = await within(sheet).findByRole("list", { name: "Peuvent le demander" });
    await waitFor(() => expect(can).toHaveTextContent("Thomas Nguyen · dans rvav-web"));
    expect(within(sheet).getByText(/^Laissés de côté.: .*Léa Martin/)).toBeInTheDocument();
  });
});

describe("shared variables, in French", () => {
  it("names a value's kind, and says how steps read a new one", { timeout: 15_000 }, async () => {
    const user = userEvent.setup();
    renderApp("/admin/variables");
    expect(await screen.findByRole("heading", { level: 1, name: "Variables" })).toBeInTheDocument();
    const table = await screen.findByRole("table", { name: "Variables" });
    const dns = await within(table).findByRole("row", { name: /vars\.network\.dns_servers/ });
    expect(dns).toHaveTextContent("Liste");
    expect(dns).toHaveTextContent("par Marc Keller");
    await user.click(screen.getByRole("button", { name: "Nouvelle variable" }));
    const sheet = await screen.findByRole("dialog", { name: "Nouvelle variable" });
    await user.type(within(sheet).getByLabelText("Groupe"), "monitoring");
    await user.type(within(sheet).getByLabelText("Nom"), "targets");
    expect(sheet).toHaveTextContent("Les étapes et les conditions la lisent sous la forme vars.monitoring.targets.");
  });
});
