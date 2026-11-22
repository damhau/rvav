import { screen, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { beforeEach, describe, expect, it } from "vitest";
import { i18n } from "@/lib/i18n";
import { db } from "@/mocks/seed";
import { renderApp } from "@/test/render";
import { roleName } from "./Applying";

beforeEach(async () => {
  db.me.locale = "fr";
  await i18n.changeLanguage("fr");
});

describe("integrations, in French", () => {
  it("counts clusters, VMs, addresses and records as each language does", () => {
    expect(i18n.t("admin:vcenters.clusters", { count: 1 })).toBe("1 cluster");
    expect(i18n.t("admin:vcenters.vms", { count: 2 })).toBe("2 VM");
    expect(i18n.t("admin:integrations.reconcile.addresses", { count: 0 })).toBe("0 adresse");
    expect(i18n.t("admin:integrations.reconcile.records", { count: 3 })).toBe("3 enregistrements DNS");
    expect(i18n.t("admin:vcenters.clusters", { count: 1, lng: "en" })).toBe("1 cluster");
    expect(i18n.t("admin:vcenters.vms", { count: 1, lng: "en" })).toBe("1 VM");
    expect(roleName("sync")).toBe("Synchronisation de l'inventaire");
  });

  it("lists the connections, and runs the EfficientIP reconcile", { timeout: 15_000 }, async () => {
    const user = userEvent.setup();
    renderApp("/admin/integrations");
    expect(await screen.findByRole("heading", { level: 1, name: "Intégrations" })).toBeInTheDocument();
    const table = await screen.findByRole("table", { name: "Connexions" });
    expect(await within(table).findByRole("link", { name: "Fournisseur d'identité" })).toBeInTheDocument();
    await user.click(screen.getByRole("tab", { name: "Vérifications" }));
    const panel = await screen.findByRole("region", { name: "Rapprochement EfficientIP" });
    await user.click(within(panel).getByRole("button", { name: "Lancer la vérification" }));
    expect(await within(panel).findByRole("table", { name: "Adresses qu'aucun déploiement ne possède" })).toBeVisible();
    expect(panel).toHaveTextContent(
      "Plages IP de rvav : 1 adresse de VM que rvav n'a plus, 2 VM sur une adresse que leur plage n'a pas donnée.",
    );
  });

  it("tests vCenter's connection and lists the vCenters", { timeout: 15_000 }, async () => {
    const user = userEvent.setup();
    renderApp("/admin/integrations/vcenter");
    const connection = await screen.findByRole("region", { name: "Connexion vCenter" });
    await user.click(within(connection).getByRole("button", { name: "Tester la connexion" }));
    const result = await within(connection).findByRole("region", { name: "Test vCenter" });
    expect(result).toHaveTextContent(/^(Toutes les vérifications ont réussi|Une vérification a échoué)\./);
    expect(within(result).getAllByLabelText(/^(Réussi|Échoué)$/)).not.toHaveLength(0);
    expect(within(connection).getByRole("button", { name: "Enregistrer les paramètres" })).toBeInTheDocument();
    const vcenters = screen.getByRole("region", { name: "vCenter" });
    const row = await within(vcenters).findByRole("row", { name: /vc01\.corp\.example/ });
    expect(row).toHaveTextContent(/4 clusters, \d+ VM/);
    expect(row).toHaveTextContent("La connexion ci-dessus");
    expect(within(vcenters).getByRole("button", { name: "Ajouter un vCenter" })).toBeInTheDocument();
  });

  it("shows the identity provider's settings", { timeout: 15_000 }, async () => {
    renderApp("/admin/integrations/oidc");
    const p = await screen.findByRole("region", { name: "Paramètres du fournisseur" });
    expect(within(p).getByLabelText("Émetteur")).toHaveValue("https://login.corp.example/realms/rvav");
    expect(within(p).getByRole("button", { name: "Essayer de se connecter" })).toBeDisabled();
    expect(within(p).getByRole("link", { name: "Projets › Groupes du fournisseur d'identité" })).toHaveAttribute(
      "href",
      "/admin/projects?tab=groups",
    );
  });
});
