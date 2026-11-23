import { expect, test, type Locator, type Page } from "@playwright/test";
import { bothThemes, scan } from "./axe";
import { chooseIn, chooseItem, press, tabTo, typeInto } from "./keyboard";

// A save the server refuses, from the keyboard (#199): the focus goes to the
// field the refusal names once that field is marked, so a screen reader reads
// the problem with it, on a catalog item's details, an answer to a step that
// waits for a person, and an update refused once confirmed. In English and in
// French, with axe in both themes.

interface FocusWindow {
  __focusLog: { el: Element; marked: boolean }[];
  __rvavMocks: { faults: { refuseNextUpdate(errors: { field: string; code: string; message: string }[]): void } };
}

/** Logs each focus, with whether what got it was marked invalid and described then. */
async function logFocus(page: Page) {
  await page.addInitScript(() => {
    const log: FocusWindow["__focusLog"] = [];
    (window as unknown as FocusWindow).__focusLog = log;
    document.addEventListener("focusin", (e) => {
      const el = e.target as Element;
      log.push({ el, marked: el.getAttribute("aria-invalid") === "true" && el.hasAttribute("aria-describedby") });
    });
  });
}

const forgetFocus = (page: Page) =>
  page.evaluate(() => void ((window as unknown as FocusWindow).__focusLog.length = 0));

/** Whether the field was marked as the focus first reached it since `forgetFocus`. */
const markedAsFocused = (field: Locator) =>
  field.evaluate((el) => (window as unknown as FocusWindow).__focusLog.find((f) => f.el === el)?.marked ?? null);

async function loaded(page: Page) {
  await expect(page.getByRole("main")).toBeVisible();
  await expect(page.locator(".animate-pulse")).toHaveCount(0);
}

/**
 * A request of a composite that asks its requester which backup it gets, made
 * on the page's mock API (a reload would start it afresh); its number.
 */
async function asked(page: Page): Promise<string> {
  await loaded(page);
  return page.evaluate(async () => {
    const call = async <T>(method: string, path: string, body?: unknown): Promise<T> => {
      const res = await fetch(`/api/v1${path}`, {
        method,
        headers: {
          Authorization: "Bearer spec",
          "Content-Type": "application/json",
          "Idempotency-Key": crypto.randomUUID(),
        },
        body: body === undefined ? undefined : JSON.stringify(body),
      });
      if (!res.ok) throw new Error(`${method} ${path}: ${res.status} ${await res.text()}`);
      return (res.status === 204 ? undefined : await res.json()) as T;
    };
    const slug = "backup-check";
    const item = await call<{ id: string; draft_version: { id: string } }>("POST", "/admin/catalog/items", {
      slug,
      name: "Backup check",
      summary: "Asks.",
      category: "compute",
      type: "composite",
    });
    const version = `/admin/catalog/items/${slug}/versions/${item.draft_version.id}`;
    const draft = await call<{ definition: Record<string, unknown>; revision: number }>("GET", version);
    const ask = {
      requester: true,
      form: [{ key: "backup", label: "Backup policy", type: "choice", options: ["daily", "none"], required: true }],
    };
    const blueprint = { kind: "composite", steps: [{ key: "check", title: "Owner check", ask }] };
    await call("PUT", version, { definition: { ...draft.definition, blueprint }, base_revision: draft.revision });
    await call("POST", `${version}/publish`);
    await call("PUT", `/admin/catalog/items/${slug}/entitlements`, { all_projects: true, project_ids: [] });
    const r = await call<{ number: string }>("POST", "/requests", {
      catalog_item_id: item.id,
      version_id: item.draft_version.id,
      project_id: "prj_web",
      environment: "dev",
      inputs: { name: "web-01" },
    });
    return r.number;
  });
}

/** Opens a request from the list, by keyboard: the page's mock API keeps what the spec made. */
async function openRequest(page: Page, requests: RegExp, number: string) {
  await press(page, page.getByRole("navigation").getByRole("link", { name: requests }).first());
  await press(
    page,
    page
      .getByRole("row", { name: new RegExp(number) })
      .getByRole("link")
      .first(),
  );
}

test.beforeEach(async ({ page }) => logFocus(page));

test("a catalog item's details refused go to the summary, heard with its problem", async ({ page }) => {
  await page.goto("/admin/catalog-items/itm_rhel9?tab=details");
  const details = page.getByRole("region", { name: "Details", exact: true });
  const summary = details.getByRole("textbox", { name: "Summary", exact: true });
  const save = details.getByRole("button", { name: "Save details", exact: true });
  await typeInto(page, summary, "");
  await forgetFocus(page);
  await press(page, save);
  await expect(summary).toBeFocused();
  await expect(summary).toHaveAttribute("aria-invalid", "true");
  await expect(summary).toHaveAccessibleDescription("Say in a line what the item gives.");
  expect(await markedAsFocused(summary)).toBe(true);
  await bothThemes(page, "a catalog item's details refused");

  await typeInto(page, summary, "A RHEL 9 VM, patched monthly.");
  await press(page, save);
  await expect(page.getByText("RHEL 9 server saved")).toBeVisible();
  await expect(save).toBeFocused();
  await expect(summary).not.toHaveAttribute("aria-invalid");
});

test("an answer refused goes to the question to answer, heard with its problem", async ({ page }) => {
  test.slow();
  await page.goto("/");
  const number = await asked(page);
  await openRequest(page, /^Requests/, number);
  const form = page.getByRole("form", { name: "Your answer: Owner check" });
  await expect(form).toBeVisible({ timeout: 20_000 });
  const backup = form.getByRole("combobox", { name: /^Backup policy/ });
  // Tabbed to, Send answer is passed the question on the way.
  await tabTo(page, form.getByRole("button", { name: "Send answer", exact: true }));
  await forgetFocus(page);
  await page.keyboard.press("Enter");
  await expect(backup).toBeFocused();
  await expect(backup).toHaveAccessibleDescription("Answer this.");
  expect(await markedAsFocused(backup)).toBe(true);
  await bothThemes(page, "an answer refused");

  await chooseIn(page, backup, "none");
  await press(page, form.getByRole("button", { name: "Send answer", exact: true }));
  await expect(page.getByRole("list", { name: "Steps that wait for a person" })).toContainText("Answered by");
});

test("an update refused once confirmed leaves the focus on the field to fix, not on Review", async ({ page }) => {
  test.slow();
  await page.goto("/deployments");
  await press(page, page.getByRole("link", { name: "web-tst-search", exact: true }));
  await expect(page.getByRole("heading", { name: "web-tst-search", level: 1 })).toBeVisible();
  await press(page, page.getByRole("button", { name: "Actions", exact: true }));
  await chooseItem(page, page.getByRole("menuitem", { name: "Update", exact: true }));
  const summary = page.getByRole("region", { name: "What the update does" });
  await press(page, page.getByRole("radio", { name: /^X-Large/ }), "Space");
  await expect(summary.getByRole("list", { name: "Changes" }).getByRole("listitem")).toHaveCount(2);

  // What the server answers when the size was withdrawn between Review and the confirmation.
  await page.evaluate(() =>
    (window as unknown as FocusWindow).__rvavMocks.faults.refuseNextUpdate([
      { field: "/inputs/size", code: "invalid", message: "X-Large is no longer offered in tst." },
    ]),
  );
  const review = summary.getByRole("button", { name: "Review the update" });
  await press(page, review);
  const dialog = page.getByRole("alertdialog", { name: "Update web-tst-search?" });
  await press(page, dialog.getByRole("button", { name: "Update", exact: true }));
  await expect(dialog).toHaveCount(0);
  const size = page.locator('[data-field="size"]');
  await expect(size).toContainText("X-Large is no longer offered in tst.");
  await expect(size.locator(":focus")).toHaveCount(1);
  // The dialog gives the focus back a task after it has gone: it stays.
  await page.waitForTimeout(500);
  await expect(size.locator(":focus")).toHaveCount(1);
  await expect(review).not.toBeFocused();
  await bothThemes(page, "an update refused once confirmed");
});

test.describe("in French", () => {
  test.use({ locale: "fr-CH" });

  async function frenchThemes(page: Page, where: string) {
    await expect(page.locator("html")).toHaveAttribute("lang", "fr");
    for (const [name, value] of [
      ["Clair", "light"],
      ["Sombre", "dark"],
    ] as const) {
      await page.getByRole("button", { name: /^Thème/ }).click();
      await page.getByRole("menuitem", { name }).click();
      await expect(page.locator("html")).toHaveAttribute("data-theme", value);
      await page.waitForTimeout(500);
      await scan(page, `${where}, ${value}`);
    }
  }

  test("a catalog item's details refused go to the summary", async ({ page }) => {
    await page.goto("/admin/catalog-items/itm_rhel9?tab=details");
    const details = page.getByRole("region", { name: "Détails", exact: true });
    const summary = details.getByRole("textbox", { name: "Résumé", exact: true });
    await typeInto(page, summary, "");
    await forgetFocus(page);
    await press(page, details.getByRole("button", { name: "Enregistrer les détails", exact: true }));
    await expect(summary).toBeFocused();
    await expect(summary).toHaveAccessibleDescription("Say in a line what the item gives.");
    expect(await markedAsFocused(summary)).toBe(true);
    await frenchThemes(page, "a catalog item's details refused, in French");
  });

  test("an answer refused goes to the question to answer", async ({ page }) => {
    test.slow();
    await page.goto("/");
    const number = await asked(page);
    await openRequest(page, /^Demandes/, number);
    const form = page.getByRole("form", { name: "Votre réponse : Owner check" });
    await expect(form).toBeVisible({ timeout: 20_000 });
    const backup = form.getByRole("combobox", { name: /^Backup policy/ });
    await tabTo(page, form.getByRole("button", { name: "Envoyer la réponse", exact: true }));
    await forgetFocus(page);
    await page.keyboard.press("Enter");
    await expect(backup).toBeFocused();
    await expect(backup).toHaveAccessibleDescription("Answer this.");
    expect(await markedAsFocused(backup)).toBe(true);
    await frenchThemes(page, "an answer refused, in French");
  });
});
