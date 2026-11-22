import { screen, within } from "@testing-library/react";
import { beforeEach, describe, expect, it } from "vitest";
import { i18n } from "@/lib/i18n";
import { db } from "@/mocks/seed";
import { renderApp } from "@/test/render";
import { vmCount } from "./ResourcesPage";

describe("the VM count, in French", () => {
  beforeEach(async () => {
    await i18n.changeLanguage("fr");
  });

  it("agrees with the number of VMs, VM staying invariable", () => {
    expect(vmCount(1, 1, false)).toBe("1 VM · 1 allumée");
    expect(vmCount(12, 10, false)).toBe("12 VM · 10 allumées");
    expect(vmCount(5, 0, true)).toBe("5 affichées · dont 0 allumée");
  });
});

describe("the resources page, in French", () => {
  beforeEach(() => {
    db.me.locale = "fr";
  });

  it("names its columns and sizes in French", async () => {
    renderApp("/resources");
    expect(await screen.findByRole("heading", { name: "Ressources", level: 1 }, { timeout: 5000 })).toBeVisible();
    const table = await screen.findByRole("table", { name: "VM" });
    expect(within(table).getByRole("button", { name: "Disques" })).toBeInTheDocument();
    const row = (await within(table).findByText("web-prd-front001")).closest("tr")!;
    // Testing Library reads a no-break space as a space.
    expect(row).toHaveTextContent("48 Go1 · 80 Go");
  });
});
