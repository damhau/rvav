import { screen, within } from "@testing-library/react";
import { beforeEach, describe, expect, it } from "vitest";
import { db } from "@/mocks/seed";
import { renderApp } from "@/test/render";

beforeEach(() => {
  db.me.locale = "fr";
});

describe("the catalog, in French", () => {
  it("counts its items and names its actions in French", async () => {
    renderApp("/catalog");
    expect(await screen.findByRole("heading", { name: "Catalogue", level: 1 }, { timeout: 5000 })).toBeInTheDocument();
    expect(await screen.findByText("11 éléments")).toBeInTheDocument();
    expect(screen.getByRole("link", { name: "Demander RHEL 9 server" })).toHaveAttribute(
      "href",
      "/catalog/rhel9-server/request",
    );
    expect(screen.getByRole("searchbox", { name: "Rechercher dans le catalogue" })).toBeInTheDocument();
    expect(
      screen.getByRole("button", { name: /^(Ajouter RHEL 9 server aux|Retirer RHEL 9 server des) favoris$/ }),
    ).toBeInTheDocument();
  });

  it("says an item's details in French, with one request said in the singular", async () => {
    const item = db.items.find((i) => i.slug === "rhel9-server")!;
    item.requests_last_30_days = 1;
    renderApp("/catalog/rhel9-server");
    expect(
      await screen.findByRole("heading", { name: "RHEL 9 server", level: 1 }, { timeout: 5000 }),
    ).toBeInTheDocument();
    const details = screen.getByRole("region", { name: "Détails" });
    expect(within(details).getByText("Approbation").nextElementSibling).toHaveTextContent(
      "Approbation requise en production",
    );
    expect(within(details).getByText("Demandé").nextElementSibling).toHaveTextContent("1 fois ces 30 derniers jours");
    expect(within(details).getByText("Version").nextElementSibling).toHaveTextContent(/v3\.2 · publiée il y a/);
    expect(await screen.findByRole("region", { name: "Ce qu'il construit" })).toBeInTheDocument();
  });
});
