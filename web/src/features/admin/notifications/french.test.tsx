import { screen, within } from "@testing-library/react";
import { beforeEach, describe, expect, it } from "vitest";
import { i18n } from "@/lib/i18n";
import { db } from "@/mocks/seed";
import { renderApp } from "@/test/render";

beforeEach(async () => {
  db.me.locale = "fr";
  await i18n.changeLanguage("fr");
});

describe("notification templates, in French", () => {
  it("lists the templates", { timeout: 15_000 }, async () => {
    renderApp("/admin/notification-templates");
    expect(await screen.findByRole("heading", { level: 1, name: "Modèles de notification" })).toBeInTheDocument();
    const table = await screen.findByRole("table", { name: "Modèles de notification" });
    expect(
      within(table)
        .getAllByRole("columnheader")
        .map((h) => h.textContent),
    ).toEqual(["Notification", "Modifiés", "Changé"]);
    expect(await within(table).findAllByText("Modèles intégrés")).not.toHaveLength(0);
    expect(within(table).getByRole("link", { name: "Approbation requise" })).toBeInTheDocument();
    expect(within(table).getByText("Au demandeur, une fois que le dernier niveau a approuvé.")).toBeInTheDocument();
    expect(within(table).queryByText(/^To the /)).not.toBeInTheDocument();
  });

  it("edits a template with French labels, and names languages inside a sentence", { timeout: 15_000 }, async () => {
    renderApp("/admin/notification-templates/approval_needed");
    expect(await screen.findByRole("button", { name: "E-mail" })).toHaveAttribute("aria-pressed", "true");
    expect(screen.getByRole("button", { name: "Anglais" })).toHaveAttribute("aria-pressed", "true");
    expect(await screen.findByLabelText("Objet")).toBeInTheDocument();
    expect(screen.getByRole("heading", { level: 1, name: "Approbation requise" })).toBeInTheDocument();
    expect((await screen.findByText(/^Ce que peut écrire un modèle/)).textContent).toBe(
      "Ce que peut écrire un modèle de la notification «\u00a0Approbation requise\u00a0». Un modèle qui en nomme une autre est refusé.",
    );
    expect(screen.getByText("Le modèle intégré.")).toBeInTheDocument();
    expect(
      screen.getByText("Avec des données d'exemple, comme l'afficherait un e-mail en anglais."),
    ).toBeInTheDocument();
    expect(screen.getByRole("button", { name: "Enregistrer le modèle" })).toBeDisabled();
    expect(await screen.findByTitle("L'e-mail en HTML")).toBeInTheDocument();
  });
});
