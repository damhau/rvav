// Request drafts in the mock backend (S11.6): the signed-in person's, kept
// without secrets as the server does.
import { http, HttpResponse } from "msw";
import type { components } from "@/lib/api/schema";
import type { Db } from "./seed";

type S = components["schemas"];

function drafts(db: Db): S["SavedDraft"][] {
  const d = db as Db & { drafts?: S["SavedDraft"][] };
  d.drafts ??= [];
  return d.drafts;
}

export function draftHandlers(db: Db, base: string, wait: () => Promise<void>) {
  const save = (id: string | null, w: S["SavedDraftWrite"]): S["SavedDraft"] | null => {
    const item = db.items.find((i) => i.id === w.draft.catalog_item_id);
    const project = db.projects.find((p) => p.id === w.draft.project_id);
    if (!item || !project) return null;
    const secrets = new Set(
      Object.entries(db.forms[item.id]?.ui_schema.fields ?? {})
        .filter(([, f]) => f.widget === "secret")
        .map(([k]) => k),
    );
    const now = new Date().toISOString();
    const existing = id ? drafts(db).find((d) => d.id === id) : undefined;
    const behalf = w.draft.on_behalf_of ? db.users.find((u) => u.id === w.draft.on_behalf_of) : undefined;
    const saved: S["SavedDraft"] = {
      id: existing?.id ?? `drf_${Math.random().toString(36).slice(2, 10)}`,
      name: w.name?.trim() || item.name,
      draft: {
        ...w.draft,
        inputs: Object.fromEntries(Object.entries(w.draft.inputs).filter(([k]) => !secrets.has(k))),
      },
      catalog_item: { id: item.id, slug: item.slug, name: item.name },
      project: { id: project.id, code: project.code, name: project.name },
      ...(behalf ? { on_behalf_of: behalf } : {}),
      created_at: existing?.created_at ?? now,
      updated_at: now,
    };
    const list = drafts(db);
    const at = list.findIndex((d) => d.id === saved.id);
    if (at >= 0) list.splice(at, 1);
    list.unshift(saved);
    return saved;
  };
  const missing = () =>
    HttpResponse.json(
      { type: "https://rvav.dev/problems/not-found", title: "Not found", status: 404, request_id: "req_mock" },
      { status: 404, headers: { "Content-Type": "application/problem+json" } },
    );
  const invalid = () =>
    HttpResponse.json(
      {
        type: "https://rvav.dev/problems/x",
        title: "Some inputs need attention",
        status: 422,
        request_id: "req_mock",
        errors: [{ field: "/draft/catalog_item_id", code: "unknown", message: "No such catalog item." }],
      },
      { status: 422, headers: { "Content-Type": "application/problem+json" } },
    );
  return [
    http.get(`${base}/me/drafts`, async () => {
      await wait();
      return HttpResponse.json({ items: drafts(db) });
    }),
    http.post(`${base}/me/drafts`, async ({ request }) => {
      await wait();
      const saved = save(null, (await request.json()) as S["SavedDraftWrite"]);
      return saved ? HttpResponse.json(saved, { status: 201 }) : invalid();
    }),
    http.get(`${base}/me/drafts/:id`, async ({ params }) => {
      await wait();
      const d = drafts(db).find((x) => x.id === params.id);
      return d ? HttpResponse.json(d) : missing();
    }),
    http.put(`${base}/me/drafts/:id`, async ({ params, request }) => {
      await wait();
      if (!drafts(db).some((x) => x.id === params.id)) return missing();
      const saved = save(String(params.id), (await request.json()) as S["SavedDraftWrite"]);
      return saved ? HttpResponse.json(saved) : invalid();
    }),
    http.delete(`${base}/me/drafts/:id`, async ({ params }) => {
      await wait();
      const list = drafts(db);
      const at = list.findIndex((x) => x.id === params.id);
      if (at < 0) return missing();
      list.splice(at, 1);
      return new HttpResponse(null, { status: 204 });
    }),
  ];
}
