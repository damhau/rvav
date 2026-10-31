import type { Page } from "@playwright/test";

/** Whether the stack runs with Keycloak sign-in (RVAV_STACK_AUTH=oidc) or dev authentication. */
export const oidc = process.env.RVAV_STACK_AUTH === "oidc";

/**
 * With Keycloak, rvav's sign-in page offers single sign-on (ADR-0030), which
 * goes to Keycloak's login page: sign in there with one of the realm's users
 * (password `rvav`). With dev authentication there is nothing to do.
 */
export async function signIn(page: Page, username = "lea") {
  if (!oidc) return;
  await page.getByRole("button", { name: "Sign in with single sign-on" }).click();
  await page.waitForURL(/\/realms\/rvav\/protocol\/openid-connect\/auth/);
  await page.locator("#username").fill(username);
  await page.locator("#password").fill("rvav");
  await page.locator("#kc-login").click();
}
