import { expect, type Locator, type Page, test } from "@playwright/test";
import { parse } from "yaml";
import { bothThemes, scan } from "./axe";

// I1.4's VM designer (ADR-0032, ADR-0066) on the mock backend. Each
// keyboard step waits for what it does (the list shown, the option
// highlighted, the focus moved) rather than counting keys: a count breaks
// when a list opens on another option or a render lands late (#197).

type Way = "Tab" | "Shift+Tab";

/**
 * Tab until `target` has the focus: Shift+Tab when it comes before what has
 * the focus. When nothing has (what had it went away), Tab goes on from
 * where it was: `gone` is the way to the target from there.
 */
async function tabTo(page: Page, target: Locator, gone?: Way) {
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
  if (!(await at())) throw new Error(`${ways.join(" then ")} never reached ${target.toString()}`);
}

async function skipToContent(page: Page, name = "Skip to content") {
  await tabTo(page, page.getByRole("link", { name }), "Tab");
  await page.keyboard.press("Enter");
}

/**
 * In a list opened from the focused select: Enter opens it, then the arrow
 * keys to the option, each key waited for until the highlight has moved,
 * then Enter, waited for until the list is gone.
 */
async function choose(page: Page, name: string | RegExp) {
  await page.keyboard.press("Enter");
  const list = page.getByRole("listbox");
  await expect(list).toBeVisible();
  const target = list.getByRole("option", { name, exact: typeof name === "string" });
  await expect(target).toBeVisible();
  const want = await target.evaluate((t) =>
    [...t.closest("[role=listbox]")!.querySelectorAll("[role=option]")].indexOf(t),
  );
  const highlighted = () =>
    list.evaluate((l) =>
      [...l.querySelectorAll("[role=option]")].findIndex(
        (o) => o.hasAttribute("data-highlighted") || o === document.activeElement,
      ),
    );
  for (let i = 0; i < 40; i++) {
    const now = await highlighted();
    if (now === want) {
      await page.keyboard.press("Enter");
      await expect(list).toBeHidden();
      return;
    }
    await page.keyboard.press(now > want ? "ArrowUp" : "ArrowDown");
    await expect.poll(highlighted).not.toBe(now);
  }
  throw new Error(`no option ${name.toString()}`);
}

/** Drawn: React has rendered what a key changed, and run its effects. */
const drawn = (page: Page) =>
  page.evaluate(() => new Promise((r) => requestAnimationFrame(() => requestAnimationFrame(r))));

/** The tabs: from the one selected to `name`, a key at a time, each waited for. */
async function selectTab(page: Page, name: string, key: "ArrowRight" | "ArrowLeft") {
  const target = page.getByRole("tab", { name, exact: true });
  const selected = page.getByRole("tab", { selected: true });
  for (let i = 0; i < 8 && (await target.getAttribute("aria-selected")) !== "true"; i++) {
    await tabTo(page, selected, "Shift+Tab");
    const was = (await selected.textContent()) ?? "";
    await page.keyboard.press(key);
    await expect(selected).not.toHaveText(was);
    await drawn(page);
  }
  await expect(target).toHaveAttribute("aria-selected", "true");
}

/** On a graph, from the focused node: Home to the first, then down, a node at a time, each waited for. */
async function arrowTo(page: Page, target: Locator) {
  const at = () => target.evaluate((e) => e === document.activeElement);
  const now = () => page.evaluate(() => document.activeElement?.getAttribute("aria-label") ?? "");
  const press = async (key: string) => {
    const before = await now();
    await page.keyboard.press(key);
    await drawn(page);
    return (await now()) !== before;
  };
  await press("Home");
  for (let row = 0; row < 12 && !(await at()); row++) {
    for (const key of ["ArrowRight", "ArrowLeft"]) while (!(await at()) && (await press(key)));
    if (!(await at()) && !(await press("ArrowDown"))) break;
  }
  if (!(await at())) throw new Error(`the arrow keys never reached ${target.toString()}`);
}

/**
 * The editor's whole text. CodeMirror draws only the lines near its
 * viewport, so the view's state is read where EditorView.findFromDOM finds
 * it (the content's tile); the lines drawn if a CodeMirror update moves it.
 */
const yamlOf = (page: Page) =>
  page.evaluate(() => {
    type View = { state: { doc: { toString(): string } } };
    const content = document.querySelector(".cm-content")!;
    const tile = (content as unknown as { cmTile?: { view?: View; root?: { view?: View } } }).cmTile;
    const view = tile?.view ?? tile?.root?.view;
    if (view) return view.state.doc.toString();
    return [...content.querySelectorAll(".cm-line")].map((l) => l.textContent).join("\n");
  });
const blueprintOf = async (page: Page) =>
  (parse(await yamlOf(page)) as { blueprint: Record<string, unknown> }).blueprint;

const item = "Web shop";

/** What the build gives, as the designer writes it. */
const built = {
  kind: "vm",
  template: "tpl-rhel9.4-2026.10",
  guest: { os_family: "linux", domain: "corp.example" },
  components: [
    { key: "web", bindings: { network: '"dvpg-web"' } },
    {
      key: "db",
      title: "Database",
      bindings: { network: '"dvpg-web"' },
      disks: [{ key: "data", size_gb: 100 }],
      depends_on: ["web"],
    },
  ],
};
const builtYaml = `form_schema:
  type: object
  required: [name]
  properties:
    name: {type: string, minLength: 1}
ui_schema:
  layout:
    kind: page
    sections: [{key: main, title: ${item}, columns: 1, fields: [name]}]
  fields:
    name: {widget: text, label: Name}
initial_values: {}
blueprint:
  kind: vm
  template: tpl-rhel9.4-2026.10
  guest:
    os_family: linux
    domain: corp.example
  components:
    - key: web
      bindings:
        network: '"dvpg-web"'
    - key: db
      title: Database
      bindings:
        network: '"dvpg-web"'
      disks:
        - key: data
          size_gb: 100
      depends_on: [web]
`;

interface Words {
  newItem: string;
  name: string;
  slug: string;
  summary: string;
  type: string;
  vmType: string;
  create: string;
  definition: string;
  topology: string;
  palette: string;
  vm: string;
  network: string;
  disk: string;
  addVm: string;
  addNetwork: string;
  addDisk: string;
  firstKey: string;
  key: string;
  title: string;
  size: string;
  to: string;
  add: string;
  selected: (title: string) => RegExp;
  connectTo: string;
  connect: string;
}

const english: Words = {
  newItem: "New item",
  name: "Name",
  slug: "Slug",
  summary: "Summary",
  type: "Type",
  vmType: "VM blueprint",
  create: "Create",
  definition: "Definition",
  topology: `Topology of ${item}`,
  palette: "Add to the topology",
  vm: "VM",
  network: "Network",
  disk: "Disk",
  addVm: "Add a VM",
  addNetwork: "Add a network",
  addDisk: "Add a disk",
  firstKey: "Key of the VM there is",
  key: "Key",
  title: "Title",
  size: "Size in GB",
  to: "Connected to",
  add: "Add",
  selected: (title) => new RegExp(`^Selected: ${title}$`),
  connectTo: "Connect to",
  connect: "Connect",
};

const french: Words = {
  newItem: "Nouvel élément",
  name: "Nom",
  slug: "Slug",
  summary: "Résumé",
  type: "Type",
  vmType: "Blueprint de VM",
  create: "Créer",
  definition: "Définition",
  topology: `Topologie de ${item}`,
  palette: "Ajouter à la topologie",
  vm: "VM",
  network: "Réseau",
  disk: "Disque",
  addVm: "Ajouter une VM",
  addNetwork: "Ajouter un réseau",
  addDisk: "Ajouter un disque",
  firstKey: "Clé de la VM existante",
  key: "Clé",
  title: "Titre",
  size: "Taille en Go",
  to: "Connecté à",
  add: "Ajouter",
  selected: (title) => new RegExp(`^Sélection\\s:\\s${title}$`),
  connectTo: "Connecter à",
  connect: "Connecter",
};

/** Fills the focused dialog's text field: what it holds replaced. */
async function typeInto(page: Page, field: Locator, value: string) {
  await expect(field).toBeFocused();
  await page.keyboard.press("Control+A");
  await page.keyboard.type(value);
}

/**
 * From an empty draft, with the keyboard only: the item's VM becomes `web`,
 * a second VM `db` is added, a network and a disk added to it; then `web`
 * is put on the network and built after `db`, from its details.
 */
async function buildOnCanvas(page: Page, w: Words, slug: string) {
  await skipToContent(page, w === english ? "Skip to content" : "Aller au contenu");
  await tabTo(page, page.getByRole("button", { name: w.newItem }));
  await page.keyboard.press("Enter");
  const create = page.getByRole("dialog", { name: w.newItem });
  await tabTo(page, create.getByRole("textbox", { name: w.name }), "Tab");
  await page.keyboard.type(item);
  await tabTo(page, create.getByRole("textbox", { name: w.slug }));
  await page.keyboard.type(slug);
  await tabTo(page, create.getByRole("textbox", { name: w.summary }));
  await page.keyboard.type("A web tier and its database.");
  await tabTo(page, create.getByRole("combobox", { name: w.type }));
  await choose(page, w.vmType);
  await tabTo(page, create.getByRole("button", { name: w.create }), "Tab");
  await page.keyboard.press("Enter");
  await expect(page.getByRole("heading", { name: item, level: 1 })).toBeVisible();
  await selectTab(page, w.definition, "ArrowRight");
  const topology = page.getByRole("region", { name: w.topology });
  const palette = page.getByRole("group", { name: w.palette });
  const node = (name: RegExp) => topology.getByRole("button", { name });

  // A second VM: the item's own becomes the first component.
  await tabTo(page, palette.getByRole("button", { name: w.vm, exact: true }));
  await page.keyboard.press("Enter");
  let dialog = page.getByRole("dialog", { name: w.addVm });
  await typeInto(page, dialog.getByRole("textbox", { name: w.firstKey }), "web");
  await page.keyboard.press("Tab");
  await typeInto(page, dialog.getByRole("textbox", { name: w.key, exact: true }), "db");
  await page.keyboard.press("Tab");
  await typeInto(page, dialog.getByRole("textbox", { name: w.title }), "Database");
  await page.keyboard.press("Enter");
  await expect(dialog).toBeHidden();
  await expect(page.getByRole("heading", { name: /Database$/, level: 4 })).toBeFocused();
  await expect(node(/^\d+ VMs? web\./)).toBeVisible();

  // A network for the selected VM: what it is connected to is that one.
  await tabTo(page, palette.getByRole("button", { name: w.network, exact: true }), "Shift+Tab");
  await page.keyboard.press("Enter");
  dialog = page.getByRole("dialog", { name: w.addNetwork });
  await expect(dialog.getByRole("combobox", { name: w.to })).toHaveText(/^Database/);
  await typeInto(page, dialog.getByRole("textbox", { name: w.network }), '"dvpg-web"');
  await page.keyboard.press("Enter");
  await expect(dialog).toBeHidden();
  await expect(node(/dvpg-web\./)).toBeVisible();

  // A disk, chosen for db in the list.
  await tabTo(page, palette.getByRole("button", { name: w.disk, exact: true }), "Shift+Tab");
  await page.keyboard.press("Enter");
  dialog = page.getByRole("dialog", { name: w.addDisk });
  await typeInto(page, dialog.getByRole("textbox", { name: w.size }), "100");
  await page.keyboard.press("Tab");
  await typeInto(page, dialog.getByRole("textbox", { name: w.key, exact: true }), "data");
  await tabTo(page, dialog.getByRole("combobox", { name: w.to }));
  await choose(page, /^Database/);
  await expect(dialog.getByRole("combobox", { name: w.to })).toHaveText(/^Database/);
  await tabTo(page, dialog.getByRole("button", { name: w.add, exact: true }));
  await page.keyboard.press("Enter");
  await expect(dialog).toBeHidden();
  await expect(node(/^\S+ data\./)).toBeVisible();

  // web, from its details: on the network, then built after db.
  await tabTo(page, topology.locator(".react-flow__node[tabindex='0']"), "Shift+Tab");
  await arrowTo(page, node(/^\d+ VMs? web\./));
  await page.keyboard.press("Enter");
  const web = page.getByRole("region", { name: w.selected("web") });
  await expect(web).toBeVisible();
  await tabTo(page, web.getByRole("combobox", { name: w.connectTo }));
  await choose(page, /^dvpg-web/);
  await tabTo(page, web.getByRole("button", { name: w.connect, exact: true }), "Tab");
  await page.keyboard.press("Enter");
  await expect(web.getByRole("combobox", { name: w.connectTo })).toBeFocused();
  await choose(page, /^Database/);
  await expect(web.locator("#vd-connect-says")).not.toBeEmpty();
  await tabTo(page, web.getByRole("button", { name: w.connect, exact: true }), "Tab");
  const before = await blueprintOf(page);
  await page.keyboard.press("Enter");
  await expect.poll(async () => (await blueprintOf(page)) !== before).toBe(true);
  return { topology, node };
}

test("a VM item built on the canvas from an empty draft, saved and published, with the keyboard only", async ({
  page,
}) => {
  test.slow();
  const errors: string[] = [];
  page.on("pageerror", (e) => errors.push(e.message));
  await page.goto("/admin/catalog-items");
  await buildOnCanvas(page, english, "web-shop");

  expect(await blueprintOf(page)).toEqual(built);

  // Saved, offered to Web Platform, published.
  await tabTo(page, page.getByRole("button", { name: "Save draft" }), "Shift+Tab");
  await page.keyboard.press("Enter");
  await expect(page.getByText(/^Draft [\d.]+ saved$/)).toBeVisible();
  await tabTo(page, page.getByRole("tab", { name: "Definition" }), "Shift+Tab");
  await selectTab(page, "Sharing", "ArrowRight");
  await tabTo(page, page.getByRole("checkbox", { name: "Web Platform" }));
  await page.keyboard.press("Space");
  await tabTo(page, page.getByRole("button", { name: "Save projects" }));
  await page.keyboard.press("Enter");
  await expect(page.getByText("Projects saved")).toBeVisible();
  await tabTo(page, page.getByRole("tab", { name: "Sharing" }), "Shift+Tab");
  await page.keyboard.press("Home");
  await expect(page.getByRole("tab", { name: "Versions" })).toHaveAttribute("aria-selected", "true");
  await tabTo(page, page.getByRole("button", { name: "Publish" }));
  await page.keyboard.press("Enter");
  await expect(page.getByText(/^[\d.]+ published$/)).toBeVisible();
  expect(errors).toEqual([]);
});

/** A new VM item, its YAML written into the editor, its Definition tab open, by clicks. */
async function designed(page: Page, slug: string, yaml = builtYaml) {
  await page.goto("/admin/catalog-items");
  await page.getByRole("button", { name: "New item" }).click();
  const create = page.getByRole("dialog", { name: "New item" });
  await create.getByRole("textbox", { name: "Name" }).fill(item);
  await create.getByRole("textbox", { name: "Slug" }).fill(slug);
  await create.getByRole("textbox", { name: "Summary" }).fill("A web tier and its database.");
  await create.getByRole("combobox", { name: "Type" }).click();
  await page.getByRole("option", { name: "VM blueprint" }).click();
  await create.getByRole("button", { name: "Create" }).click();
  await page.getByRole("tab", { name: "Definition" }).click();
  const editor = page.getByRole("textbox", { name: new RegExp(`^Definition of ${item}`) });
  await editor.click();
  await page.keyboard.press("Control+A");
  await page.keyboard.insertText(yaml);
  const topology = page.getByRole("region", { name: `Topology of ${item}` });
  await expect(topology.getByRole("button", { name: /^\S+ data\./ })).toBeVisible();
  return { editor, topology };
}

test("the item built requested with the keyboard only, and followed to Ready", async ({ page }) => {
  test.slow();
  const errors: string[] = [];
  page.on("pageerror", (e) => errors.push(e.message));
  await designed(page, "web-shop-requested");
  expect(await blueprintOf(page)).toEqual(built);
  await page.getByRole("button", { name: "Save draft" }).click();
  await expect(page.getByText(/^Draft [\d.]+ saved$/)).toBeVisible();
  await page.getByRole("tab", { name: "Sharing" }).click();
  await page.getByRole("checkbox", { name: "Web Platform" }).click();
  await page.getByRole("button", { name: "Save projects" }).click();
  await expect(page.getByText("Projects saved")).toBeVisible();
  await page.getByRole("tab", { name: "Versions" }).click();
  await page.getByRole("button", { name: "Publish" }).click();
  await expect(page.getByText(/^[\d.]+ published$/)).toBeVisible();

  await tabTo(page, page.getByRole("link", { name: "Catalog", exact: true }), "Shift+Tab");
  await page.keyboard.press("Enter");
  await skipToContent(page);
  await tabTo(page, page.getByRole("link", { name: new RegExp(`^Request ${item}`) }));
  await page.keyboard.press("Enter");
  await tabTo(page, page.getByRole("textbox", { name: "Name", exact: true }));
  await page.keyboard.type("shop-01");
  await tabTo(page, page.getByRole("button", { name: "Submit request" }));
  await page.keyboard.press("Enter");
  await expect(page).toHaveURL(/\/requests\/REQ-\d+$/);
  await expect(page.getByText("Succeeded", { exact: true }).first()).toBeVisible({ timeout: 30_000 });
  await expect(page.getByRole("region", { name: "Steps" })).toContainText("Ready");
  expect(errors).toEqual([]);
});

test("the canvas follows the YAML: a title typed keeps the node where it was, a VM typed is drawn, and YAML it can't tell apart leaves it read-only", async ({
  page,
}) => {
  const errors: string[] = [];
  page.on("pageerror", (e) => errors.push(e.message));
  const { topology } = await designed(page, "web-shop-typed");
  const db = topology.getByRole("button", { name: /^\d+ VMs? Database\./ });
  const where = await db.boundingBox();

  const editor = page.getByRole("textbox", { name: new RegExp(`^Definition of ${item}`) });
  await editor.click();
  await page.keyboard.press("Control+A");
  await page.keyboard.insertText(builtYaml.replace("title: Database", "title: Orders database"));
  const orders = topology.getByRole("button", { name: /^\d+ VMs? Orders database\./ });
  await expect(orders).toBeVisible();
  expect(await orders.boundingBox()).toEqual(where);

  await page.keyboard.press("Control+A");
  await page.keyboard.insertText(`${builtYaml}    - key: cache\n      count: 2\n`);
  await expect(topology.getByRole("button", { name: /^2 VMs cache\./ })).toBeVisible();

  await page.keyboard.press("Control+A");
  await page.keyboard.insertText(`${builtYaml}    - key: db\n`);
  await expect(
    page.getByText(
      "The canvas can't edit this YAML: Two entries of components have the key db. Edit it in the YAML; the canvas follows.",
    ),
  ).toBeVisible();
  await expect(page.getByRole("group", { name: "Add to the topology" })).toHaveCount(0);
  await page.keyboard.press("Control+Z");
  await expect(page.getByRole("group", { name: "Add to the topology" })).toBeVisible();
  expect(errors).toEqual([]);
});

test("the same build in French, with the keyboard only, and axe in both themes", async ({ page }) => {
  test.slow();
  const errors: string[] = [];
  page.on("pageerror", (e) => errors.push(e.message));
  await page.goto("/");
  await expect(page.getByRole("heading", { name: "Overview", level: 1 })).toBeVisible();
  await page.getByRole("button", { name: /^Account/ }).click();
  await page.getByRole("menuitem", { name: "Français" }).click();
  await expect(page.locator("html")).toHaveAttribute("lang", "fr");
  await page
    .getByRole("navigation", { name: "Principale" })
    .getByRole("link", { name: "Éléments du catalogue" })
    .click();
  await expect(page.getByRole("heading", { name: "Éléments du catalogue", level: 1 })).toBeVisible();
  await buildOnCanvas(page, french, "boutique");
  expect(await blueprintOf(page)).toEqual(built);
  await expect(page.getByRole("region", { name: french.selected("web") })).toContainText("Connexions");
  await page.mouse.move(0, 0);
  await expect(page.locator("[data-sonner-toast]")).toHaveCount(0, { timeout: 25_000 });
  for (const [name, t] of [
    ["Clair", "light"],
    ["Sombre", "dark"],
  ] as const) {
    await page.getByRole("button", { name: /^Thème/ }).click();
    await page.getByRole("menuitem", { name }).click();
    await expect(page.locator("html")).toHaveAttribute("data-theme", t);
    await page.waitForTimeout(500);
    await scan(page, `the VM designer in French, ${t}`);
  }
  expect(errors).toEqual([]);
});

test("axe, both themes: the designer with a VM's details, and the add dialog", async ({ page }) => {
  const { topology } = await designed(page, "web-shop-axe");
  await topology.getByRole("button", { name: /^\d+ VMs? web\./ }).click();
  await expect(page.getByRole("region", { name: "Selected: web" })).toBeVisible();
  await expect(page.locator("[data-sonner-toast]")).toHaveCount(0, { timeout: 25_000 });
  await bothThemes(page, "the VM designer, web selected");
  await page
    .getByRole("group", { name: "Add to the topology" })
    .getByRole("button", { name: "Disk", exact: true })
    .click();
  await expect(page.getByRole("dialog", { name: "Add a disk" })).toBeVisible();
  // The dialog is modal: the theme menu is out of reach until it closes.
  await scan(page, "the add dialog, light");
});
