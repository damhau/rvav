import { screen, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { beforeEach, describe, expect, it } from "vitest";
import { i18n } from "@/lib/i18n";
import { db } from "@/mocks/seed";
import { renderApp } from "@/test/render";
import { deploymentCount } from "./DeploymentsPage";

const idOf = (name: string) => db.deployments.find((d) => d.name === name)!.id;
const value = (box: HTMLElement, term: string) => within(box).getByText(term, { selector: "dt" }).nextElementSibling;

describe("the deployments count", () => {
  it("agrees with each number in English", () => {
    expect(deploymentCount(1, 1, 1, 1, false)).toBe("1 deployment · 1 VM, 1 powered on · 1 lease ends within 7 days");
    expect(deploymentCount(3, 5, 2, 0, true)).toBe("3 shown · 5 VMs, 2 powered on");
  });

  it("agrees with each number in French, VM staying invariable", async () => {
    await i18n.changeLanguage("fr");
    expect(deploymentCount(1, 1, 1, 0, false)).toBe("1 déploiement · 1 VM, 1 allumée");
    expect(deploymentCount(3, 5, 2, 2, true)).toBe("3 affichés · 5 VM, 2 allumées · 2 baux se terminent d'ici 7 jours");
  });
});

describe("the deployments, in French", () => {
  beforeEach(() => {
    db.me.locale = "fr";
  });

  it("lists them with French headings and counts", async () => {
    renderApp("/deployments?layout=table");
    expect(await screen.findByRole("heading", { name: "Déploiements", level: 1 }, { timeout: 5000 })).toBeVisible();
    expect(await screen.findByText(/^\d+ déploiements · \d+ VM, \d+ allumées/)).toBeInTheDocument();
    expect(screen.getByLabelText("Les miens seulement")).toBeInTheDocument();
  });

  it(
    "names a deployment's tabs, panels and actions in French, the server's reasons as sent",
    { timeout: 15_000 },
    async () => {
      const user = userEvent.setup();
      renderApp(`/deployments/${idOf("web-dev-api")}`);
      expect(await screen.findByRole("heading", { level: 1, name: "web-dev-api" }, { timeout: 5000 })).toBeVisible();
      // Testing Library reads a no-break space as a space.
      expect(await screen.findByText("Propriétaire : Thomas Nguyen")).toBeInTheDocument();
      for (const tab of ["Vue d'ensemble", "Topologie", "Historique", "Champs"])
        expect(screen.getByRole("tab", { name: tab })).toBeInTheDocument();
      const lease = screen.getByRole("region", { name: "Bail" });
      expect(lease).toHaveTextContent("1 prolongation sur 2 utilisée ; jusqu'à 90 jours sans approbation.");
      expect(within(lease).getByRole("button", { name: "Prolonger le bail" })).toBeInTheDocument();

      await user.click(screen.getByRole("button", { name: "Actions" }));
      const menu = await screen.findByRole("menu");
      for (const group of ["Alimentation", "Snapshots", "Cycle de vie"])
        expect(within(menu).getByText(group)).toBeVisible();
      expect(within(menu).getByRole("menuitem", { name: /^Power on/ })).toHaveTextContent(
        "The VM is already powered on.",
      );
      await user.keyboard("{Escape}");

      await user.click(screen.getByRole("tab", { name: "Historique" }));
      const history = await screen.findByRole("region", { name: "Historique" });
      expect(
        await within(history).findByText(/^Aucune action de jour 2 n'a encore été exécutée dessus\./),
      ).toBeVisible();
      expect(within(history).getByRole("link", { name: /^REQ-\d+$/ })).toBeInTheDocument();
    },
  );

  it("shows a VM's properties in French, sizes in Go", { timeout: 15_000 }, async () => {
    const user = userEvent.setup();
    renderApp(`/deployments/${idOf("web-prd-front")}`);
    const vms = await screen.findByRole("region", { name: "Machines virtuelles" }, { timeout: 5000 });
    expect(vms).toHaveTextContent(/\d+ vCPU · \d+ Go · \d+ Go/);
    await user.click(await within(vms).findByRole("button", { name: "Propriétés de web-prd-front002" }));
    const dialog = await screen.findByRole("dialog", { name: "web-prd-front002" });
    expect(value(dialog, "Alimentation")).toHaveTextContent("Allumée");
    expect(value(dialog, "Taille")).toHaveTextContent(/^\d+ vCPU · \d+ Go de mémoire$/);
    expect(within(dialog).getByRole("region", { name: "Cartes réseau" })).toBeInTheDocument();
    expect(within(dialog).getByRole("link", { name: /dans le client vSphere/ })).toBeInTheDocument();
  });
});
