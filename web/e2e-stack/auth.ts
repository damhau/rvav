import type { Page } from "@playwright/test";

/** Whether the stack runs with Keycloak sign-in (RVAV_STACK_AUTH=oidc) or dev authentication. */
export const oidc = process.env.RVAV_STACK_AUTH === "oidc";

/**
 * With Keycloak, the app sends the browser to the login page: sign in there
 * with one of the realm's users (password `rvav`). With dev authentication
 * there is nothing to do.
 */
export async function signIn(page: Page, username = "lea") {
  if (!oidc) return;
  await page.waitForURL(/\/realms\/rvav\/protocol\/openid-connect\/auth/);
  await page.locator("#username").fill(username);
  await page.locator("#password").fill("rvav");
  await page.locator("#kc-login").click();
}
