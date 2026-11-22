import { screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { http, HttpResponse } from "msw";
import { afterEach, describe, expect, it, vi } from "vitest";
import { server } from "@/mocks/node";
import { db } from "@/mocks/seed";
import { renderApp } from "@/test/render";
import { applyLocale, currentLocale, i18n, LOCALE_KEY } from ".";

function browserSpeaks(...languages: string[]) {
  vi.spyOn(navigator, "languages", "get").mockReturnValue(languages);
  vi.spyOn(navigator, "language", "get").mockReturnValue(languages[0] ?? "");
}

afterEach(() => vi.restoreAllMocks());

describe("the language shown", () => {
  it("is the person's choice, whatever the browser speaks", async () => {
    browserSpeaks("en-GB", "en");
    await applyLocale("fr");
    expect(currentLocale()).toBe("fr");
    expect(document.documentElement.lang).toBe("fr");
    expect(localStorage.getItem(LOCALE_KEY)).toBe("fr");
  });

  it("is the browser's language until the person chooses", async () => {
    browserSpeaks("de-CH", "fr-CH", "en");
    await applyLocale(undefined);
    expect(currentLocale()).toBe("fr");
    expect(localStorage.getItem(LOCALE_KEY)).toBeNull();
  });

  it("is a regional language's own, as the app starts: fr-CH shows fr, and says so in <html lang>", async () => {
    localStorage.removeItem(LOCALE_KEY);
    browserSpeaks("de-CH", "fr-CH", "en");
    await i18n.changeLanguage();
    expect(i18n.language).toBe("fr");
    expect(document.documentElement.lang).toBe("fr");
  });

  it("is English when rvav speaks none of the browser's languages", async () => {
    await i18n.changeLanguage("fr");
    browserSpeaks("de-DE", "it");
    await applyLocale(undefined);
    expect(currentLocale()).toBe("en");
    expect(document.documentElement.lang).toBe("en");
  });
});

describe("choosing a language", () => {
  async function accountMenu() {
    await userEvent.click(await screen.findByRole("button", { name: /^Account/ }, { timeout: 5000 }));
  }

  it("shows the app in it at once, sets <html lang>, and keeps it on the server", async () => {
    browserSpeaks("en-GB");
    renderApp("/");
    await accountMenu();
    const french = screen.getByRole("menuitem", { name: "Français" });
    expect(french).toHaveAttribute("lang", "fr");
    await userEvent.click(french);
    expect(await screen.findByText("Rechercher des VM, des IP, des demandes…")).toBeInTheDocument();
    expect(document.documentElement.lang).toBe("fr");
    await waitFor(() => expect(db.me.locale).toBe("fr"));
  });

  it("puts the language back when the server refuses it", async () => {
    browserSpeaks("en-GB");
    server.use(
      http.put("*/api/v1/me/locale", () =>
        HttpResponse.json(
          { title: "Not now", status: 503 },
          { status: 503, headers: { "Content-Type": "application/problem+json" } },
        ),
      ),
    );
    renderApp("/");
    await accountMenu();
    await userEvent.click(screen.getByRole("menuitem", { name: "Français" }));
    expect(await screen.findByText("Your language wasn't saved")).toBeInTheDocument();
    expect(screen.getByText("Search VMs, IPs, requests…")).toBeInTheDocument();
    expect(document.documentElement.lang).toBe("en");
  });

  it("shows a language chosen elsewhere from the first page, in any browser", async () => {
    browserSpeaks("en-GB");
    db.me.locale = "fr";
    renderApp("/");
    expect(
      await screen.findByText("Rechercher des VM, des IP, des demandes…", {}, { timeout: 5000 }),
    ).toBeInTheDocument();
    expect(screen.queryByText("Search VMs, IPs, requests…")).not.toBeInTheDocument();
    expect(document.documentElement.lang).toBe("fr");
  });
});
