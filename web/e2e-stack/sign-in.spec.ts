import { expect, test } from "@playwright/test";
import { oidc, signIn } from "./auth";

test.skip(!oidc, "the stack runs with dev authentication (set RVAV_STACK_AUTH=oidc)");

// Keycloak groups become project roles: lea is in rvav-web-admins, thomas in rvav-web.
for (const { user, name, role } of [
  { user: "lea", name: "Léa Martin", role: "Project admin" },
  { user: "thomas", name: "Thomas Nguyen", role: "Member" },
]) {
  test(`${user} signs in through Keycloak as ${role.toLowerCase()} of Web Platform`, async ({ page }) => {
    await page.goto("/deployments");
    await signIn(page, user);

    // Back on the page asked for, with the identity and role from the token.
    await expect(page).toHaveURL(/\/deployments$/);
    const account = page.getByRole("button", { name: "Account" });
    await expect(account).toContainText(name);
    await expect(account).toContainText(role);

    // Signing out ends the Keycloak session: the next visit asks again.
    await account.click();
    await page.getByRole("menuitem", { name: "Sign out" }).click();
    await page.waitForURL(/\/realms\/rvav\/protocol\/openid-connect\/auth/);
  });
}
