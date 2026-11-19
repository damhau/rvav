import { render, screen } from "@testing-library/react";
import { http, HttpResponse } from "msw";
import { afterEach, describe, expect, it } from "vitest";
import { SignInPage } from "@/features/account/SignIn";
import { brandingState } from "@/mocks/branding";
import { server } from "@/mocks/node";
import { db, resetDb } from "@/mocks/seed";
import { applyBranding, brandingCss, loadBranding, rvavBranding } from "./branding";

afterEach(() => {
  applyBranding(rvavBranding);
  resetDb();
});

describe("the portal's branding", () => {
  it("keeps rvav's own colours until an accent is set", () => {
    expect(brandingCss(rvavBranding)).toBe("");
    applyBranding(rvavBranding);
    expect(document.getElementById("rvav-branding")).toBeNull();
    expect(document.title).toBe("rvav");
  });

  it("is read before signing in: the sign-in page, the title and both themes show it", async () => {
    Object.assign(brandingState(db), { name: "Corp Cloud", accent: "#5b2a86" });
    await loadBranding();
    render(<SignInPage provider />);
    expect(screen.getByRole("heading", { name: "Sign in to Corp Cloud" })).toBeInTheDocument();
    expect(document.title).toBe("Corp Cloud");
    const css = document.getElementById("rvav-branding")!.textContent;
    expect(css).toContain("html:root { --accent: #5b2a86; --accent-fg: #ffffff;");
    expect(css).toMatch(
      /@media \(prefers-color-scheme: dark\) \{ html:root:not\(\[data-theme="light"\]\) \{ --accent: #/,
    );
    expect(css).toMatch(/html:root\[data-theme="dark"\] \{ --accent: #(?!5b2a86)/);

    applyBranding(rvavBranding);
    expect(document.getElementById("rvav-branding")!.textContent).toBe("");
  });

  it("stays rvav's when it can't be read", async () => {
    server.use(http.get("*/api/v1/branding", () => HttpResponse.error()));
    await loadBranding();
    render(<SignInPage provider />);
    expect(screen.getByRole("heading", { name: "Sign in to rvav" })).toBeInTheDocument();
  });
});
