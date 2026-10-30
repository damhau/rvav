// The catalog's Git repository in the mock backend (ADR-0029): each item's
// file, rvav's changes waiting to be committed, a conflict with both sides,
// a commit that fails, a file the import refused, a read on demand and a
// conflict settled.
import { http, HttpResponse } from "msw";
import type { components } from "@/lib/api/schema";
import type { Db, StoredItem } from "./seed";

type S = components["schemas"];

type File = S["CatalogItemGit"] & {
  itemId?: string;
  /** The repository's file rvav didn't take. */
  repository?: string;
};

interface GitState {
  status: Omit<S["CatalogGitStatus"], "files">;
  files: Record<string, File>; // by path
}

/** What the catalog's Git mock needs of the catalog's. */
export interface GitHooks {
  /** The item's file as rvav commits it, or a version's (a draft's). */
  fileOf: (item: StoredItem, versionId?: string) => string | undefined;
  /** A draft of the repository's change: its ID. */
  draftFromRepository: (item: StoredItem) => string;
  adminItem: (item: StoredItem) => S["AdminCatalogItem"];
}

const sha = (seed: string) => {
  let h = 2166136261;
  return Array.from({ length: 40 }, (_, i) => {
    h = Math.imul(h ^ seed.charCodeAt(i % seed.length), 16777619) ^ i;
    return "0123456789abcdef"[(h >>> 4) & 15];
  }).join("");
};
const ago = (minutes: number) => new Date(Date.now() - minutes * 60_000).toISOString();
const lea = { author: "Léa Martin", email: "lea.martin@corp.example" };
const thomas = { author: "Thomas Nguyen", email: "thomas.nguyen@corp.example" };

export function gitState(db: Db): GitState {
  const d = db as Db & { catalogGit?: GitState };
  if (!d.catalogGit) {
    const files: Record<string, File> = {};
    const natives = db.items.filter((i) => i.source.kind === "native");
    natives.forEach((it, i) => {
      const path = `catalog/${it.slug}.yaml`;
      files[path] = {
        path,
        state: "synced",
        attempts: 0,
        itemId: it.id,
        last_commit: {
          sha: sha(path),
          message: `Publish ${it.slug} ${it.version.number}`,
          ...lea,
          when: ago(60 * (i + 2)),
        },
      };
    });
    // A change on both sides, and a commit the Git server refuses.
    const [, both, failing] = natives;
    if (both) {
      const f = files[`catalog/${both.slug}.yaml`]!;
      Object.assign(f, {
        state: "conflict",
        note: "Changed in the repository since rvav last read it.",
        last_commit: { sha: sha(both.slug + "theirs"), message: "Smaller default size", ...thomas, when: ago(25) },
        repository: "",
      });
    }
    if (failing) {
      Object.assign(files[`catalog/${failing.slug}.yaml`]!, {
        state: "failing",
        attempts: 3,
        error: "authentication required: the token was refused (HTTP 401)",
        next_attempt_at: new Date(Date.now() + 4 * 60_000).toISOString(),
      });
    }
    files["catalog/redis-cache.yaml"] = {
      path: "catalog/redis-cache.yaml",
      state: "refused",
      attempts: 0,
      problems: [{ field: "/metadata/category", code: "unknown", message: 'There is no category "caching". (line 7)' }],
      last_commit: { sha: sha("redis"), message: "Add a Redis cache", ...thomas, when: ago(40) },
    };
    d.catalogGit = {
      status: {
        configured: true,
        url: "https://git.corp.example/infra/rvav-catalog.git",
        branch: "main",
        dir: "catalog",
        head: sha("head"),
        read_at: ago(3),
        read_requested: false,
      },
      files,
    };
  }
  return d.catalogGit;
}

const present = (f: File): S["CatalogItemGit"] => {
  const { itemId: _, repository: __, ...rest } = f;
  return rest;
};

/** The item's file in the repository. */
export function gitOf(db: Db, item: StoredItem): S["CatalogItemGit"] | undefined {
  const f = Object.values(gitState(db).files).find((x) => x.itemId === item.id);
  return f && present(f);
}

/** rvav has a change of the item to commit (publish, deprecate, details). */
export function markGit(db: Db, item: StoredItem) {
  if (item.source.kind !== "native") return;
  const st = gitState(db);
  const f = Object.values(st.files).find((x) => x.itemId === item.id);
  if (!f) {
    const path = `catalog/${item.slug}.yaml`;
    st.files[path] = { path, state: "waiting", attempts: 0, itemId: item.id };
  } else if (f.state !== "conflict") {
    Object.assign(f, { state: "waiting", attempts: 0, error: undefined, next_attempt_at: undefined });
  }
}

/** The lines from a to b (a naive diff: enough for the mock). */
function diffLines(a: string, b: string): S["DiffLine"][] {
  const x = a.split("\n").filter(Boolean);
  const y = b.split("\n").filter(Boolean);
  const out: S["DiffLine"][] = [];
  let i = 0;
  let j = 0;
  while (i < x.length || j < y.length) {
    if (i < x.length && j < y.length && x[i] === y[j]) {
      out.push({ op: "same", text: x[i++]! });
      j++;
    } else if (j < y.length && !x.slice(i).includes(y[j]!)) out.push({ op: "added", text: y[j++]! });
    else if (i < x.length) out.push({ op: "removed", text: x[i++]! });
    else out.push({ op: "added", text: y[j++]! });
  }
  return out;
}

export function catalogGitHandlers(db: Db, base: string, wait: () => Promise<void>, hooks: GitHooks) {
  const find = (id: string) => db.items.find((i) => i.id === id || i.slug === id);
  const fileOfItem = (item: StoredItem) => Object.values(gitState(db).files).find((f) => f.itemId === item.id);
  const status = (): S["CatalogGitStatus"] => {
    const st = gitState(db);
    return {
      ...st.status,
      files: Object.values(st.files)
        .sort((a, b) => a.path.localeCompare(b.path))
        .map((f) => {
          const it = f.itemId ? find(f.itemId) : undefined;
          return { ...present(f), ...(it ? { item: { id: it.id, slug: it.slug, name: it.name } } : {}) };
        }),
    };
  };
  const theirs = (item: StoredItem, f: File) => {
    if (f.repository) return f.repository;
    const ours = hooks.fileOf(item) ?? "";
    // The repository's side of the conflict: another default size.
    f.repository = ours.includes("default:")
      ? ours.replace(/default: .*/, "default: S")
      : ours.replace("summary:", "summary: Smaller by default.\n  was:");
    return f.repository;
  };
  return [
    http.get(`${base}/admin/catalog/git`, async () => {
      await wait();
      return HttpResponse.json(status());
    }),
    http.post(`${base}/admin/catalog/git/read`, async () => {
      await wait();
      const st = gitState(db);
      // The repository answers: what waits, or failed, is committed.
      for (const f of Object.values(st.files)) {
        if (f.state !== "waiting" && f.state !== "failing") continue;
        const it = f.itemId ? find(f.itemId) : undefined;
        f.last_commit = {
          sha: sha(f.path + Date.now()),
          message: `Publish ${it?.slug ?? f.path} ${it?.version.number ?? ""}`.trim(),
          author: db.me.user.name,
          when: new Date().toISOString(),
        };
        Object.assign(f, { state: "synced", attempts: 0, error: undefined, next_attempt_at: undefined });
        st.status.head = f.last_commit.sha;
      }
      st.status.read_at = new Date().toISOString();
      return HttpResponse.json(status());
    }),
    http.get(`${base}/admin/catalog/items/:id/git`, async ({ params }) => {
      await wait();
      const item = find(String(params.id));
      const f = item && fileOfItem(item);
      if (!item || !f) return HttpResponse.json({ status: 404, title: "Not found" }, { status: 404 });
      const ours = hooks.fileOf(item);
      const repository =
        f.state === "conflict"
          ? theirs(item, f)
          : f.state === "draft" && f.draft_version_id
            ? hooks.fileOf(item, f.draft_version_id)
            : undefined;
      return HttpResponse.json({
        file: present(f),
        ...(ours ? { rvav: ours } : {}),
        ...(repository ? { repository } : {}),
        diff: repository ? diffLines(ours ?? "", repository) : [],
      } satisfies S["CatalogItemGitDetail"]);
    }),
    http.post(`${base}/admin/catalog/items/:id/git/resolve`, async ({ params, request }) => {
      await wait();
      const item = find(String(params.id));
      const f = item && fileOfItem(item);
      if (!item || !f) return HttpResponse.json({ status: 404, title: "Not found" }, { status: 404 });
      if (f.state !== "conflict")
        return HttpResponse.json(
          {
            status: 409,
            title: "Conflict",
            detail: `${item.name}'s file isn't in conflict: there is nothing to settle.`,
          },
          { status: 409 },
        );
      const { take } = (await request.json()) as { take: string };
      if (take === "rvav") Object.assign(f, { state: "waiting", note: undefined, repository: undefined });
      else Object.assign(f, { state: "draft", note: undefined, draft_version_id: hooks.draftFromRepository(item) });
      return HttpResponse.json(hooks.adminItem(item));
    }),
  ];
}
