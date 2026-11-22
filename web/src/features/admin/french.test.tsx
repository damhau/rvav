import { screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { afterEach, beforeEach, describe, expect, it } from "vitest";
import { i18n } from "@/lib/i18n";
import { db, resetDb } from "@/mocks/seed";
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

  it("makes ServiceNow's token and reads its CMDB report", { timeout: 15_000 }, async () => {
    const user = userEvent.setup();
    renderApp("/admin/integrations/servicenow");
    const token = await screen.findByRole("region", { name: "Jeton de ServiceNow" });
    expect(await within(token).findByText(/ServiceNow a appelé rvav pour la dernière fois/)).toBeInTheDocument();
    expect(token).toHaveTextContent(/Accepté depuis n'importe quelle adresse, jusqu'à \d+ requêtes par minute/);
    await user.click(within(token).getByRole("button", { name: "Créer un nouveau jeton" }));
    const made = await within(token).findByRole("region", { name: "Jeton de ServiceNow créé" });
    expect(made).toHaveTextContent("Le jeton créé auparavant est révoqué.");
    const cmdb = screen.getByRole("region", { name: "CMDB" });
    expect(await within(cmdb).findByText("Tous les projets")).toBeInTheDocument();
    expect(within(cmdb).getByRole("table", { name: "Déploiements refusés par ServiceNow" })).toBeInTheDocument();
    expect(within(cmdb).getByRole("table", { name: "CI que rvav n'a plus" })).toHaveTextContent(
      "(s'ouvre dans un nouvel onglet)",
    );
    expect(i18n.t("admin:serviceNow.cmdb.servicenowHas", { count: 1, lng: "en" })).toBe(
      ": ServiceNow has 1 CI of this source, not retired",
    );
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

describe("system, setup, branding and the audit log, in French", () => {
  afterEach(() => localStorage.removeItem("rvav.mock.fresh"));

  it("counts details and dropped events as each language does", () => {
    expect(i18n.t("admin:audit.details", { count: 1 })).toBe("1 détail");
    expect(i18n.t("admin:audit.details", { count: 2 })).toBe("2 détails");
    expect(i18n.t("admin:auditForwarding.dropped", { count: 1, n: "1" })).toBe(
      "L'événement qui l'attend est abandonné. Le journal d'audit le garde.",
    );
    expect(i18n.t("admin:auditForwarding.dropped", { count: 1, n: "1", lng: "en" })).toBe(
      "The event waiting for it is dropped. The audit log keeps it.",
    );
  });

  it("opens a new install on the setup checklist", async () => {
    localStorage.setItem("rvav.mock.fresh", "1");
    resetDb();
    db.me.locale = "fr";
    renderApp("/");
    expect(await screen.findByRole("heading", { level: 1, name: "Mise en route" })).toBeInTheDocument();
    expect(await screen.findByText("0 sur 8 faites")).toBeInTheDocument();
    const steps = within(screen.getByRole("list", { name: "Étapes" })).getAllByRole("listitem");
    expect(steps[0]).toHaveAccessibleName(/^Changer le mot de passe de l'administrateur\s: à faire$/);
    expect(within(steps[3]!).getByRole("link", { name: "vCenter" })).toHaveAttribute(
      "href",
      "/admin/integrations/vcenter",
    );
    expect(steps[0]).toHaveTextContent("Le mot de passe est toujours celui de l'installation.");
    expect(steps[4]).toHaveTextContent(/\d+ environnements?, \d+ projets?, \d+ règles? de placement/);
    expect(screen.queryByText(/Not set|placement rule/)).not.toBeInTheDocument();
  });

  it("shows the system, its FIPS mode and the link to the integrations", async () => {
    renderApp("/admin/system");
    expect(await screen.findByRole("heading", { level: 1, name: "Système" })).toBeInTheDocument();
    const install = await screen.findByRole("region", { name: "Cette installation" });
    expect(install).toHaveTextContent("Mode FIPS 140-3Exclusif : autres algorithmes refusés");
    expect(within(screen.getByRole("main")).getByRole("link", { name: "Intégrations" })).toHaveAttribute(
      "href",
      "/admin/integrations",
    );
    expect(screen.getByRole("table", { name: "Processus" })).toHaveTextContent("Synchronisation de l'inventaire");
  });

  it("reads an accent's contrast with a decimal comma", { timeout: 15_000 }, async () => {
    const user = userEvent.setup();
    renderApp("/admin/branding");
    expect(await screen.findByRole("heading", { level: 1, name: "Image de marque" })).toBeInTheDocument();
    await user.type(await screen.findByLabelText("Couleur d'accent"), "#0b5394");
    expect(await screen.findByText(/^Se lit à \d+,\d{2}:1 dans le thème clair/)).toBeInTheDocument();
  });

  it("says in French why an accent doesn't read, and which colour does", { timeout: 15_000 }, async () => {
    const user = userEvent.setup();
    renderApp("/admin/branding");
    await user.type(await screen.findByLabelText("Couleur d'accent"), "#FC0");
    expect(
      await screen.findByText(
        /^#ffcc00 a un contraste de 1,\d{2}:1 dans le thème clair\s: le texte demande 4,5:1\. #[0-9a-f]{6} est la couleur plus foncée la plus proche qui convient\.$/,
      ),
    ).toBeInTheDocument();
    expect(screen.getByRole("button", { name: /^Utiliser #[0-9a-f]{6}$/ })).toBeInTheDocument();
  });

  it("filters the audit log to failed sign-ins", { timeout: 15_000 }, async () => {
    const user = userEvent.setup();
    renderApp("/admin/audit");
    const table = await screen.findByRole("table", { name: "Événements d'audit" });
    const row = (await within(table).findByText("member.set")).closest("tr")!;
    expect(row).toHaveTextContent("2 détails");
    await user.click(screen.getByRole("combobox", { name: "Quoi" }));
    await user.click(await screen.findByRole("option", { name: "Connexions échouées" }));
    const failed = (await within(table).findByText("auth.sign_in_failed")).closest("tr")!;
    expect(failed).toHaveTextContent("Inconnu");
    expect(failed).toHaveTextContent("depuis 10.20.4.17");
  });

  it("lists forwarding destinations and asks before dropping what waits", { timeout: 15_000 }, async () => {
    const user = userEvent.setup();
    renderApp("/admin/audit-forwarding");
    const region = await screen.findByRole("region", { name: "Destinations" });
    const soc = await within(region).findByRole("row", { name: /SOC sign-ins/ });
    expect(soc).toHaveTextContent("Connexions et déconnexions, Jetons d'API");
    expect(soc).toHaveTextContent("En échec");
    expect(soc).toHaveTextContent("37 en attente · 912 remis");
    await user.click(within(region).getByRole("button", { name: "Actions pour SOC sign-ins" }));
    await user.click(await screen.findByRole("menuitem", { name: "Retirer" }));
    const confirm = await screen.findByRole("alertdialog", { name: /SOC sign-ins/ });
    expect(confirm).toHaveTextContent(
      "Les 37 événements qui l'attendent sont abandonnés. Le journal d'audit les garde.",
    );
  });
});

describe("onboarding, AWX's templates and day-2 actions, in French", () => {
  async function search(user: ReturnType<typeof userEvent.setup>, text: string, found: string) {
    const box = await screen.findByRole("textbox", { name: "Rechercher" });
    await user.clear(box);
    await user.click(box);
    await user.paste(text);
    await screen.findByText(found, {}, { timeout: 5000 });
  }

  async function choose(user: ReturnType<typeof userEvent.setup>, combobox: string, option: string) {
    await user.click(screen.getByRole("combobox", { name: combobox }));
    await user.click(await screen.findByRole("option", { name: option }));
  }

  it("selects VMs, says what the filters hide, and adopts them", { timeout: 20_000 }, async () => {
    const user = userEvent.setup();
    renderApp("/admin/onboarding");
    expect(await screen.findByRole("heading", { level: 1, name: "Intégration de l'existant" })).toBeInTheDocument();
    expect(screen.getByRole("tab", { name: "VM à intégrer" })).toHaveAttribute("aria-selected", "true");
    await search(user, "app-old", "8 VM");
    const table = screen.getByRole("table", { name: "VM à intégrer" });
    await user.click(within(table).getByRole("checkbox", { name: "Sélectionner app-old01" }));
    expect(screen.getByRole("region", { name: "VM sélectionnées" })).toHaveTextContent("1 VM sélectionnée");
    await user.click(within(table).getByRole("checkbox", { name: "Sélectionner app-old02" }));
    expect(screen.getByRole("region", { name: "VM sélectionnées" })).toHaveTextContent("2 VM sélectionnées");
    await search(user, "erp-app", "12 VM · 2 sélectionnées, dont 2 non affichées");
    await user.click(screen.getByRole("button", { name: "Intégrer 2 VM…" }));

    const sheet = await screen.findByRole("dialog", { name: "Intégrer 2 VM" });
    expect(within(sheet).getByRole("list", { name: "VM à intégrer" })).toHaveTextContent(/app-old01.*app-old02/);
    await choose(user, "Projet", "Web Platform");
    await choose(user, "Environnement", "dev");
    await choose(user, "Propriétaire", "Léa Martin");
    await choose(user, "Élément de catalogue", "RHEL 9 server");
    await user.click(within(sheet).getByRole("button", { name: "Intégrer 2 VM" }));
    const adopted = await screen.findByRole("region", { name: "2 VM intégrées dans 2 déploiements" });
    expect(adopted).toHaveTextContent(/\d+ adresses?, \d+ enregistrements? DNS/);
    expect(i18n.t("admin:onboarding.adopted", { count: 1, vms: "1 VM", deployments: "1 déploiement" })).toBe(
      "1 VM intégrée dans 1 déploiement",
    );
  });

  it("changes an AWX day-2 action in its side panel", { timeout: 15_000 }, async () => {
    const user = userEvent.setup();
    renderApp("/admin/day2-actions");
    expect(await screen.findByRole("heading", { level: 1, name: "Actions de jour 2" })).toBeInTheDocument();
    expect(screen.getByRole("link", { name: "politiques de jour 2" })).toHaveAttribute(
      "href",
      "/admin/policies?tab=day2",
    );
    const table = await screen.findByRole("table", { name: "Actions de jour 2 d'AWX" });
    const row = await within(table).findByRole("row", { name: /Install monitoring agent/ });
    expect(within(row).getByText("Proposée")).toBeInTheDocument();
    expect(row).toHaveTextContent(/(Workflow|Modèle de job) n° \d+/);
    expect(within(screen.getByRole("main")).getByRole("link", { name: "Workflows personnalisés" })).toHaveAttribute(
      "href",
      "/admin/workflows",
    );
    await user.click(within(row).getByRole("button", { name: "Actions pour Install monitoring agent" }));
    await user.click(await screen.findByRole("menuitem", { name: "Modifier" }));
    const sheet = await screen.findByRole("dialog", { name: "Modifier Install monitoring agent" });
    expect(sheet).toHaveTextContent(/(Workflow AWX|Modèle de job AWX) \S+\. Son questionnaire est le formulaire/);
    expect(sheet).toHaveTextContent(
      "Proposée seulement là où cette expression CEL est vraie, par exemple resource.os_family == 'linux'. Vide : partout.",
    );
    expect(within(sheet).getByRole("group", { name: "S'exécute sur" })).toHaveTextContent("une VM à la fois");
  });

  it("opens a custom action's form designer", { timeout: 15_000 }, async () => {
    renderApp("/admin/day2-actions/wf.lb-drain/form");
    expect(
      await screen.findByRole("heading", { level: 1, name: "Formulaire de Drain" }, { timeout: 5000 }),
    ).toBeVisible();
    expect(
      within(screen.getByRole("navigation", { name: "Fil d'Ariane" })).getByRole("link", { name: "Actions de jour 2" }),
    ).toHaveAttribute("href", "/admin/day2-actions");
    await waitFor(() =>
      expect(document.body).toHaveTextContent(
        "Sur une ressource personnalisée (Load balancer), resource est son id, son type, son name, sa key et ses properties.",
      ),
    );
    expect(document.body).toHaveTextContent(
      /Une fois activé, .* lisent aussi ce sur quoi l'action s'exécute, comme resource et deployment\./,
    );
    expect(await screen.findByText(/^Pas encore conçu\s: voici le formulaire du schéma d'entrée/)).toBeInTheDocument();
    expect(i18n.t("admin:day2Form.wouldHave", { count: 1 })).toBe("Une exécution avec ces entrées aurait 1 problème.");
  });

  it("lists AWX's templates with their catalog items", { timeout: 15_000 }, async () => {
    renderApp("/admin/catalog-items?tab=awx");
    expect(await screen.findByRole("heading", { level: 1, name: "Éléments du catalogue" })).toBeInTheDocument();
    expect(screen.getByRole("tab", { name: "Modèles AWX" })).toHaveAttribute("aria-selected", "true");
    const table = await screen.findByRole("table", { name: "Modèles AWX" });
    expect(within(table).getByRole("columnheader", { name: /^Élément de catalogue/ })).toBeInTheDocument();
    expect((await within(table).findAllByText(/^(Workflow|Modèle de job) n° \d+$/)).length).toBeGreaterThan(0);
    expect(screen.getByRole("button", { name: "Synchroniser maintenant" })).toBeInTheDocument();
  });
});

describe("the vRA import, in French", () => {
  it("counts projects, drafts and moves as each language does", () => {
    expect(i18n.t("admin:vra.n.newDraft", { count: 0 })).toBe("0 nouveau brouillon");
    expect(i18n.t("admin:vra.n.newDraft", { count: 0, lng: "en" })).toBe("0 new drafts");
    expect(i18n.t("admin:vra.step.moved", { count: 2, moved: 2, total: 3 })).toBe("2 sur 3 basculés.");
    expect(i18n.t("admin:vra.step.moved", { count: 1, moved: 1, total: 3, lng: "en" })).toBe("1 of 3 moved.");
    expect(i18n.t("admin:vra.toCheck", { count: 2 })).toBe("2 à vérifier");
  });

  it("says where the move stands and maps the projects", { timeout: 15_000 }, async () => {
    renderApp("/admin/vra-import");
    expect(await screen.findByRole("heading", { level: 1, name: "Import depuis vRA" })).toBeInTheDocument();
    expect(await screen.findByText("3 projets · 5 modèles · 4 déploiements · 7 workflows vRO")).toBeInTheDocument();
    const steps = await screen.findByRole("list", { name: "Étapes" });
    expect(within(steps).getAllByRole("listitem")[0]).toHaveTextContent(/Lire vRA\s: terminée/);
    expect(await within(steps).findByText(/sans projet rvav du même nom/)).toBeInTheDocument();
    expect(steps.querySelector('[aria-current="step"]')).toHaveTextContent(
      /Faire correspondre les projets\s: en cours/,
    );
    expect(await screen.findByRole("tab", { name: "Projets", selected: true })).toBeInTheDocument();
    const table = await screen.findByRole("table", { name: "Projets vRA" });
    await waitFor(() =>
      expect(within(table).getByRole("combobox", { name: "Projet rvav pour Web Platform" })).toHaveTextContent(
        "Par nom : Web Platform",
      ),
    );
    const row = within(table).getByText("vra-prj-web").closest("tr") as HTMLTableRowElement;
    expect(row.cells[2]).toHaveTextContent("Répartition");
    expect(row.cells[4]).toHaveTextContent("Tous les membres");
  });

  it("lists what the import makes, then imports it", { timeout: 20_000 }, async () => {
    const user = userEvent.setup();
    renderApp("/admin/vra-import?tab=items");
    const templates = await screen.findByRole("table", { name: "Modèles" });
    const win = within(templates).getAllByText("win2022")[0]!.closest("tr") as HTMLTableRowElement;
    expect(win).toHaveTextContent(/win2022 · Windows · 1 contrainteCréation/);
    await user.click(within(templates).getByRole("button", { name: /^Notes sur win2022\s: 1 non repris$/ }));
    expect(await screen.findByRole("dialog", { name: "Notes sur win2022" })).toHaveTextContent(
      /Ce que l'import garde, change ou laisse de côté/,
    );
    await user.keyboard("{Escape}");

    await user.click(screen.getByRole("button", { name: "Importer…" }));
    const sheet = await screen.findByRole("dialog", { name: "Importer depuis vRA" });
    expect(sheet).toHaveTextContent(/2 groupes de propriétés à créer depuis ceux de vRA\s;/);
    expect(sheet).toHaveTextContent(/nouveaux? brouillons?, rien de publié\s; \d+ partages à ajouter/);
    await user.click(within(sheet).getByRole("button", { name: "Importer" }));
    const result = (await screen.findAllByRole("region", { name: "Importé" })).at(-1);
    expect(result).toHaveTextContent(/Groupes de propriétés créés\s: corpInputs, dnsSettings/);
    expect(result).toHaveTextContent(/Projets qui reçoivent les propriétés personnalisées de vRA\s: Web Platform/);
  });

  it("adopts a project's deployments and marks it moved", { timeout: 20_000 }, async () => {
    const user = userEvent.setup();
    renderApp("/admin/vra-import?tab=cutover");
    expect(await screen.findByText("0 sur 3 projets basculés")).toBeInTheDocument();
    await user.click(await screen.findByRole("button", { name: "Intégrer 2 déploiements (Web Platform)" }));
    expect(await screen.findByText("2 déploiements de Web Platform intégrés")).toBeInTheDocument();
    await user.click(await screen.findByRole("button", { name: "Marquer comme basculé… (Web Platform)" }));
    const sheet = await screen.findByRole("dialog", { name: "Basculer Web Platform vers rvav" });
    await user.type(within(sheet).getByRole("textbox", { name: "Note" }), "Week-end de bascule");
    await user.click(within(sheet).getByRole("button", { name: "Marquer comme basculé" }));
    expect(await screen.findByText("Web Platform basculé vers rvav")).toBeInTheDocument();
    expect(await screen.findByText("1 sur 3 projets basculés")).toBeInTheDocument();
    await user.click(await screen.findByRole("button", { name: "Non basculé… (Web Platform)" }));
    const confirm = await screen.findByRole("alertdialog", { name: /^Marquer Web Platform comme non basculé\s\?$/ });
    expect(confirm).toHaveTextContent(/supprimer dans vRA un déploiement intégré supprime ses VM/);
  });
});
