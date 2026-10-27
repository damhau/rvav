// Catalog administration in the mock backend (ADR-0019): versions with
// publish, deprecate and roll back, entitlements, categories, YAML. A
// published version also becomes what the user-facing catalog serves.
import { http, HttpResponse } from "msw";
import { parse, stringify } from "yaml";
import type { components } from "@/lib/api/schema";
import type { Db, StoredItem } from "./seed";

type S = components["schemas"];
type Version = S["CatalogItemVersionDetail"];
type Definition = S["CatalogVersionDefinition"];

interface AdminState {
  versions: Record<string, Version[]>; // by item ID, newest first
  allProjects: Set<string>;
  sortOrder: Record<string, number>;
}

const now = () => new Date().toISOString();
const uid = (prefix: string) => `${prefix}_${Math.random().toString(36).slice(2, 12)}`;

const iconSet = [
  "server", "monitor", "database", "key-round", "shield", "globe", "lock", "users", "terminal", "workflow",
  "box", "container", "hard-drive", "network", "cloud", "cpu", "package", "wrench", "mail", "file-text",
  "archive", "git-branch", "layers", "activity",
]; // prettier-ignore

function blueprintFor(item: StoredItem): Record<string, unknown> {
  return item.type === "vm_blueprint"
    ? { kind: "vm", template: "tpl-rhel9.4-2026.10", guest: { os_family: "linux", domain: "corp.example" } }
    : { kind: "awx", awx: { title: item.name, job_template: item.source.awx_template_name ?? item.slug } };
}

function state(db: Db): AdminState {
  const d = db as Db & { catalogAdmin?: AdminState };
  if (!d.catalogAdmin) {
    const versions: Record<string, Version[]> = {};
    for (const item of db.items) {
      const form = db.forms[item.id];
      versions[item.id] = [
        {
          id: item.version.id,
          number: item.version.number,
          state: "published",
          changelog: "",
          created_at: item.version.published_at ?? now(),
          published_at: item.version.published_at ?? now(),
          deployments: db.deployments.filter((x) => x.catalog_item.id === item.id).length,
          definition: {
            form_schema: form?.schema ?? { type: "object", properties: {} },
            ui_schema: form?.ui_schema ?? { layout: { kind: "page", sections: [] }, fields: {} },
            initial_values: form?.initial_values ?? {},
            blueprint: blueprintFor(item),
          },
        },
      ];
    }
    d.catalogAdmin = {
      versions,
      allProjects: new Set(db.items.filter((i) => i.source.kind === "awx").map((i) => i.id)),
      sortOrder: Object.fromEntries(db.categories.map((c, i) => [c.key, i * 10])),
    };
  }
  return d.catalogAdmin;
}

const summary = (v: Version): S["CatalogItemVersion"] => {
  const { definition: _, ...rest } = v;
  return rest;
};

function adminItem(db: Db, item: StoredItem): S["AdminCatalogItem"] {
  const st = state(db);
  const versions = st.versions[item.id] ?? [];
  const published = versions.find((v) => v.state === "published");
  const draft = versions.find((v) => v.state === "draft");
  return {
    id: item.id,
    slug: item.slug,
    name: item.name,
    summary: item.summary,
    description_md: item.description_md ?? "",
    category: item.category,
    type: item.type,
    icon: item.icon,
    tags: item.tags ?? [],
    featured: item.featured ?? false,
    source: item.source,
    ...(published ? { published_version: summary(published) } : {}),
    ...(draft ? { draft_version: summary(draft) } : {}),
    versions: versions.map(summary),
    entitlements: entitlements(db, item),
    requested: db.requests.some((r) => r.request.catalog_item.id === item.id),
    created_at: versions.at(-1)?.created_at ?? now(),
  };
}

function entitlements(db: Db, item: StoredItem): S["CatalogEntitlements"] {
  return {
    all_projects: state(db).allProjects.has(item.id),
    project_ids: [...item.projectIds].sort(),
    projects: db.projects.map((p) => ({ id: p.id, code: p.code, name: p.name })),
  };
}

function problem(status: number, title: string, errors?: S["FieldError"][], detail?: string) {
  return HttpResponse.json(
    {
      type: "https://rvav.dev/problems/x",
      title,
      status,
      request_id: uid("req"),
      ...(detail ? { detail } : {}),
      ...(errors ? { errors } : {}),
    },
    { status, headers: { "Content-Type": "application/problem+json" } },
  );
}

/** The server's structural checks, enough for the UI: labels, sections, inputs. */
function check(def: Definition, prefix: string): S["FieldError"][] {
  const errors: S["FieldError"][] = [];
  const props = (def.form_schema as { properties?: Record<string, unknown> }).properties ?? {};
  const placed = new Set(def.ui_schema.layout.sections.flatMap((s) => s.fields));
  for (const [name, f] of Object.entries(def.ui_schema.fields)) {
    if (!f.label?.trim())
      errors.push({
        field: `${prefix}/ui_schema/fields/${name}/label`,
        code: "required",
        message: "Give the field a label.",
      });
    if (!(name in props) && f.widget !== "info")
      errors.push({
        field: `${prefix}/ui_schema/fields/${name}`,
        code: "unknown_input",
        message: `"${name}" is not an input of the form's schema.`,
      });
    if (!placed.has(name))
      errors.push({
        field: `${prefix}/ui_schema/fields/${name}`,
        code: "not_placed",
        message: `"${name}" is in no section, so it would never show.`,
      });
  }
  for (const name of Object.keys(props))
    if (!def.ui_schema.fields[name])
      errors.push({
        field: `${prefix}/form_schema/properties/${name}`,
        code: "no_ui_field",
        message: `"${name}" has no UI field, so nobody could fill it in.`,
      });
  return errors;
}

function nextNumber(versions: Version[]): string {
  const last = versions[0]?.number ?? "0.9";
  const m = /^(\d+)\.(\d+)$/.exec(last);
  return m ? `${m[1]}.${Number(m[2]) + 1}` : `${last}.1`;
}

function diff(
  path: string,
  a: unknown,
  b: unknown,
  area: S["CatalogVersionChange"]["area"],
): S["CatalogVersionChange"][] {
  const name = path.replace(/^\//, "");
  if (a && b && typeof a === "object" && typeof b === "object" && !Array.isArray(a) && !Array.isArray(b)) {
    const ao = a as Record<string, unknown>;
    const bo = b as Record<string, unknown>;
    return [...new Set([...Object.keys(ao), ...Object.keys(bo)])].sort().flatMap((k): S["CatalogVersionChange"][] => {
      const sub = `${path}/${k}`;
      if (!(k in ao)) return [{ area, path: sub, change: "added", to: bo[k], summary: `${sub.slice(1)} added` }];
      if (!(k in bo)) return [{ area, path: sub, change: "removed", from: ao[k], summary: `${sub.slice(1)} removed` }];
      return diff(sub, ao[k], bo[k], area);
    });
  }
  if (
    Array.isArray(a) &&
    Array.isArray(b) &&
    [...(a as unknown[]), ...(b as unknown[])].every((x) => typeof x !== "object")
  ) {
    const av = a as unknown[];
    const bv = b as unknown[];
    return [
      ...bv
        .filter((x) => !av.includes(x))
        .map((x) => ({ area, path, change: "added" as const, to: x, summary: `${name}: ${String(x)} added` })),
      ...av
        .filter((x) => !bv.includes(x))
        .map((x) => ({ area, path, change: "removed" as const, from: x, summary: `${name}: ${String(x)} removed` })),
    ];
  }
  if (JSON.stringify(a) === JSON.stringify(b)) return [];
  return [
    { area, path, change: "changed", from: a, to: b, summary: `${name}: ${JSON.stringify(a)} → ${JSON.stringify(b)}` },
  ];
}

/** Makes a version what the user-facing catalog serves. */
function serve(db: Db, item: StoredItem, v: Version | undefined) {
  if (!v) {
    item.version = { ...item.version, id: "", number: "" };
    return;
  }
  item.version = { id: v.id, number: v.number, published_at: v.published_at };
  db.forms[item.id] = {
    item_id: item.id,
    version_id: v.id,
    schema: v.definition.form_schema,
    ui_schema: v.definition.ui_schema,
    initial_values: v.definition.initial_values,
  };
}

export function catalogAdminHandlers(db: Db, base: string, wait: () => Promise<void>) {
  const find = (id: string) => db.items.find((i) => i.id === id || i.slug === id);
  const versionsOf = (item: StoredItem) => state(db).versions[item.id] ?? [];
  return [
    http.get(`${base}/admin/catalog/items`, async ({ request }) => {
      await wait();
      const url = new URL(request.url);
      const q = (url.searchParams.get("q") ?? "").toLowerCase();
      const category = url.searchParams.get("category");
      const source = url.searchParams.get("source");
      const st = url.searchParams.get("state");
      const items = db.items
        .filter((i) => !q || `${i.name} ${i.slug} ${i.summary}`.toLowerCase().includes(q))
        .filter((i) => !category || i.category === category)
        .filter((i) => !source || i.source.kind === source)
        .map((i) => adminItem(db, i))
        .filter((i) => !st || (st === "published") === Boolean(i.published_version))
        .sort((a, b) => a.name.localeCompare(b.name));
      return HttpResponse.json({ items });
    }),
    http.post(`${base}/admin/catalog/items`, async ({ request }) => {
      await wait();
      const c = (await request.json()) as S["CatalogItemCreate"];
      if (find(c.slug)) return problem(409, "Conflict", undefined, `The slug "${c.slug}" is taken.`);
      if (!/^[a-z0-9][a-z0-9-]{1,62}$/.test(c.slug))
        return problem(422, "Some inputs need attention", [
          { field: "/slug", code: "pattern", message: "Lower case letters, digits and dashes, 2 to 63 characters." },
        ]);
      const item: StoredItem = {
        id: uid("itm"),
        slug: c.slug,
        name: c.name,
        summary: c.summary,
        description_md: c.description_md ?? "",
        category: c.category,
        type: c.type,
        icon: c.icon ?? (c.type === "vm_blueprint" ? "server" : "terminal"),
        tags: c.tags ?? [],
        featured: false,
        source: { kind: "native" },
        version: { id: "", number: "" },
        approval: "never",
        favourite: false,
        requests_last_30_days: 0,
        projectIds: [],
      };
      const def: Definition = c.definition ?? {
        form_schema: { type: "object", required: ["name"], properties: { name: { type: "string", minLength: 1 } } },
        ui_schema: {
          layout: { kind: "page", sections: [{ key: "main", title: c.name, columns: 1, fields: ["name"] }] },
          fields: { name: { widget: "text", label: "Name" } },
        },
        initial_values: {},
        blueprint: blueprintFor(item),
      };
      db.items.push(item);
      state(db).versions[item.id] = [
        {
          id: uid("ver"),
          number: "1.0",
          state: "draft",
          changelog: "",
          created_at: now(),
          deployments: 0,
          definition: def,
        },
      ];
      return HttpResponse.json(adminItem(db, item), { status: 201 });
    }),
    http.get(`${base}/admin/catalog/items/:id`, async ({ params }) => {
      await wait();
      const item = find(String(params.id));
      return item ? HttpResponse.json(adminItem(db, item)) : problem(404, "Not found");
    }),
    http.patch(`${base}/admin/catalog/items/:id`, async ({ params, request }) => {
      await wait();
      const item = find(String(params.id));
      if (!item) return problem(404, "Not found");
      const u = (await request.json()) as S["CatalogItemUpdate"];
      if (u.icon && !iconSet.includes(u.icon))
        return problem(422, "Some inputs need attention", [
          { field: "/icon", code: "unknown", message: `"${u.icon}" is not one of rvav's icons.` },
        ]);
      Object.assign(item, Object.fromEntries(Object.entries(u).filter(([, v]) => v !== undefined)));
      return HttpResponse.json(adminItem(db, item));
    }),
    http.delete(`${base}/admin/catalog/items/:id`, async ({ params }) => {
      await wait();
      const item = find(String(params.id));
      if (!item) return problem(404, "Not found");
      if (db.requests.some((r) => r.request.catalog_item.id === item.id))
        return problem(
          409,
          "Conflict",
          undefined,
          `${item.name} was requested: withdraw it instead (deprecate its published version).`,
        );
      db.items.splice(db.items.indexOf(item), 1);
      return new HttpResponse(null, { status: 204 });
    }),
    http.post(`${base}/admin/catalog/items/:id/versions`, async ({ params, request }) => {
      await wait();
      const item = find(String(params.id));
      if (!item) return problem(404, "Not found");
      const versions = versionsOf(item);
      if (versions.some((v) => v.state === "draft"))
        return problem(409, "Conflict", undefined, `${item.name} already has a draft: change it, or discard it first.`);
      const c = (await request.json()) as S["CatalogItemVersionCreate"];
      const from =
        versions.find((v) => v.id === c.from_version_id) ??
        versions.find((v) => v.state === "published") ??
        versions[0];
      const v: Version = {
        id: uid("ver"),
        number: c.number ?? nextNumber(versions),
        state: "draft",
        changelog: c.changelog ?? "",
        created_at: now(),
        created_by: { id: db.me.user.id, name: db.me.user.name },
        deployments: 0,
        definition: structuredClone(c.definition ?? from!.definition),
      };
      versions.unshift(v);
      return HttpResponse.json(v, { status: 201 });
    }),
    http.get(`${base}/admin/catalog/items/:id/versions/:vid`, async ({ params }) => {
      await wait();
      const item = find(String(params.id));
      const v = item && versionsOf(item).find((x) => x.id === params.vid);
      return v ? HttpResponse.json(v) : problem(404, "Not found");
    }),
    http.put(`${base}/admin/catalog/items/:id/versions/:vid`, async ({ params, request }) => {
      await wait();
      const item = find(String(params.id));
      const v = item && versionsOf(item).find((x) => x.id === params.vid);
      if (!v) return problem(404, "Not found");
      if (v.state !== "draft")
        return problem(409, "Conflict", undefined, "Only a draft can change: make a draft from this version.");
      const u = (await request.json()) as S["CatalogItemVersionUpdate"];
      const errors = check(u.definition, "/definition");
      if (errors.length) return problem(422, "Some inputs need attention", errors);
      v.definition = u.definition;
      if (u.number) v.number = u.number;
      if (u.changelog !== undefined) v.changelog = u.changelog;
      return HttpResponse.json(v);
    }),
    http.delete(`${base}/admin/catalog/items/:id/versions/:vid`, async ({ params }) => {
      await wait();
      const item = find(String(params.id));
      const versions = item ? versionsOf(item) : [];
      const v = versions.find((x) => x.id === params.vid);
      if (!v) return problem(404, "Not found");
      if (v.state !== "draft") return problem(409, "Conflict", undefined, "Only a draft can be discarded.");
      versions.splice(versions.indexOf(v), 1);
      return new HttpResponse(null, { status: 204 });
    }),
    http.post(`${base}/admin/catalog/items/:id/versions/:vid/publish`, async ({ params, request }) => {
      await wait();
      const item = find(String(params.id));
      const v = item && versionsOf(item).find((x) => x.id === params.vid);
      if (!item || !v) return problem(404, "Not found");
      if (v.state === "published")
        return problem(409, "Conflict", undefined, `Version ${v.number} is already published.`);
      const body =
        request.headers.get("content-length") === "0"
          ? {}
          : ((await request.json().catch(() => ({}))) as S["CatalogItemVersionPublish"]);
      for (const x of versionsOf(item))
        if (x.state === "published") Object.assign(x, { state: "deprecated", deprecated_at: now() });
      if (v.state === "draft" && body.changelog !== undefined) v.changelog = body.changelog;
      Object.assign(v, { state: "published", published_at: now() });
      delete v.deprecated_at;
      serve(db, item, v);
      return HttpResponse.json(adminItem(db, item));
    }),
    http.post(`${base}/admin/catalog/items/:id/versions/:vid/deprecate`, async ({ params }) => {
      await wait();
      const item = find(String(params.id));
      const v = item && versionsOf(item).find((x) => x.id === params.vid);
      if (!item || !v) return problem(404, "Not found");
      if (v.state !== "published")
        return problem(409, "Conflict", undefined, "Only the published version can be deprecated.");
      Object.assign(v, { state: "deprecated", deprecated_at: now() });
      serve(db, item, undefined);
      return HttpResponse.json(adminItem(db, item));
    }),
    http.get(`${base}/admin/catalog/items/:id/compare`, async ({ params, request }) => {
      await wait();
      const item = find(String(params.id));
      const url = new URL(request.url);
      const from = item && versionsOf(item).find((x) => x.id === url.searchParams.get("from"));
      const to = item && versionsOf(item).find((x) => x.id === url.searchParams.get("to"));
      if (!from || !to) return problem(404, "Not found");
      const areas = ["form_schema", "ui_schema", "initial_values", "blueprint"] as const;
      const changes = areas.flatMap((a) => diff("", from.definition[a], to.definition[a], a));
      return HttpResponse.json({ from: summary(from), to: summary(to), changes } satisfies S["CatalogVersionDiff"]);
    }),
    http.get(`${base}/admin/catalog/items/:id/entitlements`, async ({ params }) => {
      await wait();
      const item = find(String(params.id));
      return item ? HttpResponse.json(entitlements(db, item)) : problem(404, "Not found");
    }),
    http.put(`${base}/admin/catalog/items/:id/entitlements`, async ({ params, request }) => {
      await wait();
      const item = find(String(params.id));
      if (!item) return problem(404, "Not found");
      const e = (await request.json()) as S["CatalogEntitlements"];
      const st = state(db);
      if (e.all_projects) st.allProjects.add(item.id);
      else st.allProjects.delete(item.id);
      item.projectIds = e.all_projects ? db.projects.map((p) => p.id) : [...e.project_ids];
      return HttpResponse.json({ ...entitlements(db, item), all_projects: e.all_projects, project_ids: e.project_ids });
    }),
    http.get(`${base}/admin/catalog/items/:id/export`, async ({ params, request }) => {
      await wait();
      const item = find(String(params.id));
      if (!item) return problem(404, "Not found");
      const only = new URL(request.url).searchParams.get("version_id");
      const versions = [...versionsOf(item)].reverse().filter((v) => !only || v.id === only);
      const doc = {
        apiVersion: "rvav.dev/v1",
        kind: "CatalogItem",
        metadata: {
          slug: item.slug,
          name: item.name,
          summary: item.summary,
          category: item.category,
          type: item.type,
          icon: item.icon,
        },
        versions: versions.map((v) => ({
          number: v.number,
          state: v.state,
          ...(v.changelog ? { changelog: v.changelog } : {}),
          ...v.definition,
        })),
      };
      return new HttpResponse(stringify(doc), { headers: { "Content-Type": "application/yaml" } });
    }),
    http.post(`${base}/admin/catalog/import`, async ({ request }) => {
      await wait();
      let doc: {
        metadata?: Record<string, string>;
        versions?: (Definition & { number: string; changelog?: string })[];
      };
      try {
        doc = parse(await request.text()) as typeof doc;
      } catch (e) {
        return problem(422, "Some inputs need attention", [
          { field: "", code: "yaml", message: `This is not valid YAML: ${String(e)}` },
        ]);
      }
      const into = new URL(request.url).searchParams.get("item_id");
      const versions = doc.versions ?? [];
      const last = versions.at(-1);
      if (!last)
        return problem(422, "Some inputs need attention", [
          { field: "/versions", code: "required", message: "The item has no version." },
        ]);
      const errors = versions.flatMap((v, i) => check(v, `/versions/${i}`));
      if (errors.length) return problem(422, "Some inputs need attention", errors);
      const def = (v: Definition): Definition => ({
        form_schema: v.form_schema,
        ui_schema: v.ui_schema,
        initial_values: v.initial_values ?? {},
        blueprint: v.blueprint,
      });
      if (into) {
        const item = find(into);
        if (!item) return problem(404, "Not found");
        const existing = versionsOf(item);
        if (existing.some((v) => v.state === "draft"))
          return problem(409, "Conflict", undefined, `${item.name} already has a draft.`);
        existing.unshift({
          id: uid("ver"),
          number: nextNumber(existing),
          state: "draft",
          changelog: last.changelog ?? "",
          created_at: now(),
          deployments: 0,
          definition: def(last),
        });
        return HttpResponse.json({ item_id: item.id, created: false, versions: existing.map(summary) });
      }
      const m = doc.metadata ?? {};
      if (find(m.slug ?? "")) return problem(409, "Conflict", undefined, `The slug "${m.slug}" is taken.`);
      const item: StoredItem = {
        id: uid("itm"), slug: m.slug ?? uid("item"), name: m.name ?? "Imported item", summary: m.summary ?? "", description_md: "",
        category: m.category ?? "compute", type: (m.type ?? "vm_blueprint") as StoredItem["type"], icon: m.icon ?? "server", tags: [],
        featured: false, source: { kind: "native" }, version: { id: "", number: "" }, approval: "never",
        favourite: false, requests_last_30_days: 0, projectIds: [],
      }; // prettier-ignore
      db.items.push(item);
      state(db).versions[item.id] = versions
        .map((v, i): Version => ({
          id: uid("ver"),
          number: v.number,
          state: i === versions.length - 1 ? "draft" : "deprecated",
          changelog: v.changelog ?? "",
          created_at: now(),
          deployments: 0,
          definition: def(v),
        })) // prettier-ignore
        .reverse();
      return HttpResponse.json({ item_id: item.id, created: true, versions: versionsOf(item).map(summary) });
    }),
    http.get(`${base}/admin/catalog/categories`, async () => {
      await wait();
      const st = state(db);
      return HttpResponse.json({
        items: db.categories
          .map((c) => ({
            key: c.key,
            name: c.name,
            sort_order: st.sortOrder[c.key] ?? 0,
            items: db.items.filter((i) => i.category === c.key).length,
          }))
          .sort((a, b) => a.sort_order - b.sort_order || a.key.localeCompare(b.key)),
      });
    }),
    http.post(`${base}/admin/catalog/categories`, async ({ request }) => {
      await wait();
      const c = (await request.json()) as S["AdminCatalogCategory"];
      if (db.categories.some((x) => x.key === c.key))
        return problem(409, "Conflict", undefined, `The category "${c.key}" exists.`);
      db.categories.push({ key: c.key, name: c.name });
      state(db).sortOrder[c.key] = c.sort_order ?? 0;
      return HttpResponse.json({ ...c, items: 0 }, { status: 201 });
    }),
    http.patch(`${base}/admin/catalog/categories/:key`, async ({ params, request }) => {
      await wait();
      const c = db.categories.find((x) => x.key === params.key);
      if (!c) return problem(404, "Not found");
      const u = (await request.json()) as S["AdminCatalogCategoryUpdate"];
      if (u.name) c.name = u.name;
      if (u.sort_order !== undefined) state(db).sortOrder[c.key] = u.sort_order;
      return HttpResponse.json({
        key: c.key,
        name: c.name,
        sort_order: state(db).sortOrder[c.key] ?? 0,
        items: db.items.filter((i) => i.category === c.key).length,
      });
    }),
    http.delete(`${base}/admin/catalog/categories/:key`, async ({ params }) => {
      await wait();
      const i = db.categories.findIndex((x) => x.key === params.key);
      if (i < 0) return problem(404, "Not found");
      if (db.items.some((x) => x.category === params.key))
        return problem(409, "Conflict", undefined, "Items are in this category: move them to another one first.");
      db.categories.splice(i, 1);
      return new HttpResponse(null, { status: 204 });
    }),
    http.get(`${base}/admin/catalog/icons`, () => HttpResponse.json({ items: iconSet })),
  ];
}
