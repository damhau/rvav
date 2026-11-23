import { screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { afterEach, beforeEach, describe, expect, it } from "vitest";
import { i18n } from "@/lib/i18n";
import { db, resetDb } from "@/mocks/seed";
import { renderApp } from "@/test/render";
import { changesText } from "./runs";
import { targetText } from "./SubscriptionsPage";

type User = ReturnType<typeof userEvent.setup>;

async function openMenu(user: User, name: string) {
  await user.click(await screen.findByRole("button", { name: `Actions pour ${name}` }));
}

beforeEach(async () => {
  db.me.locale = "fr";
  await i18n.changeLanguage("fr");
});

afterEach(() => resetDb());

describe("subscriptions' helpers, in French", () => {
  it("names a target and what a blocking answer changed on its VM", () => {
    expect(targetText({ webhook: { url: "https://hooks.corp.example/x", secret: "s" } })).toBe(
      "Webhook vers hooks.corp.example",
    );
    expect(targetText({ awx: { job_template: "cmdb-check" } })).toBe("Modèle AWX cmdb-check");
    expect(
      changesText({
        name: "web-dev-app742",
        placement: { datastore: "ds-gva-dev-02" },
        networks: [{ index: 0, network: "vlan-31", address: "10.31.10.57" }],
      }),
    ).toBe(
      "renommée web-dev-app742\u00a0; déplacée vers le datastore ds-gva-dev-02\u00a0; adaptateur 0\u00a0: réseau vlan-31, adresse 10.31.10.57",
    );
  });
});

// Testing Library folds a no-break space into a space before it compares text.
describe("subscriptions, in French", () => {
  it("groups the topics and says where a blocking one may stop a request", { timeout: 15_000 }, async () => {
    const user = userEvent.setup();
    renderApp("/admin/subscriptions");
    expect(await screen.findByRole("heading", { level: 1, name: "Abonnements" })).toBeInTheDocument();
    const table = await screen.findByRole("table", { name: "Abonnements" });
    expect(await within(table).findByRole("row", { name: /CMDB pre-check/ })).toHaveTextContent("Bloquant");

    await user.click(screen.getByRole("button", { name: "Nouvel abonnement" }));
    const sheet = await screen.findByRole("dialog", { name: "Nouvel abonnement" });
    await user.click(within(sheet).getByRole("combobox", { name: "Sujet" }));
    const machines = screen.getByRole("group", { name: "Les VM d'une demande, un événement chacune" });
    await user.click(within(machines).getByRole("option", { name: "compute.provision.pre" }));
    expect(within(sheet).getByRole("checkbox", { name: /Bloquant/ })).toBeDisabled();
    expect(
      within(sheet).getByText(
        "(seulement sur provisioning.pre, compute.allocation.pre, network.configure, day2.pre et delete.pre)",
      ),
    ).toBeInTheDocument();
    expect(within(sheet).getByText("Un exemple d'événement compute.provision.pre")).toBeInTheDocument();
  });

  it("shows a subscription's recovery runs and what they changed", { timeout: 15_000 }, async () => {
    const user = userEvent.setup();
    renderApp("/admin/subscriptions/sub_cmdb_naming");
    await screen.findByRole("heading", { level: 1, name: "CMDB naming" });
    const crumbs = screen.getByRole("navigation", { name: "Fil d'Ariane" });
    expect(within(crumbs).getByRole("link", { name: "Abonnements" })).toBeInTheDocument();
    expect(screen.getByText("Webhook vers hooks.corp.example")).toBeInTheDocument();
    expect(
      await screen.findByText(
        "le nom de la VM (machine.name) et son cluster, son pool de ressources, son datastore et son dossier (machine.placement)",
      ),
    ).toBeInTheDocument();

    const runs = await screen.findByRole("table", { name: "Exécutions" });
    expect((await within(runs).findAllByText("Reprise")).length).toBe(2);
    const opens = await within(runs).findAllByRole("button", { name: /^Ouvrir l'exécution lancée / });
    await user.click(opens[0]!);
    let dialog = await screen.findByRole("dialog", { name: "Exécution" });
    expect(
      await within(dialog).findByText("renommée web-dev-app742 ; déplacée vers le datastore ds-gva-dev-02"),
    ).toBeInTheDocument();
    await user.keyboard("{Escape}");
    await waitFor(() => expect(screen.queryByRole("dialog")).toBeNull());

    await user.click(opens.at(-1)!);
    dialog = await screen.findByRole("dialog", { name: "Exécution" });
    expect(await within(dialog).findByText("Sa cible de reprise, après l'échec de l'abonnement")).toBeInTheDocument();
    expect(within(dialog).getByText("Aucune : le réglage de l'abonnement en cas d'échec a décidé")).toBeInTheDocument();
  });

  it("filters a subscription's runs by a status said in French", { timeout: 15_000 }, async () => {
    const user = userEvent.setup();
    renderApp("/admin/subscriptions/sub_servicenow");
    await screen.findByRole("heading", { level: 1, name: "ServiceNow change record" });
    const runs = await screen.findByRole("table", { name: "Exécutions" });
    await within(runs).findAllByRole("button", { name: /^Ouvrir l'exécution lancée / });
    await user.click(screen.getByRole("combobox", { name: "Statut" }));
    await user.click(await screen.findByRole("option", { name: "A arrêté la demande" }));
    expect(await screen.findByText("Aucune exécution au statut A arrêté la demande")).toBeInTheDocument();
    await user.click(screen.getAllByRole("button", { name: "Effacer les filtres" }).at(-1)!);
    expect(await screen.findByRole("table", { name: "Exécutions" })).toBeInTheDocument();
    expect(screen.getByRole("button", { name: "Modifier l'abonnement" })).toBeInTheDocument();
  });
});

describe("resource types and custom workflows, in French", () => {
  it("says what makes a type and why one can't be removed", { timeout: 15_000 }, async () => {
    const user = userEvent.setup();
    renderApp("/admin/resource-types");
    expect(await screen.findByRole("heading", { level: 1, name: "Types de ressources" })).toBeInTheDocument();
    const table = await screen.findByRole("table", { name: "Types de ressources" });
    const lb = await within(table).findByRole("row", { name: /Load balancer/ });
    expect(lb).toHaveTextContent("AWX · create-load-balancer");
    expect(within(table).getByRole("row", { name: /DNS alias/ })).toHaveTextContent("Désactivé");
    await openMenu(user, "Load balancer");
    expect(
      await screen.findByRole("menuitem", { name: "Retirer\u00a0: des déploiements ont 1 ressource" }),
    ).toHaveAttribute("data-disabled");
    await user.keyboard("{Escape}");
    await openMenu(user, "Load balancer");
    await user.click(await screen.findByRole("menuitem", { name: "Modifier" }));
    const sheet = await screen.findByRole("dialog", { name: "Modifier Load balancer" });
    expect(within(sheet).getByRole("group", { name: "Le crée" })).toBeInTheDocument();
    expect(within(sheet).getByRole("group", { name: "Le met à jour (facultatif)" })).toBeInTheDocument();
    expect(within(sheet).getByRole("button", { name: "Enregistrer le type de ressource" })).toBeInTheDocument();
  });

  it("says why a running workflow can't be removed, and what removing one stops", { timeout: 15_000 }, async () => {
    const user = userEvent.setup();
    renderApp("/admin/workflows");
    expect(await screen.findByRole("heading", { level: 1, name: "Workflows personnalisés" })).toBeInTheDocument();
    await openMenu(user, "Decommission an application");
    expect(
      await screen.findByRole("menuitem", { name: "Retirer\u00a0: des exécutions ne sont pas terminées" }),
    ).toHaveAttribute("aria-disabled", "true");
    await user.keyboard("{Escape}");

    await openMenu(user, "Register in monitoring");
    await user.click(await screen.findByRole("menuitem", { name: "Retirer" }));
    const confirm = await screen.findByRole("alertdialog", { name: "Retirer Register in monitoring\u00a0?" });
    expect(confirm).toHaveTextContent("Il n'est plus proposé comme étape composite.");
    await user.click(within(confirm).getByRole("button", { name: "Retirer le workflow" }));
    expect(await screen.findByText("Register in monitoring retiré")).toBeInTheDocument();
  });
});
