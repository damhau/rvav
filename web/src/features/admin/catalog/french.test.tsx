import { fireEvent, screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { HttpResponse, http } from "msw";
import { beforeEach, describe, expect, it } from "vitest";
import { i18n } from "@/lib/i18n";
import { server } from "@/mocks/node";
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

describe("an item's versions and definition, in French", () => {
  it("counts the problems a publish is refused for, and shows them on their lines", { timeout: 15_000 }, async () => {
    const user = userEvent.setup();
    renderApp("/admin/catalog-items/rhel9-server");
    await user.click(await screen.findByRole("button", { name: "Nouveau brouillon" }));
    const errors = [
      { field: "/definition/blueprint/vm/cpu", code: "invalid", message: "Not that many CPUs." },
      { field: "/definition/ui_schema/title", code: "required", message: "Give the form a title." },
    ];
    server.use(
      http.post("*/api/v1/admin/catalog/items/:id/versions/:vid/publish", () =>
        HttpResponse.json(
          { type: "about:blank", title: "Some inputs need attention", status: 422, request_id: "req", errors },
          { status: 422, headers: { "Content-Type": "application/problem+json" } },
        ),
      ),
    );
    await user.click(await screen.findByRole("button", { name: "Publier 3.3" }));
    expect(await screen.findByText("3.3 non publiée")).toBeInTheDocument();
    expect(screen.getByText("2 problèmes dans sa définition, signalés sur leurs lignes.")).toBeInTheDocument();
    expect(await screen.findByRole("tab", { name: "Définition" })).toHaveAttribute("aria-selected", "true");
    const list = await screen.findByRole("list", { name: "Problèmes dans la définition" });
    expect(within(list).getAllByRole("listitem")[0]).toHaveTextContent(/^Ligne \d+ : Not that many CPUs\.$/);
  });

  it("names a version's state and what publishing beside it keeps", { timeout: 15_000 }, async () => {
    const user = userEvent.setup();
    renderApp("/admin/catalog-items/rhel9-server");
    await user.click(await screen.findByRole("button", { name: "Nouveau brouillon" }));
    await user.click(await screen.findByRole("button", { name: "Actions pour la version 3.3" }));
    await user.click(await screen.findByRole("menuitem", { name: "Publier 3.3, en gardant 3.2 demandable" }));
    expect(await screen.findByText("3.3 publiée à côté des autres")).toBeInTheDocument();
    expect(await screen.findByText("3.3, 3.2 publiées")).toBeInTheDocument();
    expect(within(screen.getByRole("region", { name: "Versions" })).getByText("Par défaut")).toBeInTheDocument();
  });
});

describe("property groups and the form library, in French", () => {
  it("shows the code a blueprint names a group with, untranslated", { timeout: 15_000 }, async () => {
    renderApp("/admin/property-groups");
    expect(await screen.findByRole("heading", { level: 1, name: "Groupes de propriétés" })).toBeInTheDocument();
    expect(screen.getByText("property_groups: [{key: network, version: 2}]")).toBeInTheDocument();
    expect(screen.getByText("propgroup.network.<name>")).toBeInTheDocument();
  });

  it("counts the items using an entry, and names their versions' states", { timeout: 15_000 }, async () => {
    const user = userEvent.setup();
    const used = {
      key: "cmdbApps",
      name: "CMDB applications",
      description: "",
      use: "source",
      kind: "rest",
      params: { url: "https://cmdb.corp.example/api/apps", items: "$[*]", value: "$.id" },
      search: false,
      parameters: [],
      version: 2,
      versions: [1, 2],
      used_by: [
        {
          item_id: "itm_1",
          slug: "rhel9-server",
          name: "RHEL 9 server",
          version_id: "ver_1",
          number: "1.4",
          state: "published",
          entry_version: 1,
        },
      ],
      updated_at: new Date().toISOString(),
    };
    server.use(http.get("*/api/v1/admin/form-library", () => HttpResponse.json({ items: [used] })));
    renderApp("/admin/form-library");
    const row = await screen.findByRole("row", { name: /CMDB applications/ });
    expect(row).toHaveTextContent("Source de valeurs");
    await user.click(within(row).getByRole("button", { name: "Actions pour CMDB applications" }));
    expect(await screen.findByRole("menuitem", { name: "Supprimer\u00a0: 1 élément l'utilise" })).toHaveAttribute(
      "aria-disabled",
      "true",
    );
    await user.click(screen.getByRole("menuitem", { name: "Modifier ou tester" }));
    const sheet = await screen.findByRole("dialog", { name: "Modifier CMDB applications" });
    expect(sheet).toHaveTextContent("version 1.4, publiée");
    expect(sheet).toHaveTextContent("utilise la version 1");
  });
});

describe("the form designer, in French", () => {
  async function openDesigner() {
    const user = userEvent.setup();
    renderApp("/admin/catalog-items/itm_rhel9");
    await user.click(await screen.findByRole("button", { name: "Nouveau brouillon" }));
    await user.click(await screen.findByRole("tab", { name: "Concepteur de formulaire" }));
    const fields = await screen.findByRole("region", { name: "Champs" }, { timeout: 5000 });
    const preview = screen.getByRole("region", { name: "Aperçu" });
    await within(preview).findByRole("group", { name: "Basics" }, { timeout: 5000 });
    return { user, fields, preview };
  }

  it("names a layout's sections, pages or tabs, each with its gender", { timeout: 20_000 }, async () => {
    const { user, fields, preview } = await openDesigner();
    expect(screen.getByText(/^Brouillon [\d.]+ : enregistré$/)).toBeInTheDocument();
    expect(
      await within(preview).findByText(/^Une demande avec ces entrées (passerait|aurait \d+ problèmes?)\.$/),
    ).toBeInTheDocument();
    expect(within(fields).getByRole("button", { name: "Ajouter une section" })).toBeInTheDocument();
    await user.click(within(fields).getByRole("combobox", { name: "Disposition" }));
    await user.click(screen.getByRole("option", { name: "Onglets" }));
    expect(within(fields).getByRole("combobox", { name: "Disposition" })).toHaveAccessibleDescription(
      "Un onglet par section.",
    );
    expect(within(fields).getByRole("group", { name: "Onglet Basics" })).toBeInTheDocument();
    expect(within(fields).getByRole("button", { name: "Ajouter un onglet" })).toBeInTheDocument();
    await user.click(within(fields).getByRole("button", { name: "Monter l'onglet Size" }));
    expect(await screen.findByText("Onglet Size déplacé en position 1 sur 4.")).toBeInTheDocument();
  });

  it("labels a new field in French, and names its properties", { timeout: 20_000 }, async () => {
    const { user, fields } = await openDesigner();
    await user.click(within(fields).getByRole("combobox", { name: "Ajouter un champ" }));
    await user.click(screen.getByRole("option", { name: "Interrupteur" }));
    const properties = await screen.findByRole("region", { name: "Propriétés" });
    expect(within(properties).getByLabelText("Libellé")).toHaveValue("Interrupteur");
    expect(within(properties).getByRole("combobox", { name: "Type de champ" })).toBeInTheDocument();
    expect(within(properties).getByRole("group", { name: "Règles" })).toHaveTextContent(
      "Règles (CEL sur inputs, environment, project et user)",
    );
    expect(within(properties).getByRole("button", { name: "Retirer Interrupteur" })).toBeInTheDocument();
  });
});

describe("the composite designer, in French", () => {
  // Nodes are clicked with fireEvent: user-event's mousedown reaches d3-zoom, which jsdom can't serve.
  const node = (name: RegExp) =>
    within(screen.getByRole("application"))
      .getAllByRole("button")
      .find((b) => b.classList.contains("react-flow__node") && name.test(b.getAttribute("aria-label") ?? ""))!;

  it("names a step's details and the way to add one, the server's words kept", { timeout: 30_000 }, async () => {
    const user = userEvent.setup();
    renderApp("/admin/catalog-items/postgres-cluster");
    await user.click(await screen.findByRole("button", { name: "Nouveau brouillon" }));
    await user.click(await screen.findByRole("tab", { name: "Définition" }));
    await waitFor(() => expect(node(/^Élément Database nodes\./)).toBeDefined(), { timeout: 10_000 });
    expect(screen.getByRole("button", { name: "Défaire" })).toBeDisabled();
    expect(screen.getByRole("button", { name: "Refaire" })).toBeDisabled();

    fireEvent.click(node(/^Élément Database nodes\./));
    const details = await screen.findByRole("region", { name: "Sélection\u00a0: Database nodes" });
    expect(within(details).getByRole("combobox", { name: "Exécute" })).toHaveAccessibleDescription(
      "Un élément VM\u00a0: ses VM sont planifiées avec la demande, à partir des entrées du formulaire.",
    );
    expect(within(details).getByRole("button", { name: "Retirer l'étape" })).toBeInTheDocument();
    expect(within(details).getByRole("textbox", { name: "Clé" })).toHaveAccessibleDescription(
      "Les étapes suivantes lisent ses sorties par steps.nodes.",
    );

    await user.click(screen.getByRole("button", { name: "Ajouter une étape" }));
    const dialog = await screen.findByRole("dialog", { name: "Ajouter une étape" });
    expect(within(dialog).getByRole("radio", { name: "Après Database nodes" })).toBeChecked();
    expect(within(dialog).getByRole("radio", { name: "À la fin" })).toBeInTheDocument();
    await waitFor(() => expect(within(dialog).getByRole("combobox", { name: "Ce qu'elle exécute" })).toBeEnabled());
    await user.click(within(dialog).getByRole("combobox", { name: "Ce qu'elle exécute" }));
    expect(
      await screen.findByRole("option", { name: "Une attente d'un signal ou d'un événement" }),
    ).toBeInTheDocument();
    await user.click(screen.getByRole("option", { name: "Un appel REST" }));
    expect(within(dialog).getByRole("textbox", { name: "Clé" })).toHaveValue("rest_call");
    await user.click(within(dialog).getByRole("button", { name: "Ajouter l'étape" }));

    const call = await screen.findByRole("region", { name: "Sélection\u00a0: rest_call" });
    expect(within(call).getByRole("group", { name: "Appel REST" })).toHaveTextContent("Elle peut lire inputs.");
    expect(within(call).getByRole("group", { name: "Chemin de rattrapage" })).toHaveTextContent(
      "Elles lisent error.step et error.message.",
    );
    await waitFor(() => expect(screen.getByRole("button", { name: "Défaire" })).toBeEnabled());
  });
});
