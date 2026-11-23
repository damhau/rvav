import { render, screen, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { afterEach, beforeEach, describe, expect, it } from "vitest";
import type { components } from "@/lib/api/schema";
import { i18n } from "@/lib/i18n";
import { db, resetDb } from "@/mocks/seed";
import { renderApp } from "@/test/render";
import { LeaseSummary } from "./LeasePolicies";
import { askedText } from "./shared";

type S = components["schemas"];

beforeEach(async () => {
  await i18n.changeLanguage("fr");
});
afterEach(() => localStorage.removeItem("rvav.mock.as"));

const lease = (days: number, lifetime?: number) =>
  ({ default_days: days, max_days: 30, max_lifetime_days: lifetime, enforcement: "hard" }) as S["LeasePolicy"];

describe("policies, in French", () => {
  it("counts a lease's days as French does", () => {
    const { container, rerender } = render(<LeaseSummary lease={lease(1, 1)} />);
    expect(container).toHaveTextContent("1 jour, jusqu'à 30Vit 1 jour au plus");
    rerender(<LeaseSummary lease={lease(14, 90)} />);
    expect(container).toHaveTextContent("14 jours, jusqu'à 30Vit 90 jours au plus");
  });

  it("asks one of a level's roles, or the requester's manager", () => {
    const roles = [
      { key: "owner", name: "Propriétaire" },
      { key: "approver", name: "Approbateur" },
      { key: "auditor", name: "Auditeur" },
    ] as S["RoleRef"][];
    const level = { name: "Niveau 1", roles: ["owner", "approver", "auditor"], manager: true } as S["ApprovalLevel"];
    expect(askedText(level, roles, [])).toBe("Propriétaire, Approbateur ou Auditeur, le responsable du demandeur");
  });

  it("names a level's approvers to a project admin", { timeout: 15_000 }, async () => {
    localStorage.setItem("rvav.mock.as", "project-admin");
    resetDb();
    db.me.locale = "fr";
    renderApp("/admin/policies");
    const region = await screen.findByRole("region", { name: "Politiques d'approbation" });
    const production = await within(region).findByRole("row", { name: /Production VMs/ });
    expect(production).toHaveTextContent("rvav-infra-leads, Marc Keller");
    expect(production).not.toHaveTextContent("usr_mkeller");
  });

  it("shows the policies page with its tabs, and a day-2 policy's menu", { timeout: 15_000 }, async () => {
    db.me.locale = "fr";
    const user = userEvent.setup();
    renderApp("/admin/policies");
    expect(await screen.findByRole("heading", { level: 1, name: "Politiques" })).toBeInTheDocument();
    const tabs = screen.getByRole("tablist", { name: "Types de politique" });
    expect(
      within(tabs)
        .getAllByRole("tab")
        .map((t) => t.textContent),
    ).toEqual(expect.arrayContaining(["Approbation", "Jour 2", "Bail", "Limites", "Ce qui s'applique ici"]));
    expect(await screen.findByRole("region", { name: "Politiques d'approbation" })).toBeInTheDocument();

    await user.click(within(tabs).getByRole("tab", { name: "Jour 2" }));
    const region = await screen.findByRole("region", { name: "Politiques de jour 2" });
    expect(await within(region).findByRole("row", { name: /Members operate their VMs/ })).toHaveTextContent(
      "Propriétaire, Membre ou Administrateur de projet",
    );
    await user.click((await within(region).findAllByRole("button", { name: /^Actions pour / }))[0]!);
    expect(await screen.findByRole("menuitem", { name: "Désactiver" })).toBeInTheDocument();
  });
});
