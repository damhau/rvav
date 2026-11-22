import { afterEach, describe, expect, it } from "vitest";
import { i18n } from "@/lib/i18n";
import { withUnits } from "@/lib/utils";
import { setupDetail } from "./SetupPage";

const t = () => i18n.getFixedT(null, "admin");

afterEach(async () => {
  await i18n.changeLanguage("en");
});

describe("a setup step's detail", () => {
  it("shows the server's words in English", () => {
    for (const d of [
      "Not set.",
      "The development fake (RVAV_FAKE_VCENTER).",
      "1 environment, 2 projects, 0 placement rules",
      "3 items, 1 published",
    ])
      expect(setupDetail(t(), d)).toBe(d);
  });

  it("is worded in French, counts included, and leaves addresses and groups as they are", async () => {
    await i18n.changeLanguage("fr");
    expect(setupDetail(t(), "Not set.")).toBe("Non défini.");
    expect(setupDetail(t(), "None: only the local administrator can sign in.")).toBe(
      "Aucun : seul l'administrateur local peut se connecter.",
    );
    expect(setupDetail(t(), "The development fake (RVAV_FAKE_AWX).")).toBe(
      "Le simulateur de développement (RVAV_FAKE_AWX).",
    );
    expect(setupDetail(t(), "1 environment, 2 projects, 0 placement rules")).toBe(
      "1 environnement, 2 projets, 0 règle de placement",
    );
    expect(setupDetail(t(), "3 items, 1 published")).toBe("3 éléments, 1 publié");
    expect(setupDetail(t(), "https://vcenter.example.com")).toBe("https://vcenter.example.com");
    expect(setupDetail(t(), "rvav-admins, ops")).toBe("rvav-admins, ops");
  });
});

describe("a size option's meta", () => {
  it("writes its sizes in the language shown", async () => {
    expect(withUnits("2 vCPU · 4 GB · 60 GB")).toBe("2 vCPU · 4 GB · 60 GB");
    await i18n.changeLanguage("fr");
    expect(withUnits("2 vCPU · 4 GB · 60 GB")).toBe("2 vCPU · 4 Go · 60 Go");
    expect(withUnits("1.5 TB free")).toBe("1,5 To free");
  });
});
