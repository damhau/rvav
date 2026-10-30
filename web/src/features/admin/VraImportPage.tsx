// The vRA importer (P2.5, ADR-0027): read vRA, or the file vra-export
// wrote; see what an import would do (the dry run) and map vRA's projects;
// then import the catalog as drafts with its sharing, and adopt
// deployments. It can run again. vRA is only read.
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { Circle, CircleCheck, Download, FileUp, Hand, RefreshCw, TriangleAlert } from "lucide-react";
import { Fragment, useRef, useState } from "react";
import { toast } from "sonner";
import { ErrorState, SkeletonRows } from "@/components/common/feedback";
import { Time } from "@/components/common/misc";
import { EmptyState, Page, PageHeader, Panel } from "@/components/common/page";
import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import { Checkbox } from "@/components/ui/checkbox";
import { Dialog, DialogContent } from "@/components/ui/dialog";
import { Field, Textarea } from "@/components/ui/input";
import { Select } from "@/components/ui/select";
import { Tabs, TabsContent, TabsList, TabsTrigger } from "@/components/ui/tabs";
import { api, call, isApiError } from "@/lib/api/client";
import type { components } from "@/lib/api/schema";
import { adm } from "@/lib/query";
import { failed, td, th } from "./governance/common";

type S = components["schemas"];
type Plan = S["VraPlan"];
type Tone = "neutral" | "accent" | "ok" | "warn" | "bad" | "run" | "outline";

const actions: Record<S["VraItemPlan"]["action"], [string, Tone]> = {
  create: ["Create", "accent"],
  update: ["New draft", "run"],
  unchanged: ["Unchanged", "neutral"],
  conflict: ["Conflict", "bad"],
  skip: ["Not imported", "outline"],
};
const mapping: Record<S["VraItemPlan"]["status"], [string, Tone]> = {
  complete: ["Complete", "ok"],
  partial: ["Partial", "warn"],
  skipped: ["Nothing maps", "outline"],
};
const adoption: Record<S["VraDeploymentPlan"]["status"], [string, Tone]> = {
  adopt: ["To adopt", "accent"],
  adopted: ["Adopted", "ok"],
  skipped: ["Left in vRA", "outline"],
  failed: ["Refused", "bad"],
};
const levels: Record<S["VraNote"]["level"], [string, Tone]> = {
  info: ["Info", "neutral"],
  warning: ["Check", "warn"],
  unmapped: ["Left out", "bad"],
};
const groups: Record<string, [string, Tone]> = {
  A: ["A · Native", "ok"],
  B: ["B · AWX", "accent"],
  C: ["C · Custom workflow", "run"],
  D: ["D · Retire", "outline"],
};
const kinds: Record<string, string> = {
  workflow: "Workflow",
  action: "Action",
  abx_action: "ABX action",
  catalog_item: "Catalog item",
  subscription: "Subscription",
  day2_action: "Day-2 action",
  form_field: "Form field",
};
/** The Select value of "no mapping": by name, or none. */
const AUTO = "_auto";
const plural = (n: number, one: string, many = one + "s") => `${n} ${n === 1 ? one : many}`;

function Notes({ notes }: { notes: S["VraNote"][] }) {
  if (notes.length === 0) return <p className="text-xs text-faint">Nothing to note.</p>;
  return (
    <ul className="flex flex-col gap-1.5">
      {notes.map((n, i) => (
        <li key={i} className="flex flex-wrap items-baseline gap-x-2 gap-y-0.5 text-xs">
          <Badge tone={levels[n.level][1]}>{levels[n.level][0]}</Badge>
          <code className="font-mono text-[11px] text-faint">{n.where}</code>
          <span className="min-w-0 basis-full sm:basis-auto">{n.text}</span>
        </li>
      ))}
    </ul>
  );
}

/** What a row's notes hold, as a button that shows them. */
function NotesButton({ notes, open, onToggle }: { notes: S["VraNote"][]; open: boolean; onToggle: () => void }) {
  const check = notes.filter((n) => n.level === "warning").length;
  const out = notes.filter((n) => n.level === "unmapped").length;
  const parts = [check ? `${check} to check` : "", out ? `${out} left out` : ""].filter(Boolean);
  return (
    <Button size="sm" variant="ghost" aria-expanded={open} onClick={onToggle}>
      {parts.length ? parts.join(" · ") : plural(notes.length, "note")}
    </Button>
  );
}

function Summary({ plan }: { plan: Plan }) {
  const s = plan.summary;
  const stat = (label: string, n: number, tone?: string) => (
    <div className="flex flex-col">
      <span className={"tnum text-lg font-semibold " + (tone ?? "")}>{n}</span>
      <span className="text-xs text-muted">{label}</span>
    </div>
  );
  return (
    <div className="grid gap-4 md:grid-cols-2">
      <Panel title="Catalog">
        <div className="flex flex-wrap gap-x-8 gap-y-3">
          {stat("to create", s.items_create)}
          {stat("new drafts", s.items_update)}
          {stat("unchanged", s.items_unchanged)}
          {stat("not imported", s.items_blocked, s.items_blocked ? "text-warn" : "")}
          {stat("projects to share with", s.shares_add)}
        </div>
      </Panel>
      <Panel title="Deployments">
        <div className="flex flex-wrap gap-x-8 gap-y-3">
          {stat("to adopt", s.deployments_adopt)}
          {stat("adopted", s.deployments_adopted)}
          {stat("left in vRA", s.deployments_skipped, s.deployments_skipped ? "text-warn" : "")}
        </div>
      </Panel>
    </div>
  );
}

function ProjectsTab({ plan }: { plan: Plan }) {
  const qc = useQueryClient();
  const projects = useQuery(adm.projects("", false));
  const map = useMutation({
    mutationFn: ({ vra, rvav }: { vra: string; rvav: string | null }) =>
      call(
        api.PUT("/admin/vra/projects/{vraProjectId}", {
          params: { path: { vraProjectId: vra } },
          body: { rvav_project_id: rvav },
        }),
      ),
    onSuccess: () => void qc.invalidateQueries({ queryKey: ["admin", "vra", "plan"] }),
    onError: (e) => failed(e, "The project was not mapped"),
  });
  return (
    <div className="overflow-x-auto">
      <table className="w-full min-w-[640px] border-collapse text-[13px]" aria-label="vRA projects">
        <thead>
          <tr className="label text-left">
            <th scope="col" className={th}>
              vRA project
            </th>
            <th scope="col" className={th + " w-72"}>
              rvav project
            </th>
            <th scope="col" className={th}>
              Items shared
            </th>
            <th scope="col" className={th}>
              Deployments
            </th>
          </tr>
        </thead>
        <tbody>
          {plan.projects.map((p) => (
            <tr key={p.vra_id}>
              <td className={td}>
                <span className="font-medium">{p.name}</span>
                <div className="font-mono text-[11px] text-faint">{p.vra_id}</div>
              </td>
              <td className={td}>
                <Select
                  aria-label={`rvav project for ${p.name}`}
                  value={p.how === "mapped" ? (p.rvav_project_id ?? AUTO) : AUTO}
                  disabled={map.isPending}
                  onChange={(v) => map.mutate({ vra: p.vra_id, rvav: v === AUTO ? null : v })}
                  options={[
                    {
                      value: AUTO,
                      label: p.how === "same_name" ? `By name: ${p.rvav_project_name}` : "None: stays in vRA",
                    },
                    ...(projects.data?.items ?? []).map((x) => ({ value: x.id, label: x.name })),
                  ]}
                />
              </td>
              <td className={td + " tnum"}>{p.items}</td>
              <td className={td + " tnum"}>{p.deployments}</td>
            </tr>
          ))}
        </tbody>
      </table>
    </div>
  );
}

function ItemsTab({ plan }: { plan: Plan }) {
  const [open, setOpen] = useState<string | null>(null);
  const names = new Map(
    plan.projects.filter((p) => p.rvav_project_id).map((p) => [p.rvav_project_id!, p.rvav_project_name]),
  );
  return (
    <div className="overflow-x-auto">
      <table className="w-full min-w-[820px] border-collapse text-[13px]" aria-label="Cloud templates">
        <thead>
          <tr className="label text-left">
            <th scope="col" className={th}>
              Cloud template
            </th>
            <th scope="col" className={th}>
              Maps
            </th>
            <th scope="col" className={th}>
              Import
            </th>
            <th scope="col" className={th}>
              Shared with
            </th>
            <th scope="col" className={th}>
              Notes
            </th>
          </tr>
        </thead>
        <tbody>
          {plan.items.map((it) => (
            <Fragment key={it.blueprint_id}>
              <tr>
                <td className={td}>
                  <span className="font-medium">{it.name}</span>
                  <div className="font-mono text-[11px] text-faint">
                    {it.slug}
                    {it.version ? ` · ${it.version}` : ""}
                  </div>
                </td>
                <td className={td}>
                  <Badge tone={mapping[it.status][1]}>{mapping[it.status][0]}</Badge>
                </td>
                <td className={td}>
                  <Badge tone={actions[it.action][1]}>{actions[it.action][0]}</Badge>
                  {it.reason && <div className="mt-1 max-w-64 text-xs text-muted">{it.reason}</div>}
                </td>
                <td className={td + " text-xs"}>
                  {it.share_add.length > 0 && (
                    <div>Adds {it.share_add.map((id) => names.get(id) ?? id).join(", ")}</div>
                  )}
                  {it.share_unmapped.length > 0 && (
                    <div className="text-warn">Not {it.share_unmapped.join(", ")}: not mapped</div>
                  )}
                  {it.share_add.length === 0 && it.share_unmapped.length === 0 && (
                    <span className="text-faint">Nothing to add</span>
                  )}
                </td>
                <td className={td}>
                  <NotesButton
                    notes={it.notes}
                    open={open === it.blueprint_id}
                    onToggle={() => setOpen(open === it.blueprint_id ? null : it.blueprint_id)}
                  />
                </td>
              </tr>
              {open === it.blueprint_id && (
                <tr>
                  <td className={td + " bg-sunken"} colSpan={5}>
                    <Notes notes={it.notes} />
                  </td>
                </tr>
              )}
            </Fragment>
          ))}
        </tbody>
      </table>
    </div>
  );
}

function DeploymentsTab({ plan }: { plan: Plan }) {
  const [open, setOpen] = useState<string | null>(null);
  return (
    <div className="overflow-x-auto">
      <table className="w-full min-w-[820px] border-collapse text-[13px]" aria-label="vRA deployments">
        <thead>
          <tr className="label text-left">
            <th scope="col" className={th}>
              Deployment
            </th>
            <th scope="col" className={th}>
              Owner
            </th>
            <th scope="col" className={th}>
              VMs
            </th>
            <th scope="col" className={th}>
              Adoption
            </th>
            <th scope="col" className={th}>
              Notes
            </th>
          </tr>
        </thead>
        <tbody>
          {plan.deployments.map((d) => {
            const why = [...d.notes].reverse().find((n) => n.level === "unmapped");
            return (
              <Fragment key={d.vra_id}>
                <tr>
                  <td className={td}>
                    <span className="font-medium">{d.name}</span>
                    <div className="text-[11px] text-faint">
                      {d.vra_project}
                      {d.environment ? ` · ${d.environment}` : ""}
                    </div>
                  </td>
                  <td className={td + " text-xs"}>{d.owner}</td>
                  <td className={td + " font-mono text-xs"}>
                    {(d.vms ?? []).map((v) => v.name).join(", ") || <span className="text-faint">none found</span>}
                  </td>
                  <td className={td}>
                    <Badge tone={adoption[d.status][1]}>{adoption[d.status][0]}</Badge>
                    {why && <div className="mt-1 max-w-72 text-xs text-muted">{why.text}</div>}
                  </td>
                  <td className={td}>
                    <NotesButton
                      notes={d.notes}
                      open={open === d.vra_id}
                      onToggle={() => setOpen(open === d.vra_id ? null : d.vra_id)}
                    />
                  </td>
                </tr>
                {open === d.vra_id && (
                  <tr>
                    <td className={td + " bg-sunken"} colSpan={5}>
                      <Notes notes={d.notes} />
                    </td>
                  </tr>
                )}
              </Fragment>
            );
          })}
        </tbody>
      </table>
    </div>
  );
}

function PoliciesTab({ plan }: { plan: Plan }) {
  return (
    <div className="flex flex-col gap-3 p-4">
      <p className="max-w-3xl text-xs text-muted">
        Policies decide who may do what, so the import doesn't change rvav's: set each where it says, with the values
        below.
      </p>
      {plan.policies.map((p) => (
        <section key={p.vra_id} className="rounded-md border border-line p-3" aria-label={p.name}>
          <div className="flex flex-wrap items-center gap-2">
            <h3 className="text-[13px] font-medium">{p.name}</h3>
            <Badge tone="outline">{p.type}</Badge>
            <span className="text-xs text-muted">{p.vra_project ? `In ${p.vra_project}` : "Every project"}</span>
          </div>
          <p className="mt-1 mb-2 text-xs">Becomes: {p.becomes}</p>
          <Notes notes={p.notes} />
        </section>
      ))}
    </div>
  );
}

function VroTab({ plan }: { plan: Plan }) {
  const t = plan.triage;
  const download = useMutation({
    mutationFn: async () => {
      const text = await call(api.GET("/admin/vra/triage", { parseAs: "text" }));
      const url = URL.createObjectURL(new Blob([String(text)], { type: "text/markdown" }));
      Object.assign(document.createElement("a"), { href: url, download: "vro-triage.md" }).click();
      URL.revokeObjectURL(url);
    },
    onError: (e) => toast.error(isApiError(e) ? e.problem.title : "The report could not be made"),
  });
  return (
    <div className="flex flex-col">
      <div className="flex flex-wrap items-center gap-3 border-b border-line px-4 py-3 text-xs">
        {Object.entries(groups).map(([g, [label, tone]]) => (
          <span key={g} className="flex items-center gap-1.5">
            <Badge tone={tone}>{label}</Badge>
            <span className="tnum">{t.groups[g] ?? 0}</span>
          </span>
        ))}
        <span className="text-muted">vRO's own library ({t.library}) left out. Suggestions: people decide.</span>
        <Button
          className="ml-auto"
          size="sm"
          variant="ghost"
          disabled={download.isPending}
          onClick={() => download.mutate()}
        >
          <Download /> Report (Markdown)
        </Button>
      </div>
      <div className="overflow-x-auto">
        <table className="w-full min-w-[900px] border-collapse text-[13px]" aria-label="vRO workflows and actions">
          <thead>
            <tr className="label text-left">
              <th scope="col" className={th}>
                Name
              </th>
              <th scope="col" className={th}>
                Used by
              </th>
              <th scope="col" className={th}>
                Last run
              </th>
              <th scope="col" className={th}>
                Group
              </th>
              <th scope="col" className={th}>
                Why
              </th>
            </tr>
          </thead>
          <tbody>
            {t.items.map((it) => (
              <tr key={it.kind + it.id}>
                <td className={td}>
                  <span className="font-medium">{it.name}</span>
                  <div className="text-[11px] text-faint">
                    {kinds[it.kind] ?? it.kind}
                    {it.category && it.kind === "workflow" ? ` · ${it.category}` : ""}
                    {it.systems.length > 0 ? ` · ${it.systems.join(", ")}` : ""}
                  </div>
                </td>
                <td className={td + " text-xs"}>
                  {it.used_by.length === 0 && <span className="text-faint">Nothing</span>}
                  {it.used_by.map((u, i) => (
                    <div key={i} className={u.disabled ? "text-faint line-through" : ""}>
                      {kinds[u.kind] ?? u.kind} {u.name}
                    </div>
                  ))}
                </td>
                <td className={td + " text-xs whitespace-nowrap"}>
                  {it.kind !== "workflow" ? "–" : it.last_run ? <Time iso={it.last_run} /> : "never"}
                </td>
                <td className={td}>
                  <Badge tone={groups[it.group]?.[1] ?? "neutral"}>{groups[it.group]?.[0] ?? it.group}</Badge>
                  <div className="mt-1 text-[11px] text-muted">{it.becomes}</div>
                </td>
                <td className={td + " max-w-md text-xs"}>{it.reasons.join(" ")}</td>
              </tr>
            ))}
          </tbody>
        </table>
      </div>
    </div>
  );
}

type CheckLook = [React.ElementType, string, string];
const notYet: CheckLook = [Circle, "text-faint", "Not yet"];
const checkIcons: Record<string, CheckLook> = {
  ok: [CircleCheck, "text-ok", "Done"],
  warn: [TriangleAlert, "text-warn", "To do"],
  todo: notYet,
  manual: [Hand, "text-muted", "By hand"],
};

function MoveDialog({ project, onClose }: { project: S["VraCutoverProject"]; onClose: () => void }) {
  const qc = useQueryClient();
  const [note, setNote] = useState("");
  const move = useMutation({
    mutationFn: () =>
      call(
        api.PUT("/admin/vra/projects/{vraProjectId}/cutover", {
          params: { path: { vraProjectId: project.vra_id } },
          body: { moved: true, ...(note.trim() ? { note: note.trim() } : {}) },
        }),
      ),
    onSuccess: () => {
      void qc.invalidateQueries({ queryKey: ["admin", "vra", "plan"] });
      toast.success(`${project.name} moved to rvav`);
      onClose();
    },
    onError: (e) => failed(e, "It was not marked moved"),
  });
  return (
    <Dialog open onOpenChange={(o) => !o && onClose()}>
      <DialogContent title={`Move ${project.name} to rvav`} className="w-[min(520px,calc(100vw-32px))]">
        <form
          className="flex flex-col gap-3 text-[13px]"
          onSubmit={(e) => {
            e.preventDefault();
            move.mutate();
          }}
        >
          <p>
            From now on, {project.rvav_project_name}'s people request in rvav. vRA's administrators stop offering the
            project's items in vRA; the next read of vRA checks they did. vRA keeps its deployments not adopted yet.
          </p>
          {!project.ready && <p className="text-warn">Not everything is ready: check the list before moving it.</p>}
          <Field label="Note" htmlFor="vra-move-note">
            <Textarea
              id="vra-move-note"
              rows={2}
              maxLength={500}
              value={note}
              onChange={(e) => setNote(e.target.value)}
            />
          </Field>
          <div className="flex justify-end gap-2">
            <Button type="button" variant="ghost" onClick={onClose}>
              Cancel
            </Button>
            <Button type="submit" variant="primary" disabled={move.isPending}>
              Mark as moved
            </Button>
          </div>
        </form>
      </DialogContent>
    </Dialog>
  );
}

function CutoverTab({ plan }: { plan: Plan }) {
  const qc = useQueryClient();
  const c = plan.cutover;
  const [moving, setMoving] = useState<S["VraCutoverProject"] | null>(null);
  const undo = useMutation({
    mutationFn: (id: string) =>
      call(
        api.PUT("/admin/vra/projects/{vraProjectId}/cutover", {
          params: { path: { vraProjectId: id } },
          body: { moved: false },
        }),
      ),
    onSuccess: () => void qc.invalidateQueries({ queryKey: ["admin", "vra", "plan"] }),
    onError: (e) => failed(e, "It was not changed"),
  });
  return (
    <div className="flex flex-col gap-3 p-4">
      <p className="text-[13px]" aria-live="polite">
        <span className="font-medium">
          {c.moved} of {plural(c.total, "project")} moved
        </span>
        {" · "}
        {c.deployments_left.length === 0
          ? "every deployment adopted"
          : `${plural(c.deployments_left.length, "deployment")} still in vRA`}
        {" · "}
        {c.switch_off ? (
          <span className="text-ok">vRA can be switched off</span>
        ) : (
          <span className="text-muted">
            vRA can be switched off once every project has moved and every deployment is adopted
          </span>
        )}
      </p>
      {c.projects.map((cp) => (
        <section key={cp.vra_id} className="rounded-md border border-line" aria-label={cp.name}>
          <div className="flex flex-wrap items-center gap-2 border-b border-line px-3 py-2">
            <h3 className="text-[13px] font-medium">{cp.name}</h3>
            {cp.rvav_project_name && <span className="text-xs text-muted">→ {cp.rvav_project_name}</span>}
            {cp.moved_at ? (
              <Badge tone="ok">
                Moved <Time iso={cp.moved_at} />
                {cp.moved_by ? ` by ${cp.moved_by}` : ""}
              </Badge>
            ) : cp.ready ? (
              <Badge tone="accent">Ready</Badge>
            ) : (
              <Badge tone="outline">Not ready</Badge>
            )}
            <span className="ml-auto">
              {cp.moved_at ? (
                <Button size="sm" variant="ghost" disabled={undo.isPending} onClick={() => undo.mutate(cp.vra_id)}>
                  Not moved
                </Button>
              ) : (
                <Button size="sm" variant="ghost" disabled={!cp.rvav_project_id} onClick={() => setMoving(cp)}>
                  Mark as moved…
                </Button>
              )}
            </span>
          </div>
          {cp.note && <p className="px-3 pt-2 text-xs text-muted">{cp.note}</p>}
          <ul className="flex flex-col gap-1.5 px-3 py-2">
            {cp.checks.map((ch) => {
              const [Icon, tone, label] = checkIcons[ch.state] ?? notYet;
              return (
                <li key={ch.key} className="flex items-start gap-2 text-xs">
                  <Icon className={"mt-px size-3.5 shrink-0 " + tone} aria-label={label} role="img" />
                  <span>
                    <span className="font-medium">{ch.label}.</span> {ch.detail}
                  </span>
                </li>
              );
            })}
          </ul>
        </section>
      ))}
      {moving && <MoveDialog project={moving} onClose={() => setMoving(null)} />}
    </div>
  );
}

function ImportDialog({
  plan,
  onClose,
  onDone,
}: {
  plan: Plan;
  onClose: () => void;
  onDone: (r: S["VraImportResult"]) => void;
}) {
  const s = plan.summary;
  const [catalog, setCatalog] = useState(true);
  const [deployments, setDeployments] = useState(false);
  const run = useMutation({
    mutationFn: () => call(api.POST("/admin/vra/import", { body: { catalog, deployments } })),
    onSuccess: onDone,
    onError: (e) => failed(e, "Nothing was imported"),
  });
  return (
    <Dialog open onOpenChange={(o) => !o && onClose()}>
      <DialogContent title="Import from vRA" className="w-[min(520px,calc(100vw-32px))]">
        <form
          className="flex flex-col gap-4"
          onSubmit={(e) => {
            e.preventDefault();
            run.mutate();
          }}
        >
          <label htmlFor="vra-catalog" className="flex items-start gap-2 text-[13px]">
            <Checkbox id="vra-catalog" checked={catalog} onChange={setCatalog} />
            <span>
              <span className="font-medium">Catalog items</span>
              <span className="block text-xs text-muted">
                {plural(s.items_create, "item")} to create and {plural(s.items_update, "new draft")}, nothing published;
                shared with {plural(s.shares_add, "more project")}.
              </span>
            </span>
          </label>
          <label htmlFor="vra-deployments" className="flex items-start gap-2 text-[13px]">
            <Checkbox id="vra-deployments" checked={deployments} onChange={setDeployments} />
            <span>
              <span className="font-medium">Deployments</span>
              <span className="block text-xs text-muted">
                {plural(s.deployments_adopt, "deployment")} to adopt through onboarding, nothing changed in vCenter or
                EfficientIP. An item must be published for its deployments to be adopted.
              </span>
            </span>
          </label>
          <p className="text-xs text-muted">
            It can run again: only what changed in vRA, or isn't yet in rvav, is imported.
          </p>
          <div className="flex justify-end gap-2">
            <Button type="button" variant="ghost" onClick={onClose}>
              Cancel
            </Button>
            <Button type="submit" variant="primary" disabled={(!catalog && !deployments) || run.isPending}>
              {run.isPending ? "Importing…" : "Import"}
            </Button>
          </div>
        </form>
      </DialogContent>
    </Dialog>
  );
}

function Result({ r, onClose }: { r: S["VraImportResult"]; onClose: () => void }) {
  const line = (label: string, names: string[]) =>
    names.length > 0 && (
      <p>
        <span className="font-medium">{label}:</span> {names.join(", ")}
      </p>
    );
  return (
    <Panel
      title="Imported"
      actions={
        <Button size="sm" variant="ghost" onClick={onClose}>
          Close
        </Button>
      }
    >
      <section aria-label="Imported" className="flex flex-col gap-1 text-[13px]">
        {line("Items made, as drafts", r.created)}
        {line("New drafts", r.updated)}
        {r.shared > 0 && <p>Shared with {plural(r.shared, "more project")}.</p>}
        {line("Deployments adopted", r.adopted)}
        {r.failed.map((f) => (
          <p key={f.name} className="text-bad">
            {f.name}: {f.reason}
          </p>
        ))}
        {r.created.length + r.updated.length + r.shared + r.adopted.length + r.failed.length === 0 && (
          <p className="text-muted">Nothing to do: rvav has it all already.</p>
        )}
      </section>
    </Panel>
  );
}

export function VraImportPage() {
  const qc = useQueryClient();
  const file = useRef<HTMLInputElement>(null);
  const [importing, setImporting] = useState(false);
  const [result, setResult] = useState<S["VraImportResult"] | null>(null);
  const status = useQuery({
    queryKey: ["admin", "vra", "status"],
    queryFn: ({ signal }) => call(api.GET("/admin/vra", { signal })),
  });
  const snap = status.data?.snapshot ?? null;
  const plan = useQuery({
    queryKey: ["admin", "vra", "plan", snap?.id],
    queryFn: ({ signal }) => call(api.GET("/admin/vra/plan", { signal })),
    enabled: snap !== null,
  });
  const read = useMutation({
    mutationFn: async (f?: File) => {
      if (!f) return call(api.POST("/admin/vra/snapshots", {}));
      let body: Record<string, unknown>;
      try {
        body = JSON.parse(await f.text()) as Record<string, unknown>;
      } catch {
        throw new Error(`${f.name} is not JSON: give the file rvav vra-export wrote.`);
      }
      return call(api.POST("/admin/vra/snapshots", { body }));
    },
    onSuccess: (s) => {
      setResult(null);
      void qc.invalidateQueries({ queryKey: ["admin", "vra"] });
      toast.success(s.source === "vra" ? "vRA read" : "The file was read");
    },
    onError: (e) =>
      e instanceof Error && !isApiError(e) ? toast.error(e.message) : failed(e, "vRA could not be read"),
  });
  const p = plan.data;
  return (
    <Page>
      <PageHeader
        title="vRA import"
        meta={
          snap ? (
            <>
              <span>
                Read <Time iso={snap.read_at} /> {snap.source === "file" ? "from a file" : `from ${snap.url ?? "vRA"}`}
                {snap.created_by ? ` by ${snap.created_by}` : ""}
              </span>
              <span>
                {plural(snap.counts.projects, "project")} · {plural(snap.counts.templates, "template")} ·{" "}
                {plural(snap.counts.deployments, "deployment")} · {plural(snap.counts.vro_workflows, "vRO workflow")}
              </span>
            </>
          ) : (
            "Move vRA's catalog and deployments to rvav. vRA is only read."
          )
        }
        actions={
          <>
            <input
              ref={file}
              type="file"
              accept="application/json,.json"
              className="hidden"
              aria-label="vra-export file"
              onChange={(e) => {
                const f = e.target.files?.[0];
                if (f) read.mutate(f);
                e.target.value = "";
              }}
            />
            <Button size="sm" variant="ghost" disabled={read.isPending} onClick={() => file.current?.click()}>
              <FileUp /> Upload file…
            </Button>
            <Button
              size="sm"
              variant="ghost"
              disabled={!status.data?.configured || read.isPending}
              title={status.data?.configured ? undefined : "Set RVAV_VRA_URL for rvav to read vRA itself"}
              onClick={() => read.mutate(undefined)}
            >
              <RefreshCw /> {read.isPending ? "Reading…" : "Read vRA"}
            </Button>
            <Button size="sm" variant="primary" disabled={!p} onClick={() => setImporting(true)}>
              Import…
            </Button>
          </>
        }
      />
      {status.isError && <ErrorState error={status.error} onRetry={() => void status.refetch()} />}
      {status.data && !snap && (
        <Panel>
          <EmptyState
            title="Nothing read yet"
            body={
              status.data.configured
                ? "Read vRA to see what an import would do. Nothing is written until you import."
                : "Upload the file rvav vra-export wrote, or set RVAV_VRA_URL for rvav to read vRA itself."
            }
          />
        </Panel>
      )}
      {result && <Result r={result} onClose={() => setResult(null)} />}
      {plan.isPending && snap && <SkeletonRows rows={4} cols={4} />}
      {plan.isError && <ErrorState error={plan.error} onRetry={() => void plan.refetch()} />}
      {p && (
        <>
          <Summary plan={p} />
          <Panel flush>
            <Tabs defaultValue="items">
              <TabsList className="px-4 pt-2">
                <TabsTrigger value="projects">Projects</TabsTrigger>
                <TabsTrigger value="items">Catalog items ({p.items.length})</TabsTrigger>
                <TabsTrigger value="deployments">Deployments ({p.deployments.length})</TabsTrigger>
                <TabsTrigger value="policies">Policies ({p.policies.length})</TabsTrigger>
                <TabsTrigger value="vro">vRO ({p.triage.items.length})</TabsTrigger>
                <TabsTrigger value="cutover">
                  Cutover ({p.cutover.moved}/{p.cutover.total})
                </TabsTrigger>
              </TabsList>
              <TabsContent value="projects">
                <ProjectsTab plan={p} />
              </TabsContent>
              <TabsContent value="items">
                <ItemsTab plan={p} />
              </TabsContent>
              <TabsContent value="deployments">
                <DeploymentsTab plan={p} />
              </TabsContent>
              <TabsContent value="policies">
                <PoliciesTab plan={p} />
              </TabsContent>
              <TabsContent value="vro">
                <VroTab plan={p} />
              </TabsContent>
              <TabsContent value="cutover">
                <CutoverTab plan={p} />
              </TabsContent>
            </Tabs>
          </Panel>
        </>
      )}
      {importing && p && (
        <ImportDialog
          plan={p}
          onClose={() => setImporting(false)}
          onDone={(r) => {
            setImporting(false);
            setResult(r);
            qc.setQueryData(["admin", "vra", "plan", snap?.id], r.plan);
            void qc.invalidateQueries({ queryKey: ["admin", "catalog"] });
            void qc.invalidateQueries({ queryKey: ["deployments"] });
            toast.success("Imported from vRA");
          }}
        />
      )}
    </Page>
  );
}
