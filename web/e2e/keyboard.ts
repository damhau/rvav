import { expect, type Locator, type Page } from "@playwright/test";

// The keys of the keyboard-only specs, each awaited until it has done what it
// does. Radix moves the focus a task after the key that moves it (a select's
// options, a menu's items, tabs), and gives it back to a select a task after
// its list has gone: read at once, the focus is still where it was, and the
// next key goes one too far or is lost. Nightly runs 45018303759 and
// 45047495115 failed so (#197).

export type Way = "Tab" | "Shift+Tab";
type Name = string | RegExp;

/** The element that has the focus, named for a failure's message. */
export function focused(page: Page): Promise<string> {
  return page.evaluate(() => {
    const e = document.activeElement;
    if (!e || e === document.body) return "the page's body";
    const name = (e.getAttribute("aria-label") ?? e.textContent ?? "").replace(/\s+/g, " ").trim().slice(0, 80);
    return `${e.getAttribute("role") ?? e.tagName.toLowerCase()} "${name}"`;
  });
}

/**
 * Tab until `target` has the focus: Shift+Tab when it comes before what has
 * the focus (the page has no positive tabindex, so Tab follows the DOM).
 * When nothing has (what had it went away, a button gone or disabled), Tab
 * goes on from where it was: `gone` is the way to the target from there, as
 * the reader knows it (else forward, then back: a walk to the page's end).
 */
export async function tabTo(page: Page, target: Locator, gone?: Way) {
  await expect(target).toBeVisible();
  const at = () => target.evaluate((e) => e === document.activeElement);
  const before = await target.evaluate((e) => {
    const now = document.activeElement;
    if (!now || now === document.body) return undefined;
    return !!(e.compareDocumentPosition(now) & Node.DOCUMENT_POSITION_FOLLOWING);
  });
  const back = (w: Way): Way => (w === "Tab" ? "Shift+Tab" : "Tab");
  const ways: Way[] = before === undefined ? [gone ?? "Tab", back(gone ?? "Tab")] : [before ? "Shift+Tab" : "Tab"];
  for (const key of ways) {
    for (let i = 0; i < 150; i++) {
      if (await at()) return;
      await page.keyboard.press(key);
    }
  }
  if (await at()) return;
  throw new Error(`${ways.join(" then ")} never reached ${target.toString()}: the focus is on ${await focused(page)}`);
}

/** Tab to `target`, then press `key` on it. */
export async function press(page: Page, target: Locator, key = "Enter") {
  await tabTo(page, target);
  await page.keyboard.press(key);
}

/** Tab to a field, then type `text` in place of what it holds. */
export async function typeInto(page: Page, field: Locator, text: string) {
  await tabTo(page, field);
  await page.keyboard.press("Control+A");
  await page.keyboard.press("Delete");
  await page.keyboard.type(text);
}

/** Types into the focused field (in place of what it holds, with `replace`), then Tab, which commits it. */
export async function typeAndTab(page: Page, text: string, replace = false) {
  if (replace) await page.keyboard.press("Control+a");
  await page.keyboard.type(text);
  await page.keyboard.press("Tab");
}

/** The page's skip link (`name` in the language shown), then Enter: Tab goes on from the page's content. */
export async function skipToContent(page: Page, name = "Skip to content") {
  // The page's first Tab stop: the first Tab of a page just opened.
  await tabTo(page, page.getByRole("link", { name }), "Tab");
  await page.keyboard.press("Enter");
}

/** Drawn: React has rendered what a key changed, and run its effects. */
export const drawn = (page: Page) =>
  page.evaluate(() => new Promise((r) => requestAnimationFrame(() => requestAnimationFrame(r))));

interface Seen {
  /** The entry the keys are on, by its place in the list; -1 for none. */
  at: number;
  /** The focus is in the list: on an entry, or on the list itself (a select opened with no value). */
  inside: boolean;
  /** The entry wanted, by its place. */
  want: number;
  count: number;
  /** What the keys are on, for a failure's message. */
  on: string;
}

/**
 * Where the keys are in the list or menu that holds `entry`, once the focus
 * is in it ("in"), on an entry other than `from`, or on `entry` ("on"); as
 * they are after five seconds otherwise.
 */
function seen(entry: Locator, until: "in" | "on" | { from: number }): Promise<Seen> {
  return entry.evaluate(async (wanted, until) => {
    const read = (): Seen => {
      const box = wanted.closest('[role="listbox"], [role="menu"]');
      const all = box ? [...box.querySelectorAll('[role="option"], [role^="menuitem"]')] : [];
      const now = document.activeElement;
      const ref = now?.getAttribute("aria-activedescendant");
      const at = all.findIndex((e) => e === now || (!!ref && e.id === ref));
      const inside = at >= 0 || (!!box && !!now && box.contains(now));
      const text = (e: Element | null | undefined) => `"${(e?.textContent ?? "").replace(/\s+/g, " ").trim()}"`;
      const on = at >= 0 ? text(all[at]) : inside ? "the list itself" : now === document.body ? "the body" : text(now);
      return { at, inside, want: all.indexOf(wanted), count: all.length, on };
    };
    const done = (s: Seen) =>
      until === "in" ? s.inside : until === "on" ? s.at === s.want : s.at >= 0 && s.at !== until.from;
    const end = Date.now() + 5_000;
    let s = read();
    while (!done(s) && Date.now() < end) {
      await new Promise((r) => setTimeout(r, 10));
      s = read();
    }
    return s;
  }, until);
}

/** From where the keys are in the list, the arrow keys to `entry`, one at a time, each awaited until it has moved. */
async function arrowsTo(page: Page, entry: Locator, what: string) {
  let now = await seen(entry, "in");
  if (now.want < 0) throw new Error(`${what} is in no list or menu`);
  if (!now.inside) throw new Error(`${what}: the list never took the focus, which is on ${now.on}`);
  for (let keys = 0; now.at !== now.want; keys++) {
    if (keys > now.count) throw new Error(`${what}: ${keys} keys, and the keys are on ${now.on}`);
    const key = now.at < now.want ? "ArrowDown" : "ArrowUp";
    await page.keyboard.press(key);
    const next = await seen(entry, { from: now.at });
    if (next.at < 0 || next.at === now.at) throw new Error(`${what}: ${key} on ${now.on} left the keys on ${next.on}`);
    now = next;
  }
}

/** Once a list has closed, the focus is back on its select, or wherever the page puts it. */
async function landed(page: Page, what: string) {
  const back = () => {
    const e = document.activeElement;
    return !!e && e !== document.body && !e.closest('[role="listbox"]');
  };
  await page.waitForFunction(back, undefined, { timeout: 5_000 }).catch(async () => {
    throw new Error(`${what} chosen, and the focus is on ${await focused(page)}`);
  });
}

interface How {
  /** Typeahead: types this in the open list, in place of the arrow keys. */
  typed?: string;
  /** Clicks the option, in place of the arrow keys. */
  click?: boolean;
  /** A string `name` is the option's whole name, unless this is false: then a part of it, in any case. */
  exact?: boolean;
}

/**
 * Opens the focused select with Enter, then the arrow keys to the option
 * `name`, then Enter. It waits for the list and its options, for each key
 * to move, and for the list to close and the focus to come back: a key
 * pressed before then goes to the list, or nowhere.
 */
export async function choose(page: Page, name: Name, how: How = {}) {
  const what = `the option ${String(name)}`;
  await page.keyboard.press("Enter");
  const option = page.getByRole("option", { name, exact: typeof name === "string" && how.exact !== false });
  const list = page.getByRole("listbox").filter({ has: option });
  await expect(list, `a list opened by Enter, with ${what}`).toBeVisible();
  if (how.click) {
    await option.click();
  } else if (how.typed !== undefined) {
    const was = await seen(option, "in");
    if (!was.inside) throw new Error(`${what}: the list never took the focus, which is on ${was.on}`);
    await page.keyboard.type(how.typed);
    const now = await seen(option, "on");
    if (now.at !== now.want) throw new Error(`${what}: "${how.typed}" put the keys on ${now.on}`);
    await page.keyboard.press("Enter");
  } else {
    await arrowsTo(page, option, what);
    await page.keyboard.press("Enter");
  }
  await expect(list, `the list closed on ${what}`).toHaveCount(0);
  await landed(page, what);
}

/**
 * Tabs to `select` once it can be used, then chooses `name` in it (see
 * `choose`). `focus` focuses it instead, for a spec that reaches each
 * control by its locator rather than by Tab.
 */
export async function chooseIn(page: Page, select: Locator, name: Name, how: How & { focus?: boolean } = {}) {
  await expect(select).toBeEnabled();
  if (how.focus) await select.focus();
  else await tabTo(page, select);
  await choose(page, name, how);
}

/** In an open menu, the arrow keys to `item`, each awaited, then Enter; the menu closes unless it opens another. */
export async function chooseItem(page: Page, item: Locator) {
  const what = `the menu item ${item.toString()}`;
  await expect(item, what).toBeVisible();
  await arrowsTo(page, item, what);
  const submenu = (await item.getAttribute("aria-haspopup")) === "menu";
  await page.keyboard.press("Enter");
  if (!submenu) await expect(item, `the menu closed on ${what}`).toBeHidden();
}

/**
 * The tabs are one Tab stop, the arrow keys between them, each tab shown as
 * it is reached: from the one selected to `name`, a key at a time, each
 * awaited until it has selected the next, back on the tabs (above their
 * panel: `gone`) if what one showed took the focus.
 */
export async function selectTab(
  page: Page,
  name: string,
  key: "ArrowRight" | "ArrowLeft" = "ArrowRight",
  gone: Way = "Shift+Tab",
) {
  const target = page.getByRole("tab", { name, exact: true });
  const selected = page.getByRole("tablist").filter({ has: target }).getByRole("tab", { selected: true });
  for (let i = 0; i < 12 && (await target.getAttribute("aria-selected")) !== "true"; i++) {
    await tabTo(page, selected, gone);
    const was = (await selected.textContent()) ?? "";
    await page.keyboard.press(key);
    await expect(selected, `${key} from the tab ${was}, on the way to ${name}`).not.toHaveText(was);
    await drawn(page);
  }
  await expect(target).toHaveAttribute("aria-selected", "true");
}

/**
 * On a graph, the arrow keys from the focused node to `target`: along each
 * row, then down to the next. It never goes up: visit an upper node (a
 * branch) before the one below it (a step on its side), or start from the
 * graph's first node with `home`.
 */
export async function arrowTo(page: Page, target: Locator, { home = false } = {}) {
  const at = () => target.evaluate((e) => e === document.activeElement);
  const now = () => page.evaluate(() => document.activeElement?.getAttribute("aria-label") ?? "");
  const move = async (key: string) => {
    const before = await now();
    await page.keyboard.press(key);
    await drawn(page);
    return (await now()) !== before;
  };
  if (home) await move("Home");
  for (let row = 0; row < 12; row++) {
    if (await at()) return;
    for (const key of ["ArrowRight", "ArrowLeft"]) {
      while (await move(key)) if (await at()) return;
    }
    if (!(await move("ArrowDown"))) break;
  }
  if (await at()) return;
  throw new Error(`the arrow keys never reached ${target.toString()}: the focus is on ${await focused(page)}`);
}
