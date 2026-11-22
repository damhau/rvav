import { screen, within } from "@testing-library/react";
import userEvent, { type UserEvent } from "@testing-library/user-event";
import { beforeEach, describe, expect, it } from "vitest";
import { i18n } from "@/lib/i18n";
import { db } from "@/mocks/seed";
import { renderApp } from "@/test/render";
import { countsText, filterText, type Plan, scheduleText, targetText } from "./plans";
import { emptyTarget, inputProblems } from "./target";

const plan: Plan = {
  id: "obp-1",
  name: "ERP servers",
  description: "",
  filter: { tag: "app/erp" },
  target: { project_id: "p1", environment: "prd", component: "web" },
  matching: 12,
  running: false,
  created_at: "2026-11-01T08:00:00Z",
  updated_at: "2026-11-01T08:00:00Z",
};
const project = () => "Web Platform";

async function choose(user: UserEvent, combobox: string, option: string) {
  await user.click(screen.getByRole("combobox", { name: combobox }));
  await user.click(await screen.findByRole("option", { name: option }));
}

beforeEach(async () => {
  db.me.locale = "fr";
  await i18n.changeLanguage("fr");
});

describe("onboarding plans' helpers, in French", () => {
  it("says which VMs a plan adopts, where they go and when it runs", () => {
    expect(filterText({ q: "erp", folder: "/GVA/app", tag: "app/erp", power_state: "off" })).toBe(
      "nom ou adresse erp, dans /GVA/app, avec le tag app/erp, éteintes",
    );
    expect(filterText({})).toBe("Toutes les VM");
    expect(targetText(plan, project)).toBe("Chacune dans son propre déploiement, dans Web Platform prd, comme VM web");
    expect(targetText({ ...plan, target: { ...plan.target, deployment_id: "d1" } }, project)).toBe(
      "Dans un déploiement supprimé depuis, comme VM web",
    );
    expect(scheduleText(plan)).toBe("Quand on le lance à la main");
    expect(scheduleText({ ...plan, every_hours: 1 })).toBe("Toutes les heures");
    expect(scheduleText({ ...plan, every_hours: 24 })).toBe("Toutes les 24 heures");
    expect(
      countsText({
        id: "r1",
        plan_id: "obp-1",
        plan_name: "ERP servers",
        trigger: "manual",
        status: "partial",
        started_at: "2026-11-01T08:00:00Z",
        adopted: 10,
        skipped: 1,
        failed: 1,
      }),
    ).toBe("Intégrées\u00a0: 10, ignorées\u00a0: 1, en échec\u00a0: 1");
    expect(inputProblems({ ...emptyTarget, inputs: [{ key: "", value: "erp" }] })).toEqual({
      0: "Donnez un nom au champ.",
    });
  });
});

describe("an onboarding plan, in French", () => {
  it("saves a plan, runs it and counts what it adopted", { timeout: 20_000 }, async () => {
    const user = userEvent.setup();
    renderApp("/admin/onboarding?tab=plans");
    expect(await screen.findByText("Aucun plan pour l'instant")).toBeInTheDocument();
    await user.click(await screen.findByRole("button", { name: "Nouveau plan" }));
    const sheet = await screen.findByRole("dialog", { name: "Nouveau plan" });
    expect(
      within(sheet).getByText(
        "Donnez au moins un critère : un plan n'intègre jamais toutes les VM que rvav ne gère pas.",
      ),
    ).toBeInTheDocument();
    await user.type(within(sheet).getByRole("textbox", { name: "Nom" }), "ERP servers");
    await user.click(within(sheet).getByRole("textbox", { name: /^Tag/ }));
    await user.paste("app/erp");
    expect(await within(sheet).findByText("12 VM que rvav ne gère pas correspondent maintenant.")).toBeInTheDocument();
    await choose(user, "Projet", "Web Platform");
    await choose(user, "Où elles vont", "Toutes dans un nouveau déploiement");
    await choose(user, "Environnement", "prd");
    await choose(user, "Propriétaire", "Thomas Nguyen");
    await choose(user, "Élément de catalogue", "RHEL 9 server");
    await user.type(within(sheet).getByRole("textbox", { name: "Nom du déploiement" }), "erp-prd");
    await user.click(within(sheet).getByRole("button", { name: "Enregistrer le plan" }));

    expect(await screen.findByRole("heading", { name: "ERP servers", level: 1 })).toBeInTheDocument();
    const crumbs = screen.getByRole("navigation", { name: "Fil d'Ariane" });
    expect(within(crumbs).getByRole("link", { name: "Intégration de l'existant" })).toBeInTheDocument();
    expect(screen.getByText("12 VM qu'il trouve et qu'aucun déploiement n'a")).toBeInTheDocument();
    expect(screen.getByText("Dans erp-prd, un seul déploiement, dans Web Platform prd")).toBeInTheDocument();
    expect(await screen.findByText("Jamais lancé")).toBeInTheDocument();
    await user.click(screen.getByRole("button", { name: "Lancer maintenant" }));
    const runs = await screen.findByRole("table", { name: "Exécutions" });
    expect(await within(runs).findByText("Réussie", {}, { timeout: 8000 })).toBeInTheDocument();
    expect(within(runs).getByText("Intégrées : 12, ignorées : 0, en échec : 0")).toBeInTheDocument();
    await user.click(within(runs).getAllByRole("button", { name: /^Ouvrir l'exécution lancée / })[0]!);
    const run = await screen.findByRole("dialog", { name: /^Exécution du / });
    const vms = await within(run).findByRole("table", { name: "VM de l'exécution" });
    expect(within(vms).getAllByText("Intégrée")).toHaveLength(12);
  });
});
