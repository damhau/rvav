// The vRA importer (P2.5, ADR-0027): read vRA, or the file vra-export
// wrote; see what an import would do (the dry run) and map vRA's projects;
// then import the catalog as drafts with its sharing, and adopt
// deployments. It can run again. vRA is only read.
import { useIsMutating, useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { Circle, CircleCheck, Download, FileUp, Hand, RefreshCw, TriangleAlert } from "lucide-react";
import { Link, useNavigate } from "@tanstack/react-router";
import { useRef, useState } from "react";
import { toast } from "sonner";
import { ErrorState, SkeletonRows } from "@/components/common/feedback";
import { Time } from "@/components/common/misc";
import { Page } from "@/components/common/page";
import { EmptyState } from "@/components/ui/empty-state";
import { PageHeader } from "@/components/ui/page-header";
import { Panel } from "@/components/ui/panel";
import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import { Checkbox } from "@/components/ui/checkbox";
import { ConfirmDialog } from "@/components/ui/confirm-dialog";
import { DataTable, type Column } from "@/components/ui/data-table";
import { Field, Textarea } from "@/components/ui/input";
import { Select } from "@/components/ui/select";
import { Sheet } from "@/components/ui/sheet";
import { StepMark, stepNow } from "@/components/ui/step-mark";
import { Tabs, TabsContent, TabsList, TabsTrigger } from "@/components/ui/tabs";
import { api, call, isApiError } from "@/lib/api/client";
import type { components } from "@/lib/api/schema";
import { adm } from "@/lib/query";
import { cn } from "@/lib/utils";
import { failed } from "./governance/common";

type S = components["schemas"];
type Plan = S["VraPlan"];
type Tone = "neutral" | "accent" | "ok" | "warn" | "bad" | "run" | "outline";

export const vraTabs = ["projects", "items", "deployments", "policies", "vro", "cutover"] as const;
export type VraTab = (typeof vraTabs)[number];

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
const strategyLabels: Record<S["PlacementStrategy"], string> = {
  most_free_memory: "Most free memory",
  binpack: "Binpack",
  spread: "Spread",
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
/** The Select values of "no mapping" (by name, or none) and of "stays in vRA". */
const AUTO = "_auto";
const STAYS = "_stays";
/** Every mutation of the page: one at a time. */
const busyKey = ["vra"];
const plural = (n: number, one: string, many = one + "s") => `${n} ${n === 1 ? one : many}`;

function Notes({ notes }: { notes: S["VraNote"][] }) {
  if (notes.length === 0) return <p className="text-secondary text-faint">Nothing to note.</p>;
  return (
    <ul className="flex flex-col gap-2.5">
      {notes.map((n, i) => (
        <li key={i} className="flex flex-wrap items-baseline gap-x-2 gap-y-0.5 text-secondary">
          <Badge tone={levels[n.level][1]}>{levels[n.level][0]}</Badge>
          <code className="font-mono text-caption text-faint">{n.where}</code>
          <span className="min-w-0 basis-full">{n.text}</span>
        </li>
      ))}
    </ul>
  );
}

interface Noted {
  of: string;
  notes: S["VraNote"][];
}

/** What a row's notes hold, as a button that opens them in a side panel. */
function NotesButton({ of, notes, onOpen }: Noted & { onOpen: (n: Noted) => void }) {
  const check = notes.filter((n) => n.level === "warning").length;
  const out = notes.filter((n) => n.level === "unmapped").length;
  const parts = [check ? `${check} to check` : "", out ? `${out} left out` : ""].filter(Boolean);
  const text = parts.length ? parts.join(" · ") : plural(notes.length, "note");
  return (
    <Button
      size="sm"
      variant="quiet"
      aria-haspopup="dialog"
      aria-label={`Notes on ${of}: ${text}`}
      onClick={() => onOpen({ of, notes })}
    >
      {text}
    </Button>
  );
}

function NotesSheet({ noted, onClose }: { noted: Noted; onClose: () => void }) {
  return (
    <Sheet
      open
      onOpenChange={(o) => !o && onClose()}
      title={`Notes on ${noted.of}`}
      description="What the import keeps, changes or leaves out, and what to check."
    >
      <Notes notes={noted.notes} />
    </Sheet>
  );
}

function Summary({ plan }: { plan: Plan }) {
  const s = plan.summary;
  const stat = (label: string, n: number, tone?: string) => (
    <div className="flex flex-col">
      <span className={"tnum text-title font-semibold " + (tone ?? "")}>{n}</span>
      <span className="text-secondary text-muted">{label}</span>
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
          {stat("shares to add", s.shares_add)}
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

/** Where the move stands: read, map, import, move each project. */
function Steps({ read, plan }: { read: S["VraSnapshot"] | null; plan?: Plan }) {
  const unplaced = plan?.projects.filter((p) => p.how === "none").length ?? 0;
  const s = plan?.summary;
  const items = s ? s.templates_create + s.items_create + s.items_update : 0;
  const left = s ? items + s.shares_add + s.deployments_adopt : 0;
  const steps = [
    {
      title: "Read vRA",
      done: read !== null,
      detail: read ? (
        <>
          Read <Time iso={read.read_at} />
        </>
      ) : (
        "From vRA, or from the file rvav vra-export wrote."
      ),
    },
    {
      title: "Map the projects",
      done: Boolean(plan) && unplaced === 0,
      detail: !plan
        ? "Each vRA project to an rvav project, or kept in vRA."
        : unplaced > 0
          ? `${plural(unplaced, "project")} with no rvav project of the same name: choose one, or keep it in vRA.`
          : "Each has an rvav project, or stays in vRA.",
    },
    {
      title: "Import",
      done: Boolean(plan) && left === 0,
      detail: !s
        ? "The catalog as drafts, with its sharing, and the deployments."
        : left === 0
          ? "Nothing left to import."
          : `${plural(items, "item")}, ${plural(s.shares_add, "share")} and ${plural(s.deployments_adopt, "deployment")} to import.`,
    },
    {
      title: "Move each project",
      done: plan?.cutover.switch_off ?? false,
      detail: plan
        ? `${plan.cutover.moved} of ${plan.cutover.total} moved.`
        : "Its people request in rvav, and vRA stops offering its items.",
    },
  ];
  const current = steps.findIndex((st) => !st.done);
  return (
    <ol
      aria-label="Steps"
      className="grid gap-px overflow-hidden rounded-lg border border-line bg-line sm:grid-cols-2 xl:grid-cols-4"
    >
      {steps.map((st, i) => {
        const state = st.done ? "done" : i === current ? "now" : "later";
        return (
          <li
            key={st.title}
            aria-current={state === "now" ? "step" : undefined}
            className={cn("flex gap-3 bg-surface px-4 py-3", state === "now" && stepNow)}
          >
            <StepMark n={i + 1} state={state} />
            <div className="flex min-w-0 flex-col gap-0.5">
              <p className="text-body font-medium">
                {st.title}
                <span className="sr-only">: {state}</span>
              </p>
              <p className="text-secondary text-muted">{st.detail}</p>
            </div>
          </li>
        );
      })}
    </ol>
  );
}

function ProjectsTab({ plan }: { plan: Plan }) {
  const qc = useQueryClient();
  const projects = useQuery(adm.projectNames());
  const map = useMutation({
    mutationFn: ({ vra, to }: { vra: string; to: string }) =>
      call(
        api.PUT("/admin/vra/projects/{vraProjectId}", {
          params: { path: { vraProjectId: vra } },
          body: { rvav_project_id: to === AUTO || to === STAYS ? null : to, stays_in_vra: to === STAYS },
        }),
      ),
    // The row stays busy until the plan shows the new mapping.
    onSuccess: () => qc.invalidateQueries({ queryKey: ["admin", "vra", "plan"] }),
    onError: (e) => failed(e, "The project was not mapped"),
  });
  const namesake = (name: string) => projects.data?.items.find((x) => x.name.toLowerCase() === name.toLowerCase());
  const columns: Column<S["VraProjectPlan"]>[] = [
    {
      id: "vra",
      header: "vRA project",
      sortValue: (p) => p.name,
      cell: (p) => (
        <>
          <span className="font-medium">{p.name}</span>
          <div className="font-mono text-caption text-faint">{p.vra_id}</div>
        </>
      ),
    },
    {
      id: "rvav",
      header: "rvav project",
      className: "w-72",
      cell: (p) => (
        <Select
          aria-label={`rvav project for ${p.name}`}
          value={p.how === "mapped" ? (p.rvav_project_id ?? AUTO) : p.how === "stays" ? STAYS : AUTO}
          loading={map.isPending && map.variables.vra === p.vra_id}
          onChange={(v) => map.mutate({ vra: p.vra_id, to: v })}
          options={[
            {
              value: AUTO,
              label: namesake(p.name) ? `By name: ${namesake(p.name)?.name}` : "By name: none, so it stays in vRA",
            },
            { value: STAYS, label: "Stays in vRA" },
            ...(projects.data?.items ?? []).map((x) => ({ value: x.id, label: x.name })),
          ]}
        />
      ),
    },
    {
      id: "strategy",
      header: "Placement strategy",
      sortValue: (p) => (p.strategy ? strategyLabels[p.strategy] : null),
      cell: (p) => (
        <>
          {p.strategy ? strategyLabels[p.strategy] : <span className="text-faint">None</span>}
          {p.placement_policy && <div className="font-mono text-caption text-faint">vRA: {p.placement_policy}</div>}
        </>
      ),
    },
    {
      id: "items",
      header: "Items shared",
      sortValue: (p) => p.items,
      cell: (p) => <span className="tnum">{p.items}</span>,
    },
    {
      id: "deployments",
      header: "Deployments",
      sortValue: (p) => p.deployments,
      cell: (p) => <span className="tnum">{p.deployments}</span>,
    },
  ];
  return (
    <>
      <DataTable
        label="vRA projects"
        columns={columns}
        rows={plan.projects}
        rowKey={(p) => p.vra_id}
        minWidth={760}
        toolbar={
          projects.isError && (
            <p className="text-secondary text-bad">
              rvav's projects couldn't be loaded, so only "by name" and "stays in vRA" can be chosen.{" "}
              <Button size="sm" variant="quiet" onClick={() => void projects.refetch()}>
                Retry
              </Button>
            </p>
          )
        }
      />
    </>
  );
}

function ItemsTab({ plan, onNotes }: { plan: Plan; onNotes: (n: Noted) => void }) {
  const names = new Map(
    plan.projects.filter((p) => p.rvav_project_id).map((p) => [p.rvav_project_id!, p.rvav_project_name]),
  );
  const columns: Column<S["VraItemPlan"]>[] = [
    {
      id: "template",
      header: "Cloud template",
      sortValue: (it) => it.name,
      cell: (it) => (
        <>
          <span className="font-medium">{it.name}</span>
          <div className="font-mono text-caption text-faint">
            {it.slug}
            {it.version ? ` · ${it.version}` : ""}
          </div>
        </>
      ),
    },
    {
      id: "maps",
      header: "Maps",
      sortValue: (it) => mapping[it.status][0],
      cell: (it) => <Badge tone={mapping[it.status][1]}>{mapping[it.status][0]}</Badge>,
    },
    {
      id: "import",
      header: "Import",
      sortValue: (it) => actions[it.action][0],
      cell: (it) => (
        <>
          <Badge tone={actions[it.action][1]}>{actions[it.action][0]}</Badge>
          {it.reason && <div className="mt-1 max-w-64 text-secondary text-muted">{it.reason}</div>}
        </>
      ),
    },
    {
      id: "shared",
      header: "Shared with",
      cell: (it) => (
        <span className="text-secondary">
          {it.share_add.length > 0 && <div>Adds {it.share_add.map((id) => names.get(id) ?? id).join(", ")}</div>}
          {it.share_unmapped.length > 0 && (
            <div className="text-warn">Not {it.share_unmapped.join(", ")}: not mapped</div>
          )}
          {it.share_add.length === 0 && it.share_unmapped.length === 0 && (
            <span className="text-faint">Nothing to add</span>
          )}
        </span>
      ),
    },
    {
      id: "notes",
      header: "Notes",
      cell: (it) => <NotesButton of={it.name} notes={it.notes} onOpen={onNotes} />,
    },
  ];
  return (
    <DataTable
      label="Cloud templates"
      columns={columns}
      rows={plan.items}
      rowKey={(it) => it.blueprint_id}
      minWidth={820}
      empty={<EmptyState title="No cloud template" body="vRA has no cloud template to import." />}
    />
  );
}

function DeploymentsTab({ plan, onNotes }: { plan: Plan; onNotes: (n: Noted) => void }) {
  const columns: Column<S["VraDeploymentPlan"]>[] = [
    {
      id: "deployment",
      header: "Deployment",
      sortValue: (d) => d.name,
      cell: (d) => (
        <>
          <span className="font-medium">{d.name}</span>
          <div className="text-caption text-faint">
            {d.vra_project}
            {d.environment ? ` · ${d.environment}` : ""}
            {d.rvav_name && d.rvav_name !== d.name ? ` · ${d.rvav_name} in rvav` : ""}
          </div>
        </>
      ),
    },
    {
      id: "owner",
      header: "Owner",
      sortValue: (d) => d.owner,
      cell: (d) => <span className="text-secondary">{d.owner}</span>,
    },
    {
      id: "vms",
      header: "VMs",
      cell: (d) => (
        <span className="font-mono text-secondary">
          {(d.vms ?? []).map((v) => v.name).join(", ") || <span className="text-faint">none found</span>}
        </span>
      ),
    },
    {
      id: "adoption",
      header: "Adoption",
      sortValue: (d) => adoption[d.status][0],
      cell: (d) => {
        const why = [...d.notes].reverse().find((n) => n.level === "unmapped");
        return (
          <>
            <Badge tone={adoption[d.status][1]}>{adoption[d.status][0]}</Badge>
            {why && <div className="mt-1 max-w-72 text-secondary text-muted">{why.text}</div>}
          </>
        );
      },
    },
    {
      id: "notes",
      header: "Notes",
      cell: (d) => <NotesButton of={d.name} notes={d.notes} onOpen={onNotes} />,
    },
  ];
  return (
    <DataTable
      label="vRA deployments"
      columns={columns}
      rows={plan.deployments}
      rowKey={(d) => d.vra_id}
      minWidth={820}
      empty={<EmptyState title="No deployment" body="vRA has no deployment to adopt." />}
    />
  );
}

function PoliciesTab({ plan }: { plan: Plan }) {
  return (
    <div className="flex flex-col gap-3 p-4">
      <p className="max-w-3xl text-secondary text-muted">
        Policies decide who may do what, so the import doesn't change rvav's: set each where it says, with the values
        below.
      </p>
      {plan.policies.map((p) => (
        <section key={p.vra_id} className="rounded-md border border-line p-3" aria-label={p.name}>
          <div className="flex flex-wrap items-center gap-2">
            <h3 className="text-body font-medium">{p.name}</h3>
            <Badge tone="outline">{p.type}</Badge>
            <span className="text-secondary text-muted">{p.vra_project ? `In ${p.vra_project}` : "Every project"}</span>
          </div>
          <p className="mt-1 mb-2 text-secondary">Becomes: {p.becomes}</p>
          <Notes notes={p.notes} />
        </section>
      ))}
    </div>
  );
}

type TriageItem = Plan["triage"]["items"][number];

const triageColumns: Column<TriageItem>[] = [
  {
    id: "name",
    header: "Name",
    sortValue: (it) => it.name,
    cell: (it) => (
      <>
        <span className="font-medium">{it.name}</span>
        <div className="text-caption text-faint">
          {kinds[it.kind] ?? it.kind}
          {it.category && it.kind === "workflow" ? ` · ${it.category}` : ""}
          {it.systems.length > 0 ? ` · ${it.systems.join(", ")}` : ""}
        </div>
      </>
    ),
  },
  {
    id: "used",
    header: "Used by",
    sortValue: (it) => it.used_by.length,
    cell: (it) => (
      <span className="text-secondary">
        {it.used_by.length === 0 && <span className="text-faint">Nothing</span>}
        {it.used_by.map((u, i) => (
          <div key={i} className={u.disabled ? "text-faint" : ""}>
            {kinds[u.kind] ?? u.kind} {u.name}
            {u.disabled ? " (disabled)" : ""}
          </div>
        ))}
      </span>
    ),
  },
  {
    id: "run",
    header: "Last run",
    sortValue: (it) => it.last_run ?? null,
    className: "whitespace-nowrap",
    cell: (it) => (
      <span className="text-secondary">
        {it.kind !== "workflow" ? "–" : it.last_run ? <Time iso={it.last_run} /> : "never"}
      </span>
    ),
  },
  {
    id: "group",
    header: "Group",
    sortValue: (it) => it.group,
    cell: (it) => (
      <>
        <Badge tone={groups[it.group]?.[1] ?? "neutral"}>{groups[it.group]?.[0] ?? it.group}</Badge>
        <div className="mt-1 text-caption text-muted">{it.becomes}</div>
      </>
    ),
  },
  {
    id: "why",
    header: "Why",
    className: "max-w-md",
    cell: (it) => <span className="text-secondary">{it.reasons.join(" ")}</span>,
  },
];

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
    <DataTable
      label="vRO workflows and actions"
      columns={triageColumns}
      rows={t.items}
      rowKey={(it) => it.kind + it.id}
      minWidth={900}
      toolbar={
        <div className="flex w-full flex-wrap items-center gap-3 text-secondary">
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
            variant="secondary"
            disabled={download.isPending}
            onClick={() => download.mutate()}
          >
            <Download /> Report (Markdown)
          </Button>
        </div>
      }
      empty={<EmptyState title="No vRO workflow" body="Nothing in vRO outside its own library is used by vRA." />}
    />
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

function MoveSheet({ project, onClose }: { project: S["VraCutoverProject"]; onClose: () => void }) {
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
    onSuccess: async () => {
      await qc.invalidateQueries({ queryKey: ["admin", "vra", "plan"] });
      toast.success(`${project.name} moved to rvav`);
      onClose();
    },
    onError: (e) => failed(e, "It was not marked moved"),
  });
  return (
    <Sheet
      open
      onOpenChange={(o) => !o && onClose()}
      title={`Move ${project.name} to rvav`}
      onSubmit={() => move.mutate()}
      submitLabel="Mark as moved"
      submitDisabled={move.isPending}
    >
      <p className="text-body">
        From now on, {project.rvav_project_name}&apos;s people request in rvav. vRA&apos;s administrators stop offering
        the project&apos;s items in vRA; the next read of vRA checks they did. vRA keeps its deployments not adopted
        yet.
      </p>
      {!project.ready && <p className="text-warn">Not everything is ready: check the list before moving it.</p>}
      <Field label="Note" htmlFor="vra-move-note" hint="Shown with the move, such as when or why.">
        <Textarea id="vra-move-note" rows={3} maxLength={500} value={note} onChange={(e) => setNote(e.target.value)} />
      </Field>
    </Sheet>
  );
}

function UndoMove({ project, onClose }: { project: S["VraCutoverProject"]; onClose: () => void }) {
  const qc = useQueryClient();
  const undo = useMutation({
    mutationFn: () =>
      call(
        api.PUT("/admin/vra/projects/{vraProjectId}/cutover", {
          params: { path: { vraProjectId: project.vra_id } },
          body: { moved: false },
        }),
      ),
    onSuccess: async () => {
      await qc.invalidateQueries({ queryKey: ["admin", "vra", "plan"] });
      toast.success(`${project.name} is no longer moved`);
      onClose();
    },
    onError: (e) => {
      onClose();
      failed(e, "It was not changed");
    },
  });
  return (
    <ConfirmDialog
      open
      onOpenChange={(o) => !o && !undo.isPending && onClose()}
      title={`Mark ${project.name} not moved?`}
      description='Its move date and note are cleared; marking it moved again starts the "Nothing new in vRA" check from then. Its adopted deployments stay in rvav.'
      consequences="If vRA's administrators give its people vRA back, warn them: deleting an adopted deployment in vRA deletes its VMs."
      confirmLabel="Not moved"
      pending={undo.isPending}
      onConfirm={() => undo.mutate()}
    />
  );
}

function CutoverTab({ plan, busy }: { plan: Plan; busy: boolean }) {
  const qc = useQueryClient();
  const c = plan.cutover;
  const [moving, setMoving] = useState<S["VraCutoverProject"] | null>(null);
  const [undoing, setUndoing] = useState<S["VraCutoverProject"] | null>(null);
  // One project's deployments, adopted on their own.
  const adopt = useMutation({
    mutationKey: busyKey,
    mutationFn: (cp: S["VraCutoverProject"]) =>
      call(api.POST("/admin/vra/import", { body: { catalog: false, deployments: true, projects: [cp.vra_id] } })),
    onSuccess: (r, cp) => {
      qc.setQueryData(["admin", "vra", "plan"], r.plan);
      void qc.invalidateQueries({ queryKey: ["deployments"] });
      void qc.invalidateQueries({ queryKey: ["admin", "onboarding"] });
      if (r.adopted.length) toast.success(`Adopted ${plural(r.adopted.length, "deployment")} of ${cp.name}`);
      if (r.failed.length)
        toast.error(`${plural(r.failed.length, "deployment")} not adopted`, {
          description: r.failed.map((f) => `${f.name}: ${f.reason}`).join(" "),
        });
      if (!r.adopted.length && !r.failed.length) toast(`Nothing of ${cp.name}'s to adopt`);
    },
    onError: (e) => {
      void qc.invalidateQueries({ queryKey: ["admin", "vra", "plan"] });
      failed(e, "The adoption stopped; what was adopted is kept");
    },
  });
  const toAdopt = (cp: S["VraCutoverProject"]) =>
    plan.deployments.filter((d) => d.vra_project === cp.name && d.status === "adopt").length;
  return (
    <div className="flex flex-col gap-3 p-4">
      <p className="text-body" aria-live="polite">
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
            <h3 className="text-body font-medium">{cp.name}</h3>
            {cp.rvav_project_name && <span className="text-secondary text-muted">→ {cp.rvav_project_name}</span>}
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
            <span className="ml-auto flex gap-1.5">
              {toAdopt(cp) > 0 && (
                <Button
                  size="sm"
                  variant="secondary"
                  disabled={busy}
                  aria-label={`Adopt ${plural(toAdopt(cp), "deployment")} (${cp.name})`}
                  onClick={() => adopt.mutate(cp)}
                >
                  Adopt {plural(toAdopt(cp), "deployment")}
                </Button>
              )}
              {cp.moved_at ? (
                <Button size="sm" variant="quiet" aria-label={`Not moved… (${cp.name})`} onClick={() => setUndoing(cp)}>
                  Not moved…
                </Button>
              ) : (
                <Button
                  size="sm"
                  variant="secondary"
                  disabled={!cp.rvav_project_id}
                  aria-label={`Mark as moved… (${cp.name})`}
                  onClick={() => setMoving(cp)}
                >
                  Mark as moved…
                </Button>
              )}
            </span>
          </div>
          {cp.note && <p className="px-3 pt-2 text-secondary text-muted">{cp.note}</p>}
          <ul className="flex flex-col gap-1.5 px-3 py-2">
            {cp.checks.map((ch) => {
              const [Icon, tone, label] = checkIcons[ch.state] ?? notYet;
              return (
                <li key={ch.key} className="flex items-start gap-2 text-secondary">
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
      {moving && <MoveSheet project={moving} onClose={() => setMoving(null)} />}
      {undoing && <UndoMove project={undoing} onClose={() => setUndoing(null)} />}
    </div>
  );
}

function ImportSheet({
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
  const qc = useQueryClient();
  const run = useMutation({
    mutationKey: busyKey,
    mutationFn: () => call(api.POST("/admin/vra/import", { body: { catalog, deployments } })),
    onSuccess: onDone,
    onError: (e) => {
      // It may have stopped half way: what it did is kept, and shown.
      for (const key of [["admin", "vra", "plan"], ["admin", "catalog"], ["deployments"], ["admin", "onboarding"]])
        void qc.invalidateQueries({ queryKey: key });
      failed(e, "The import stopped; what was done is kept");
    },
  });
  return (
    <Sheet
      open
      onOpenChange={(o) => !o && !run.isPending && onClose()}
      title="Import from vRA"
      description="It can run again: only what changed in vRA, or isn't yet in rvav, is imported."
      onSubmit={() => run.mutate()}
      submitLabel={run.isPending ? "Importing…" : "Import"}
      submitDisabled={(!catalog && !deployments) || run.isPending}
    >
      <label htmlFor="vra-catalog" className="flex items-start gap-2 text-body">
        <Checkbox id="vra-catalog" className="mt-0.5" checked={catalog} onChange={setCatalog} />
        <span>
          <span className="font-medium">Catalog items</span>
          <span className="block text-secondary text-muted">
            {s.templates_create > 0 && `${plural(s.templates_create, "template")} to make from vRA's image mappings; `}
            {plural(s.items_create, "item")} to create and {plural(s.items_update, "new draft")}, nothing published;{" "}
            {plural(s.shares_add, "share")} to add (an item offered to one more project).
          </span>
        </span>
      </label>
      <label htmlFor="vra-deployments" className="flex items-start gap-2 text-body">
        <Checkbox id="vra-deployments" className="mt-0.5" checked={deployments} onChange={setDeployments} />
        <span>
          <span className="font-medium">Deployments</span>
          <span className="block text-secondary text-muted">
            {plural(s.deployments_adopt, "deployment")} to adopt through onboarding, every project&apos;s, nothing
            changed in vCenter or EfficientIP. To adopt one project&apos;s, use its Cutover section.
          </span>
        </span>
      </label>
      {!catalog && !deployments && <p className="text-secondary text-muted">Choose what to import.</p>}
    </Sheet>
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
        <Button size="sm" variant="quiet" onClick={onClose}>
          Close
        </Button>
      }
    >
      <section aria-label="Imported" className="flex flex-col gap-1 text-body">
        {line("Templates made", r.templates_created)}
        {line("Items made, as drafts", r.created)}
        {line("New drafts", r.updated)}
        {r.shared > 0 && <p>{plural(r.shared, "share")} added: items offered to more projects.</p>}
        {line("Deployments adopted", r.adopted)}
        {r.failed.map((f) => (
          <p key={f.name} className="text-bad">
            {f.name}: {f.reason}
          </p>
        ))}
        {r.templates_created.length +
          r.created.length +
          r.updated.length +
          r.shared +
          r.adopted.length +
          r.failed.length ===
          0 && <p className="text-muted">Nothing new to import. What is left, and why, is in the plan below.</p>}
      </section>
    </Panel>
  );
}

export function VraImportPage({ tab = "items" }: { tab?: VraTab }) {
  const qc = useQueryClient();
  const navigate = useNavigate();
  const file = useRef<HTMLInputElement>(null);
  const [importing, setImporting] = useState(false);
  const [result, setResult] = useState<S["VraImportResult"] | null>(null);
  const [noted, setNoted] = useState<Noted | null>(null);
  const busy = useIsMutating({ mutationKey: busyKey }) > 0;
  const status = useQuery({
    queryKey: ["admin", "vra", "status"],
    queryFn: ({ signal }) => call(api.GET("/admin/vra", { signal })),
  });
  const snap = status.data?.snapshot ?? null;
  // One key for every read: a new read refreshes the plan in place.
  const plan = useQuery({
    queryKey: ["admin", "vra", "plan"],
    queryFn: ({ signal }) => call(api.GET("/admin/vra/plan", { signal })),
    enabled: snap !== null,
  });
  const read = useMutation({
    mutationKey: busyKey,
    mutationFn: (f?: File) =>
      f
        ? // The file as it is: the server reads it as it comes, and says what is wrong with it.
          call(
            api.POST("/admin/vra/snapshots", {
              body: f as unknown as Record<string, unknown>,
              bodySerializer: (b: unknown) => b as BodyInit,
              headers: { "Content-Type": "application/json" },
            }),
          )
        : call(api.POST("/admin/vra/snapshots", {})),
    onSuccess: (s) => {
      setResult(null);
      void qc.invalidateQueries({ queryKey: ["admin", "vra"] });
      toast.success(s.source === "vra" ? "vRA read" : "The file was read");
    },
    onError: (e) => failed(e, "vRA could not be read"),
  });
  const p = plan.data;
  // What the plan was made from; the status's until there is one.
  const shown = p?.snapshot ?? snap;
  const configured = Boolean(status.data?.configured);
  // Before a plan, reading is the page's main action; after, importing.
  const readFirst = !p;
  return (
    <Page>
      <PageHeader
        title="vRA import"
        description={
          <>
            Move vRA&apos;s catalog and deployments to rvav, project by project. vRA is only read.
            {status.data && !configured && (
              <>
                {" "}
                rvav reads no vRA itself: set it in{" "}
                <Link
                  to="/admin/integrations/$kind"
                  params={{ kind: "vra" }}
                  className="text-accent underline underline-offset-2"
                >
                  Integrations › vRA
                </Link>
                , or upload the file rvav vra-export wrote.
              </>
            )}
          </>
        }
        meta={
          shown && (
            <>
              <span>
                Read <Time iso={shown.read_at} />{" "}
                {shown.source === "file" ? "from a file" : `from ${shown.url ?? "vRA"}`}
                {shown.created_by ? ` by ${shown.created_by}` : ""}
              </span>
              <span>
                {plural(shown.counts.projects, "project")} · {plural(shown.counts.templates, "template")} ·{" "}
                {plural(shown.counts.deployments, "deployment")} · {plural(shown.counts.vro_workflows, "vRO workflow")}
              </span>
            </>
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
            <Button
              variant={readFirst && !configured ? "primary" : "secondary"}
              disabled={busy}
              onClick={() => file.current?.click()}
            >
              <FileUp /> Upload file…
            </Button>
            {configured && (
              <Button
                variant={readFirst ? "primary" : "secondary"}
                disabled={busy}
                onClick={() => read.mutate(undefined)}
              >
                <RefreshCw /> {read.isPending ? "Reading…" : "Read vRA"}
              </Button>
            )}
            {p && (
              <Button variant="primary" disabled={busy} onClick={() => setImporting(true)}>
                Import…
              </Button>
            )}
          </>
        }
      />
      {status.isError && <ErrorState error={status.error} onRetry={() => void status.refetch()} />}
      {status.data && <Steps read={shown} plan={p} />}
      {status.data && !snap && (
        <Panel>
          <EmptyState
            icon={FileUp}
            title="Nothing read yet"
            body={
              configured
                ? "Read vRA to see what an import would do. Nothing is written until you import."
                : "Upload the file rvav vra-export wrote, or set vRA in Integrations for rvav to read it itself."
            }
            action={
              configured ? (
                <Button size="sm" variant="primary" disabled={busy} onClick={() => read.mutate(undefined)}>
                  <RefreshCw /> Read vRA
                </Button>
              ) : (
                <Button size="sm" variant="primary" disabled={busy} onClick={() => file.current?.click()}>
                  <FileUp /> Upload file…
                </Button>
              )
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
            <Tabs
              value={tab}
              onValueChange={(v) =>
                void navigate({ to: "/admin/vra-import", search: { tab: v as VraTab }, replace: true })
              }
            >
              <TabsList aria-label="The plan" className="px-4 pt-2">
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
                <ItemsTab plan={p} onNotes={setNoted} />
              </TabsContent>
              <TabsContent value="deployments">
                <DeploymentsTab plan={p} onNotes={setNoted} />
              </TabsContent>
              <TabsContent value="policies">
                <PoliciesTab plan={p} />
              </TabsContent>
              <TabsContent value="vro">
                <VroTab plan={p} />
              </TabsContent>
              <TabsContent value="cutover">
                <CutoverTab plan={p} busy={busy} />
              </TabsContent>
            </Tabs>
          </Panel>
        </>
      )}
      {noted && <NotesSheet noted={noted} onClose={() => setNoted(null)} />}
      {importing && p && (
        <ImportSheet
          plan={p}
          onClose={() => setImporting(false)}
          onDone={(r) => {
            setImporting(false);
            setResult(r);
            qc.setQueryData(["admin", "vra", "plan"], r.plan);
            void qc.invalidateQueries({ queryKey: ["admin", "catalog"] });
            void qc.invalidateQueries({ queryKey: ["deployments"] });
            void qc.invalidateQueries({ queryKey: ["admin", "onboarding"] });
            toast.success("Imported from vRA");
          }}
        />
      )}
    </Page>
  );
}
