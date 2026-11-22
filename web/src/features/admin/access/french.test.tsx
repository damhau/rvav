import { screen, within } from "@testing-library/react";
import { beforeEach, describe, expect, it } from "vitest";
import type { components } from "@/lib/api/schema";
import { i18n } from "@/lib/i18n";
import { permissionsAnswer } from "@/mocks/roles";
import { db } from "@/mocks/seed";
import { renderApp } from "@/test/render";
import { allows, scopeOf } from "./permissions";
import { cantDelete } from "./RolePage";

type Role = components["schemas"]["Role"];

beforeEach(async () => {
  await i18n.changeLanguage("fr");
});

describe("roles and access, in French", () => {
  it("joins actions and permissions as French lists do", () => {
    const c = permissionsAnswer();
    expect(allows(c, ["day2.power:power_off", "day2.power:power_on"]).map((s) => s.text)).toContain(
      "Exécuter les actions de jour 2 Éteindre et Allumer du groupe alimentation.",
    );
    expect(allows(c, ["day2.snapshots"]).map((s) => s.text)).toContain(
      "Exécuter toutes les actions de jour 2 du groupe snapshots, y compris celles ajoutées plus tard.",
    );
    expect(scopeOf(c, ["policies.read"]).why).toBe(
      "policies.read est une permission pour l'ensemble de rvav seulement.",
    );
    expect(scopeOf(c, ["policies.read", "access.read"]).why).toBe(
      "access.read et policies.read sont des permissions pour l'ensemble de rvav seulement.",
    );
  });

  it("says why a role can't be deleted", () => {
    const role = (holders: number, policies: string[] = []) => ({ holders, policies }) as unknown as Role;
    expect(cantDelete(role(1))).toBe("Supprimer : donné 1 fois");
    expect(cantDelete(role(3))).toBe("Supprimer : donné 3 fois");
    expect(cantDelete(role(0, ["p"]))).toBe("Supprimer : des politiques de jour 2 le nomment");
  });

  it("shows the roles page with its tabs and columns", { timeout: 15_000 }, async () => {
    db.me.locale = "fr";
    renderApp("/admin/roles");
    expect(await screen.findByRole("heading", { level: 1, name: "Rôles" })).toBeInTheDocument();
    const tabs = screen.getByRole("tablist", { name: "Sections des rôles" });
    expect(
      within(tabs)
        .getAllByRole("tab")
        .map((t) => t.textContent),
    ).toEqual(["Rôles", "Pour l'ensemble de rvav", "Ce que quelqu'un peut faire"]);
    const table = await screen.findByRole("table", { name: "Rôles" });
    expect(await within(table).findAllByText("Prédéfini")).not.toHaveLength(0);
    expect(screen.getByRole("link", { name: "Nouveau rôle" })).toBeInTheDocument();
    expect(within(table).getByRole("link", { name: "Administrateur du catalogue" })).toHaveAttribute(
      "href",
      "/admin/roles/catalog-admin",
    );
    expect(within(table).queryByRole("link", { name: "Catalog admin" })).not.toBeInTheDocument();
  });

  it("names a built-in role's permissions and their areas in French", { timeout: 15_000 }, async () => {
    db.me.locale = "fr";
    renderApp("/admin/roles/project-admin");
    expect(await screen.findByRole("heading", { level: 1, name: "Administrateur de projet" })).toBeInTheDocument();
    expect(await screen.findByText("Demander pour quelqu'un d'autre.")).toBeInTheDocument();
    expect(screen.queryByText("Request for someone else.")).not.toBeInTheDocument();
  });

  it("links the roles for all of rvav to the identity provider groups", { timeout: 15_000 }, async () => {
    db.me.locale = "fr";
    renderApp("/admin/roles?tab=rvav");
    expect(await screen.findByRole("table", { name: "Donnés pour l'ensemble de rvav" })).toBeInTheDocument();
    expect(screen.getByRole("link", { name: "Projets › Groupes du fournisseur d'identité" })).toHaveAttribute(
      "href",
      "/admin/projects?tab=groups",
    );
  });
});
