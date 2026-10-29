import { expect, test } from "@playwright/test";
import { oidc, signIn } from "./auth";

// The web app's security headers (S14.6), and nothing the page does breaks
// its Content-Security-Policy: signing in, pages that load code on demand,
// the editor, the live stream.
test("pages load within the Content-Security-Policy", async ({ page }) => {
  const violations: string[] = [];
  page.on("console", (m) => {
    if (m.text().includes("Content Security Policy")) violations.push(m.text());
  });
  const response = await page.goto("/");
  const headers = response?.headers() ?? {};
  expect(headers["content-security-policy"]).toContain("default-src 'self'");
  expect(headers["content-security-policy"]).toContain("object-src 'none'");
  expect(headers["x-frame-options"]).toBe("SAMEORIGIN");
  expect(headers["x-content-type-options"]).toBe("nosniff");
  if (oidc) {
    expect(headers["content-security-policy"]).toMatch(/connect-src 'self' http:\/\/localhost:\d+;/);
    await signIn(page, "lea");
  }
  for (const path of ["/catalog", "/requests", "/deployments", "/approvals"]) {
    await page.goto(path);
    await expect(page.getByRole("main")).toBeVisible();
  }
  await page.waitForTimeout(500);
  expect(violations).toEqual([]);
});
