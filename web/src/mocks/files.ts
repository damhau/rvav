// File fields in the mock backend (F1.2, ADR-0040): a file is checked for
// its field's types and size, and its first bytes for what its name says,
// kept in memory, and sent back as a download.
import { http, HttpResponse } from "msw";
import type { components } from "@/lib/api/schema";
import { designerForm } from "./catalogAdmin";
import type { Db } from "./seed";

type S = components["schemas"];
type Definition = S["CatalogVersionDefinition"];

/** The types rvav takes, by extension, as internal/core/files.go has them. */
const kinds: Record<string, string> = {
  ".pdf": "application/pdf",
  ".png": "image/png",
  ".jpg": "image/jpeg",
  ".jpeg": "image/jpeg",
  ".gif": "image/gif",
  ".webp": "image/webp",
  ".txt": "text/plain",
  ".log": "text/plain",
  ".csv": "text/csv",
  ".md": "text/markdown",
  ".json": "application/json",
  ".yaml": "application/yaml",
  ".yml": "application/yaml",
  ".xml": "application/xml",
  ".sh": "application/x-sh",
  ".ps1": "text/x-powershell",
  ".pem": "application/x-pem-file",
  ".crt": "application/x-x509-ca-cert",
  ".cer": "application/x-x509-ca-cert",
  ".zip": "application/zip",
  ".gz": "application/gzip",
  ".tgz": "application/gzip",
  ".docx": "application/vnd.openxmlformats-officedocument.wordprocessingml.document",
  ".xlsx": "application/vnd.openxmlformats-officedocument.spreadsheetml.sheet",
  ".pptx": "application/vnd.openxmlformats-officedocument.presentationml.presentation",
};

const accepts = (entry: string, mime: string) => {
  const e = entry.trim().toLowerCase();
  if (e in kinds) return kinds[e] === mime;
  if (e.endsWith("/*")) return mime.startsWith(e.slice(0, -1));
  return e === mime;
};
const known = (entry: string) => Object.values(kinds).some((m) => accepts(entry, m));
const extension = (name: string) => (name.includes(".") ? name.slice(name.lastIndexOf(".")).toLowerCase() : "");

/** What a file's first bytes say it is, as far as the checks need. */
function sniffed(b: Uint8Array): string {
  const starts = (s: string) => [...s].every((c, i) => b[i] === c.charCodeAt(0));
  if (starts("%PDF-")) return "application/pdf";
  if (starts("\x89PNG")) return "image/png";
  if (starts("\xff\xd8\xff")) return "image/jpeg";
  if (starts("GIF8")) return "image/gif";
  if (starts("PK\x03\x04")) return "application/zip";
  if (starts("\x1f\x8b")) return "application/gzip";
  const head = new TextDecoder().decode(b.slice(0, 512)).trimStart().toLowerCase();
  if (head.startsWith("<!doctype html") || head.startsWith("<html") || head.startsWith("<script")) return "text/html";
  return b.slice(0, 512).some((c) => c < 9) ? "application/octet-stream" : "text/plain";
}

const textual = (mime: string) =>
  mime.startsWith("text/") ||
  [
    "application/json",
    "application/yaml",
    "application/xml",
    "application/x-sh",
    "application/x-pem-file",
    "application/x-x509-ca-cert",
  ].includes(mime);
const agrees = (mime: string, seen: string) =>
  seen === mime ||
  (textual(mime) && seen === "text/plain") ||
  (mime.startsWith("application/vnd.openxmlformats") && seen === "application/zip");

/** As the server's checkFile: types it knows, a size it takes, nothing an approver or the form fills in. */
export function fileProblems(def: Definition, prefix: string, publish: boolean): S["FieldError"][] {
  const errors: S["FieldError"][] = [];
  for (const [name, f] of Object.entries(def.ui_schema.fields)) {
    if (f.widget !== "file") continue;
    const at = `${prefix}/ui_schema/fields/${name}`;
    (f.accept ?? []).forEach((a, i) => {
      if (!known(a))
        errors.push({ field: `${at}/accept/${i}`, code: "unknown", message: `rvav doesn't take ${a} files.` });
    });
    if (publish && !f.accept?.length)
      errors.push({ field: `${at}/accept`, code: "required", message: "Say which types of file the field takes." });
    if (f.max_size_mb !== undefined && (f.max_size_mb < 1 || f.max_size_mb > 25))
      errors.push({
        field: `${at}/max_size_mb`,
        code: "out_of_range",
        message: "A file field takes files of 1 to 25 MB.",
      });
    if (f.approver_editable)
      errors.push({
        field: `${at}/approver_editable`,
        code: "not_allowed",
        message: "An approver can't change a file the requester attached.",
      });
    if (f.secret)
      errors.push({
        field: `${at}/secret`,
        code: "not_allowed",
        message: "A file is kept as it is sent, not sealed: a file field can't be secret.",
      });
    if (name in def.initial_values)
      errors.push({
        field: `${prefix}/initial_values/${name}`,
        code: "not_allowed",
        message: "A file field starts empty: the requester attaches the file.",
      });
  }
  return errors;
}

function problem(status: number, type: string, title: string, detail?: string) {
  return HttpResponse.json(
    { type: `https://rvav.dev/problems/${type}`, title, status, request_id: "req_mock", ...(detail ? { detail } : {}) },
    { status, headers: { "Content-Type": "application/problem+json" } },
  );
}

const hex = (b: ArrayBuffer) => [...new Uint8Array(b)].map((x) => x.toString(16).padStart(2, "0")).join("");

export function fileHandlers(db: Db, base: string, wait: () => Promise<void>) {
  const kept = new Map<string, { ref: S["FileRef"]; content: Uint8Array }>();
  return [
    http.post(`${base}/forms/files`, async ({ request }) => {
      await wait();
      const q = new URL(request.url).searchParams;
      const project = db.projects.find((p) => p.id === q.get("project_id"));
      const versionId = q.get("version_id") ?? "";
      const form =
        (project && designerForm(db, versionId, project.code, undefined)?.form) ??
        Object.values(db.forms).find((f) => f.version_id === versionId);
      if (!project || !form) return problem(404, "not-found", "Not found");
      const field = form.ui_schema.fields[q.get("field") ?? ""];
      if (field?.widget !== "file")
        return problem(
          400,
          "bad-request",
          "The request is not valid",
          `The form has no file field "${q.get("field")}".`,
        );
      const name = (q.get("name") ?? "").replaceAll("\\", "/").split("/").pop()?.trim() ?? "";
      const max = (field.max_size_mb ?? 10) * 1024 * 1024;
      const takes = `${field.label} takes ${(field.accept ?? []).join(", ")} files of ${field.max_size_mb ?? 10} MB at most.`;
      const content = new Uint8Array(await request.arrayBuffer());
      if (content.length > max)
        return problem(
          413,
          "too-large",
          "The file is too large",
          `${name} is larger than ${max >> 20} MB, the most ${field.label} takes.`,
        );
      const mime = kinds[extension(name)];
      if (!mime || !(field.accept ?? []).some((a) => accepts(a, mime)))
        return problem(415, "unsupported-type", "This type of file isn't taken", takes);
      if (content.length === 0) return problem(400, "bad-request", "The request is not valid", `${name} is empty.`);
      const seen = sniffed(content);
      if (!agrees(mime, seen))
        return problem(
          415,
          "unsupported-type",
          "This type of file isn't taken",
          `${name} isn't what a ${extension(name)} file holds: its content is ${seen}.`,
        );
      const ref: S["FileRef"] = {
        id: `fil_${crypto.randomUUID().replaceAll("-", "").slice(0, 20)}`,
        name,
        size: content.length,
        content_type: mime,
        sha256: hex(await crypto.subtle.digest("SHA-256", content)),
      };
      kept.set(ref.id, { ref, content });
      return HttpResponse.json(ref, { status: 201 });
    }),
    http.get(`${base}/files/:fileId`, async ({ params }) => {
      await wait();
      const f = kept.get(String(params.fileId));
      if (!f) return problem(404, "not-found", "Not found");
      return new HttpResponse(f.content, {
        headers: {
          "Content-Type": "application/octet-stream",
          "Content-Disposition": `attachment; filename="${f.ref.name}"`,
          "X-Content-Type-Options": "nosniff",
        },
      });
    }),
  ];
}
