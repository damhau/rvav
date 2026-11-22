// The vRA importer (P2.5, ADR-0027): read vRA, or the file vra-export
// wrote; see what an import would do (the dry run) and map vRA's projects;
// then import the catalog as drafts with its sharing, and adopt
// deployments. It can run again. vRA is only read.
import { useIsMutating, useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { Circle, CircleCheck, Download, FileUp, Hand, Lock, RefreshCw, TriangleAlert } from "lucide-react";
import { Link, useNavigate } from "@tanstack/react-router";
import type { TFunction } from "i18next";
import { useRef, useState } from "react";
import { Trans, useTranslation } from "react-i18next";
import { toast } from "sonner";
import { ErrorState, SkeletonRows } from "@/components/common/feedback";
import { Time } from "@/components/common/misc";
import { Page } from "@/components/common/page";
import { EmptyState } from "@/components/ui/empty-state";
import { PageHeader } from "@/components/ui/page-header";
import { Panel } from "@/components/ui/panel";
import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import { CheckField } from "@/components/ui/checkbox";
import { ConfirmDialog } from "@/components/ui/confirm-dialog";
import { DataTable, type Column } from "@/components/ui/data-table";
import { Field, Textarea } from "@/components/ui/input";
import { Select } from "@/components/ui/select";
import { Sheet } from "@/components/ui/sheet";
import { StepMark, type StepState, stepNow } from "@/components/ui/step-mark";
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

const actionTones: Record<S["VraItemPlan"]["action"], Tone> = {
  create: "accent",
  update: "run",
  unchanged: "neutral",
  conflict: "bad",
  skip: "outline",
};
const groupActionTones: Record<S["VraPropertyGroupPlan"]["action"], Tone> = {
  create: "accent",
  unchanged: "neutral",
  differs: "warn",
};
const templateActionTones: Record<S["VraTemplatePlan"]["action"], Tone> = {
  ...groupActionTones,
  skip: "outline",
};
const mappingTones: Record<S["VraItemPlan"]["status"], Tone> = {
  complete: "ok",
  partial: "warn",
  skipped: "outline",
};
const adoptionTones: Record<S["VraDeploymentPlan"]["status"], Tone> = {
  adopt: "accent",
  adopted: "ok",
  skipped: "outline",
  failed: "bad",
};
const levelTones: Record<S["VraNote"]["level"], Tone> = {
  info: "neutral",
  warning: "warn",
  unmapped: "bad",
};
const groupTones = { A: "ok", B: "accent", C: "run", D: "outline" } as const satisfies Record<string, Tone>;
type Group = keyof typeof groupTones;
const isGroup = (g: string): g is Group => g in groupTones;
const kinds = [
  "workflow",
  "action",
  "abx_action",
  "catalog_item",
  "subscription",
  "day2_action",
  "form_field",
] as const;
// The kinds whose French name is feminine, for the words that agree with it.
const feminine = new Set(["action", "abx_action", "day2_action"]);
const kindLabel = (t: TFunction<"admin">, k: string) =>
  (kinds as readonly string[]).includes(k) ? t(`vra.kind.${k as (typeof kinds)[number]}`) : k;
/** The Select values of "no mapping" (by name, or none) and of "stays in vRA". */
const AUTO = "_auto";
const STAYS = "_stays";
/** Every mutation of the page: one at a time. */
const busyKey = ["vra"];

function Notes({ notes }: { notes: S["VraNote"][] }) {
  const { t } = useTranslation("admin");
  if (notes.length === 0) return <p className="text-secondary text-faint">{t("vra.nothingToNote")}</p>;
  return (
    <ul className="flex flex-col gap-2.5">
      {notes.map((n, i) => (
        <li key={i} className="flex flex-wrap items-baseline gap-x-2 gap-y-0.5 text-secondary">
          <Badge tone={levelTones[n.level]}>{t(`vra.level.${n.level}`)}</Badge>
          <code className="font-mono text-caption text-faint">{n.where}</code>
          {/* The plan's notes are the server's, in English. */}
          <span lang="en" className="min-w-0 basis-full">
            {n.text}
          </span>
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
  const { t } = useTranslation("admin");
  const check = notes.filter((n) => n.level === "warning").length;
  const out = notes.filter((n) => n.level === "unmapped").length;
  const parts = [check ? t("vra.toCheck", { count: check }) : "", out ? t("vra.leftOut", { count: out }) : ""].filter(
    Boolean,
  );
  const text = parts.length ? parts.join(" · ") : t("vra.n.note", { count: notes.length });
  return (
    <Button
      size="sm"
      variant="quiet"
      aria-haspopup="dialog"
      aria-label={t("vra.notesOnWith", { of, text })}
      onClick={() => onOpen({ of, notes })}
    >
      {text}
    </Button>
  );
}

function NotesSheet({ noted, onClose }: { noted: Noted; onClose: () => void }) {
  const { t } = useTranslation("admin");
  return (
    <Sheet
      open
      onOpenChange={(o) => !o && onClose()}
      title={t("vra.notesOn", { of: noted.of })}
      description={t("vra.notesDescription")}
    >
      <Notes notes={noted.notes} />
    </Sheet>
  );
}

function Summary({ plan }: { plan: Plan }) {
  const { t } = useTranslation("admin");
  const s = plan.summary;
  const stat = (label: string, n: number, warn = false) => (
    <div className="flex flex-col">
      <span className={cn("tnum text-title font-semibold", warn && "text-warn")}>{n}</span>
      <span className="text-secondary text-muted">{label}</span>
    </div>
  );
  return (
    <div className="grid gap-4 md:grid-cols-2">
      <Panel title={t("vra.summary.catalog")}>
        <div className="flex flex-wrap gap-x-8 gap-y-3">
          {stat(t("vra.summary.toCreate"), s.items_create)}
          {stat(t("vra.summary.newDrafts"), s.items_update)}
          {stat(t("vra.summary.unchanged"), s.items_unchanged)}
          {stat(t("vra.summary.notImported"), s.items_blocked, s.items_blocked > 0)}
          {stat(t("vra.summary.sharesToAdd"), s.shares_add)}
        </div>
      </Panel>
      <Panel title={t("vra.summary.deployments")}>
        <div className="flex flex-wrap gap-x-8 gap-y-3">
          {stat(t("vra.summary.toAdopt"), s.deployments_adopt)}
          {stat(t("vra.summary.adopted"), s.deployments_adopted)}
          {stat(t("vra.summary.leftInVra"), s.deployments_skipped, s.deployments_skipped > 0)}
        </div>
      </Panel>
    </div>
  );
}

const stepState = (done: boolean, now: boolean): StepState => (done ? "done" : now ? "now" : "later");

function planLeft(plan?: Plan) {
  const unplaced = plan?.projects.filter((p) => p.how === "none").length ?? 0;
  const s = plan?.summary;
  const items = s
    ? s.templates_create + s.property_groups_create + s.form_library_create + s.items_create + s.items_update
    : 0;
  const left = s ? items + s.shares_add + s.deployments_adopt : 0;
  return { unplaced, items, left };
}

/** The plan's tab for the step the move is at: projects to map, then what to import, then the move. */
function tabForStep(plan: Plan): VraTab {
  const { unplaced, left } = planLeft(plan);
  if (unplaced > 0) return "projects";
  if (left === 0) return "cutover";
  return plan.summary.deployments_adopt === left ? "deployments" : "items";
}

/** Where the move stands: read, map, import, move each project. */
function Steps({ read, plan }: { read: S["VraSnapshot"] | null; plan?: Plan }) {
  const { t } = useTranslation("admin");
  const { unplaced, items, left } = planLeft(plan);
  const s = plan?.summary;
  const steps = [
    {
      title: t("vra.step.read"),
      done: read !== null,
      detail: read ? (
        <Trans t={t} i18nKey="vra.step.readAt" components={{ time: <Time iso={read.read_at} /> }} />
      ) : (
        t("vra.step.readHint")
      ),
    },
    {
      title: t("vra.step.map"),
      done: Boolean(plan) && unplaced === 0,
      detail: !plan
        ? t("vra.step.mapHint")
        : unplaced > 0
          ? t("vra.step.unplaced", { count: unplaced })
          : t("vra.step.mapped"),
    },
    {
      title: t("vra.step.import"),
      done: Boolean(plan) && left === 0,
      detail: !s
        ? t("vra.step.importHint")
        : left === 0
          ? t("vra.step.nothingLeft")
          : t("vra.step.left", {
              items: t("vra.n.item", { count: items }),
              shares: t("vra.n.share", { count: s.shares_add }),
              deployments: t("vra.n.deployment", { count: s.deployments_adopt }),
            }),
    },
    {
      title: t("vra.step.move"),
      done: plan?.cutover.switch_off ?? false,
      detail: plan
        ? t("vra.step.moved", { count: plan.cutover.moved, moved: plan.cutover.moved, total: plan.cutover.total })
        : t("vra.step.moveHint"),
    },
  ];
  const current = steps.findIndex((st) => !st.done);
  return (
    <ol
      aria-label={t("vra.steps")}
      className="grid gap-px overflow-hidden rounded-lg border border-line bg-line sm:grid-cols-2 xl:grid-cols-4"
    >
      {steps.map((st, i) => {
        const state = stepState(st.done, i === current);
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
                <span className="sr-only">{t("vra.stepState", { state: t(`vra.state.${state}`) })}</span>
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
  const { t } = useTranslation("admin");
  const qc = useQueryClient();
  const projects = useQuery(adm.projectNames());
  const map = useMutation({
    mutationKey: busyKey,
    mutationFn: ({ vra, to }: { vra: string; to: string }) =>
      call(
        api.PUT("/admin/vra/projects/{vraProjectId}", {
          params: { path: { vraProjectId: vra } },
          body: { rvav_project_id: to === AUTO || to === STAYS ? null : to, stays_in_vra: to === STAYS },
        }),
      ),
    // The row stays busy until the plan shows the new mapping.
    onSuccess: () => qc.invalidateQueries({ queryKey: ["admin", "vra", "plan"] }),
    onError: (e) => failed(e, t("vra.projects.notMapped")),
  });
  const namesake = (name: string) => projects.data?.items.find((x) => x.name.toLowerCase() === name.toLowerCase());
  const strategy = (p: S["VraProjectPlan"]) => (p.strategy ? t(`vra.strategy.${p.strategy}`) : null);
  const columns: Column<S["VraProjectPlan"]>[] = [
    {
      id: "vra",
      header: t("vra.projects.vra"),
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
      header: t("vra.projects.rvav"),
      className: "w-72",
      cell: (p) => (
        <Select
          aria-label={t("vra.projects.rvavFor", { name: p.name })}
          value={p.how === "mapped" ? (p.rvav_project_id ?? AUTO) : p.how === "stays" ? STAYS : AUTO}
          loading={map.isPending && map.variables.vra === p.vra_id}
          onChange={(v) => map.mutate({ vra: p.vra_id, to: v })}
          options={[
            {
              value: AUTO,
              label: namesake(p.name)
                ? t("vra.projects.byName", { name: namesake(p.name)?.name })
                : t("vra.projects.byNameNone"),
            },
            { value: STAYS, label: t("vra.projects.stays") },
            ...(projects.data?.items ?? []).map((x) => ({ value: x.id, label: x.name })),
          ]}
        />
      ),
    },
    {
      id: "strategy",
      header: t("vra.projects.strategy"),
      sortValue: strategy,
      cell: (p) => (
        <>
          {strategy(p) ?? <span className="text-faint">{t("vra.projects.none")}</span>}
          {p.placement_policy && (
            <div className="font-mono text-caption text-faint">
              {t("vra.projects.vraValue", { value: p.placement_policy })}
            </div>
          )}
        </>
      ),
    },
    {
      id: "properties",
      header: t("vra.projects.properties"),
      cell: (p) => <ProjectProperties p={p} />,
    },
    {
      id: "sharing",
      header: t("vra.projects.sharing"),
      sortValue: (p) => t(`vra.sharing.${p.deployment_sharing}`),
      cell: (p) => <ProjectSharing p={p} />,
    },
    {
      id: "ad",
      header: t("vra.projects.ad"),
      cell: (p) =>
        p.ad_ou ? (
          <>
            <span className="font-mono text-caption">{p.ad_ou}</span>
            {p.ad_blueprint_ou && <div className="text-caption text-faint">{t("vra.projects.templatesChoose")}</div>}
          </>
        ) : (
          <span className="text-faint">{p.ad_none ? t("vra.projects.noComputerObjects") : t("vra.projects.none")}</span>
        ),
    },
    {
      id: "items",
      header: t("vra.projects.itemsShared"),
      sortValue: (p) => p.items,
      cell: (p) => <span className="tnum">{p.items}</span>,
    },
    {
      id: "deployments",
      header: t("vra.projects.deployments"),
      sortValue: (p) => p.deployments,
      cell: (p) => <span className="tnum">{p.deployments}</span>,
    },
  ];
  return (
    <>
      <DataTable
        label={t("vra.projects.label")}
        columns={columns}
        rows={plan.projects}
        rowKey={(p) => p.vra_id}
        minWidth={960}
        toolbar={
          projects.isError && (
            <p className="text-secondary text-bad">
              {t("vra.projects.notLoaded")}{" "}
              <Button size="sm" variant="quiet" onClick={() => void projects.refetch()}>
                {t("vra.projects.retry")}
              </Button>
            </p>
          )
        }
      />
    </>
  );
}

// The import leaves the rvav project's deployment sharing alone: say where it differs.
function ProjectSharing({ p }: { p: S["VraProjectPlan"] }) {
  const { t } = useTranslation("admin");
  const rvav = p.rvav_deployment_sharing;
  return (
    <>
      {t(`vra.sharing.${p.deployment_sharing}`)}
      {rvav && rvav !== p.deployment_sharing && (
        <div className="text-caption text-muted">
          {t(`vra.projects.sharingDiffers.${rvav}`, { project: p.rvav_project_name })}
        </div>
      )}
    </>
  );
}

// What importing the catalog adds to the rvav project, and what it can't.
function ProjectProperties({ p }: { p: S["VraProjectPlan"] }) {
  const { t } = useTranslation("admin");
  const add = Object.entries(p.properties_add ?? {});
  const unmapped = p.properties_unmapped ?? [];
  if (add.length + unmapped.length === 0) return <span className="text-faint">{t("vra.projects.noneToAdd")}</span>;
  return (
    <>
      {add.length > 0 && (
        <ul
          aria-label={t("vra.projects.added", {
            project: p.rvav_project_name ?? t("vra.projects.theRvavProject"),
          })}
        >
          {add.map(([k, v]) => (
            <li key={k} className="font-mono text-caption">
              {k}={v}
            </li>
          ))}
        </ul>
      )}
      {unmapped.length > 0 && (
        <div className="text-caption text-warn">{t("vra.projects.leftOut", { names: unmapped.join(", ") })}</div>
      )}
    </>
  );
}

function ItemsTab({ plan, onNotes }: { plan: Plan; onNotes: (n: Noted) => void }) {
  const { t } = useTranslation("admin");
  const names = new Map(
    plan.projects.filter((p) => p.rvav_project_id).map((p) => [p.rvav_project_id!, p.rvav_project_name]),
  );
  const columns: Column<S["VraItemPlan"]>[] = [
    {
      id: "template",
      header: t("vra.items.template"),
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
      header: t("vra.items.maps"),
      sortValue: (it) => t(`vra.maps.${it.status}`),
      cell: (it) => <Badge tone={mappingTones[it.status]}>{t(`vra.maps.${it.status}`)}</Badge>,
    },
    {
      id: "import",
      header: t("vra.items.import"),
      sortValue: (it) => t(`vra.import.${it.action}`),
      cell: (it) => (
        <>
          <Badge tone={actionTones[it.action]}>{t(`vra.import.${it.action}`)}</Badge>
          {it.reason && <div className="mt-1 max-w-64 text-secondary text-muted">{it.reason}</div>}
        </>
      ),
    },
    {
      id: "shared",
      header: t("vra.items.sharedWith"),
      cell: (it) => (
        <span className="text-secondary">
          {it.share_add.length > 0 && (
            <div>
              {t("vra.items.adds", {
                projects: it.share_add
                  .map((id) =>
                    it.share_audiences?.some((a) => a.project_id === id)
                      ? t("vra.items.chosenPeople", { project: names.get(id) ?? id })
                      : (names.get(id) ?? id),
                  )
                  .join(", "),
              })}
            </div>
          )}
          {it.share_unmapped.length > 0 && (
            <div className="text-warn">{t("vra.items.notMapped", { projects: it.share_unmapped.join(", ") })}</div>
          )}
          {it.share_add.length === 0 && it.share_unmapped.length === 0 && (
            <span className="text-faint">{t("vra.items.nothingToAdd")}</span>
          )}
        </span>
      ),
    },
    {
      id: "notes",
      header: t("vra.items.notes"),
      cell: (it) => <NotesButton of={it.name} notes={it.notes} onOpen={onNotes} />,
    },
  ];
  return (
    <>
      <DataTable
        label={t("vra.items.label")}
        columns={columns}
        rows={plan.items}
        rowKey={(it) => it.blueprint_id}
        minWidth={820}
        empty={<EmptyState title={t("vra.items.none")} body={t("vra.items.noneBody")} />}
      />
      {plan.templates.length > 0 && <Templates plan={plan} onNotes={onNotes} />}
      {plan.property_groups.length > 0 && <PropertyGroups plan={plan} onNotes={onNotes} />}
      {plan.form_library.length > 0 && <FormLibrary plan={plan} onNotes={onNotes} />}
    </>
  );
}

/** vRA's image mappings, made rvav templates before the items that use them. */
function Templates({ plan, onNotes }: { plan: Plan; onNotes: (n: Noted) => void }) {
  const { t } = useTranslation("admin");
  const why = (tp: S["VraTemplatePlan"]) =>
    tp.action === "differs" || tp.action === "skip" ? t(`vra.templates.${tp.action}`) : null;
  const columns: Column<S["VraTemplatePlan"]>[] = [
    {
      id: "template",
      header: t("vra.templates.template"),
      sortValue: (tp) => tp.label,
      cell: (tp) => (
        <>
          <span className="font-medium">{tp.label}</span>
          <div className="text-caption text-faint">
            <span className="font-mono">{tp.key}</span>
            {[
              tp.os_family === "windows" ? "Windows" : "Linux",
              tp.user_data ? "cloud-init" : "",
              tp.constraints.length ? t("vra.n.constraint", { count: tp.constraints.length }) : "",
            ]
              .filter(Boolean)
              .map((x) => ` · ${x}`)
              .join("")}
          </div>
        </>
      ),
    },
    {
      id: "import",
      header: t("vra.items.import"),
      sortValue: (tp) => t(`vra.import.${tp.action}`),
      cell: (tp) => (
        <>
          <Badge tone={templateActionTones[tp.action]}>{t(`vra.import.${tp.action}`)}</Badge>
          {why(tp) && <div className="mt-1 max-w-64 text-secondary text-muted">{why(tp)}</div>}
        </>
      ),
    },
    {
      id: "sources",
      header: t("vra.templates.inVcenter"),
      cell: (tp) =>
        tp.sources.length ? (
          <ul className="flex flex-col text-secondary">
            {tp.sources.map((s) => (
              <li key={s.vcenter}>
                {t("vra.templates.source", { vcenter: s.vcenter || t("vra.templates.integrations") })}{" "}
                <span className="font-mono">{s.template}</span>
              </li>
            ))}
          </ul>
        ) : (
          <span className="text-secondary text-faint">{t("vra.templates.none")}</span>
        ),
    },
    {
      id: "notes",
      header: t("vra.items.notes"),
      cell: (tp) => <NotesButton of={tp.label} notes={tp.notes} onOpen={onNotes} />,
    },
  ];
  return (
    <section aria-labelledby="vra-templates" className="mt-6">
      <h3 id="vra-templates" className="px-4 pb-2 text-body font-medium">
        {t("vra.templates.label")}
      </h3>
      <DataTable
        label={t("vra.templates.label")}
        columns={columns}
        rows={plan.templates}
        rowKey={(tp) => tp.key}
        minWidth={720}
      />
    </section>
  );
}

/** vRA's property groups, made in rvav before the items that name them. */
function PropertyGroups({ plan, onNotes }: { plan: Plan; onNotes: (n: Noted) => void }) {
  const { t } = useTranslation("admin");
  const columns: Column<S["VraPropertyGroupPlan"]>[] = [
    {
      id: "group",
      header: t("vra.groups.group"),
      sortValue: (g) => g.label,
      cell: (g) => (
        <>
          <span className="font-medium">{g.label}</span>
          <div className="font-mono text-caption text-faint">
            {g.key} ·{" "}
            {g.type === "input"
              ? t("vra.n.input", { count: g.inputs.length })
              : t("vra.n.constant", { count: Object.keys(g.constants).length })}
          </div>
        </>
      ),
    },
    {
      id: "import",
      header: t("vra.items.import"),
      sortValue: (g) => t(`vra.import.${g.action}`),
      cell: (g) => (
        <>
          <Badge tone={groupActionTones[g.action]}>{t(`vra.import.${g.action}`)}</Badge>
          {g.action === "differs" && (
            <div className="mt-1 max-w-64 text-secondary text-muted">
              {t("vra.versionNamed", { version: g.version })}
            </div>
          )}
        </>
      ),
    },
    {
      id: "notes",
      header: t("vra.items.notes"),
      cell: (g) => <NotesButton of={g.label} notes={g.notes} onOpen={onNotes} />,
    },
  ];
  return (
    <section aria-labelledby="vra-property-groups" className="mt-6">
      <h3 id="vra-property-groups" className="px-4 pb-2 text-body font-medium">
        {t("vra.groups.label")}
      </h3>
      <DataTable
        label={t("vra.groups.label")}
        columns={columns}
        rows={plan.property_groups}
        rowKey={(g) => g.vra_id}
        minWidth={560}
      />
    </section>
  );
}

/** The vRO actions custom forms use, made form library entries before the items that name them (ADR-0043). */
function FormLibrary({ plan, onNotes }: { plan: Plan; onNotes: (n: Noted) => void }) {
  const { t } = useTranslation("admin");
  const why = (e: S["VraFormLibraryPlan"]) =>
    e.action === "differs"
      ? t("vra.versionNamed", { version: e.version })
      : e.action === "skip"
        ? t("vra.library.skip")
        : null;
  const columns: Column<S["VraFormLibraryPlan"]>[] = [
    {
      id: "action",
      header: t("vra.library.action"),
      sortValue: (e) => e.vro_action,
      cell: (e) => (
        <>
          <span className="font-medium">{e.vro_action}</span>
          <div className="font-mono text-caption text-faint">
            {e.key}
            {e.url && ` · ${e.url}`}
          </div>
        </>
      ),
    },
    {
      id: "import",
      header: t("vra.items.import"),
      sortValue: (e) => t(`vra.import.${e.action}`),
      cell: (e) => (
        <>
          <Badge tone={templateActionTones[e.action]}>{t(`vra.import.${e.action}`)}</Badge>
          {why(e) && <div className="mt-1 max-w-64 text-secondary text-muted">{why(e)}</div>}
        </>
      ),
    },
    {
      id: "notes",
      header: t("vra.items.notes"),
      cell: (e) => <NotesButton of={e.vro_action} notes={e.notes} onOpen={onNotes} />,
    },
  ];
  return (
    <section aria-labelledby="vra-form-library" className="mt-6">
      <h3 id="vra-form-library" className="px-4 pb-2 text-body font-medium">
        {t("vra.library.label")}
      </h3>
      <DataTable
        label={t("vra.library.label")}
        columns={columns}
        rows={plan.form_library}
        rowKey={(e) => e.vro_action}
        minWidth={620}
      />
    </section>
  );
}

function DeploymentsTab({ plan, onNotes }: { plan: Plan; onNotes: (n: Noted) => void }) {
  const { t } = useTranslation("admin");
  const columns: Column<S["VraDeploymentPlan"]>[] = [
    {
      id: "deployment",
      header: t("vra.deployments.deployment"),
      sortValue: (d) => d.name,
      cell: (d) => (
        <>
          <span className="font-medium">{d.name}</span>
          <div className="text-caption text-faint">
            {d.vra_project}
            {d.environment ? ` · ${d.environment}` : ""}
            {d.rvav_name && d.rvav_name !== d.name ? ` · ${t("vra.deployments.inRvav", { name: d.rvav_name })}` : ""}
          </div>
        </>
      ),
    },
    {
      id: "owner",
      header: t("vra.deployments.owner"),
      sortValue: (d) => d.owner,
      cell: (d) => <span className="text-secondary">{d.owner}</span>,
    },
    {
      id: "vms",
      header: t("vra.deployments.vms"),
      cell: (d) =>
        d.vms?.length ? (
          <ul className="flex flex-col gap-0.5">
            {d.vms.map((v) => (
              <li key={`${v.vcenter}/${v.moref}/${v.name}`} className="flex flex-wrap items-center gap-1.5">
                <span className="font-mono text-secondary">{v.name}</span>
                {v.prevent_delete && (
                  <Badge tone="neutral" title={t("vra.deployments.keptHint")}>
                    <Lock aria-hidden /> {t("vra.deployments.kept")}
                  </Badge>
                )}
              </li>
            ))}
          </ul>
        ) : (
          <span className="text-faint">{t("vra.deployments.noneFound")}</span>
        ),
    },
    {
      id: "adoption",
      header: t("vra.deployments.adoption"),
      sortValue: (d) => t(`vra.adoption.${d.status}`),
      cell: (d) => {
        const why = [...d.notes].reverse().find((n) => n.level === "unmapped");
        return (
          <>
            <Badge tone={adoptionTones[d.status]}>{t(`vra.adoption.${d.status}`)}</Badge>
            {why && (
              <div lang="en" className="mt-1 max-w-72 text-secondary text-muted">
                {why.text}
              </div>
            )}
          </>
        );
      },
    },
    {
      id: "notes",
      header: t("vra.items.notes"),
      cell: (d) => <NotesButton of={d.name} notes={d.notes} onOpen={onNotes} />,
    },
  ];
  return (
    <DataTable
      label={t("vra.deployments.label")}
      columns={columns}
      rows={plan.deployments}
      rowKey={(d) => d.vra_id}
      minWidth={820}
      empty={<EmptyState title={t("vra.deployments.none")} body={t("vra.deployments.noneBody")} />}
    />
  );
}

function PoliciesTab({ plan }: { plan: Plan }) {
  const { t } = useTranslation("admin");
  return (
    <div className="flex flex-col gap-3 p-4">
      <p className="max-w-3xl text-secondary text-muted">{t("vra.policies.intro")}</p>
      {plan.policies.map((p) => (
        <section key={p.vra_id} className="rounded-md border border-line p-3" aria-label={p.name}>
          <div className="flex flex-wrap items-center gap-2">
            <h3 className="text-body font-medium">{p.name}</h3>
            <Badge tone="outline">{p.type}</Badge>
            {p.enforcement && (
              <Badge tone={p.enforcement === "soft" ? "warn" : "neutral"}>
                {t(`vra.enforcement.${p.enforcement}`)}
              </Badge>
            )}
            <span className="text-secondary text-muted">
              {p.vra_project ? t("vra.policies.inProject", { project: p.vra_project }) : t("vra.policies.everyProject")}
            </span>
          </div>
          <p className="mt-1 mb-2 text-secondary">{t("vra.policies.becomes", { what: p.becomes })}</p>
          <Notes notes={p.notes} />
        </section>
      ))}
    </div>
  );
}

type TriageItem = Plan["triage"]["items"][number];

const triageColumnsOf = (t: TFunction<"admin">): Column<TriageItem>[] => [
  {
    id: "name",
    header: t("vra.triage.name"),
    sortValue: (it) => it.name,
    cell: (it) => (
      <>
        <span className="font-medium">{it.name}</span>
        <div className="text-caption text-faint">
          {kindLabel(t, it.kind)}
          {it.category && it.kind === "workflow" ? ` · ${it.category}` : ""}
          {it.systems.length > 0 ? ` · ${it.systems.join(", ")}` : ""}
        </div>
      </>
    ),
  },
  {
    id: "used",
    header: t("vra.triage.usedBy"),
    sortValue: (it) => it.used_by.length,
    cell: (it) => (
      <span className="text-secondary">
        {it.used_by.length === 0 && <span className="text-faint">{t("vra.triage.nothing")}</span>}
        {it.used_by.map((u, i) => (
          <div key={i} className={u.disabled ? "text-faint" : ""}>
            {kindLabel(t, u.kind)} {u.name}
            {u.disabled
              ? ` ${t("vra.triage.disabled", { context: feminine.has(u.kind) ? "feminine" : undefined })}`
              : ""}
          </div>
        ))}
      </span>
    ),
  },
  {
    id: "run",
    header: t("vra.triage.lastRun"),
    sortValue: (it) => it.last_run ?? null,
    className: "whitespace-nowrap",
    cell: (it) => (
      <span className="text-secondary">
        {it.kind !== "workflow" ? "–" : it.last_run ? <Time iso={it.last_run} /> : t("vra.triage.never")}
      </span>
    ),
  },
  {
    id: "group",
    header: t("vra.triage.group"),
    sortValue: (it) => it.group,
    cell: (it) => (
      <>
        <Badge tone={isGroup(it.group) ? groupTones[it.group] : "neutral"}>
          {isGroup(it.group) ? t(`vra.group.${it.group}`) : it.group}
        </Badge>
        <div className="mt-1 text-caption text-muted">{it.becomes}</div>
      </>
    ),
  },
  {
    id: "why",
    header: t("vra.triage.why"),
    className: "max-w-md",
    cell: (it) => <span className="text-secondary">{it.reasons.join(" ")}</span>,
  },
];

function VroTab({ plan }: { plan: Plan }) {
  const { t } = useTranslation("admin");
  const triage = plan.triage;
  const download = useMutation({
    mutationFn: async () => {
      const text = await call(api.GET("/admin/vra/triage", { parseAs: "text" }));
      const url = URL.createObjectURL(new Blob([String(text)], { type: "text/markdown" }));
      Object.assign(document.createElement("a"), { href: url, download: "vro-triage.md" }).click();
      URL.revokeObjectURL(url);
    },
    onError: (e) => toast.error(isApiError(e) ? e.problem.title : t("vra.triage.reportFailed")),
  });
  return (
    <DataTable
      label={t("vra.triage.label")}
      columns={triageColumnsOf(t)}
      rows={triage.items}
      rowKey={(it) => it.kind + it.id}
      minWidth={900}
      toolbar={
        <div className="flex w-full flex-wrap items-center gap-3 text-secondary">
          {(Object.keys(groupTones) as Group[]).map((g) => (
            <span key={g} className="flex items-center gap-1.5">
              <Badge tone={groupTones[g]}>{t(`vra.group.${g}`)}</Badge>
              <span className="tnum">{triage.groups[g] ?? 0}</span>
            </span>
          ))}
          <span className="text-muted">{t("vra.triage.library", { n: triage.library })}</span>
          <Button
            className="ml-auto"
            size="sm"
            variant="secondary"
            disabled={download.isPending}
            onClick={() => download.mutate()}
          >
            <Download /> {t("vra.triage.report")}
          </Button>
        </div>
      }
      empty={<EmptyState title={t("vra.triage.none")} body={t("vra.triage.noneBody")} />}
    />
  );
}

type CheckLook = [React.ElementType, string, "ok" | "warn" | "todo" | "manual"];
const notYet: CheckLook = [Circle, "text-faint", "todo"];
const checkIcons: Record<string, CheckLook> = {
  ok: [CircleCheck, "text-ok", "ok"],
  warn: [TriangleAlert, "text-warn", "warn"],
  todo: notYet,
  manual: [Hand, "text-muted", "manual"],
};

function MoveSheet({ project, onClose }: { project: S["VraCutoverProject"]; onClose: () => void }) {
  const { t } = useTranslation("admin");
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
      toast.success(t("vra.move.toast", { name: project.name }));
      onClose();
    },
    onError: (e) => failed(e, t("vra.move.notMarked")),
  });
  return (
    <Sheet
      open
      onOpenChange={(o) => !o && onClose()}
      pending={move.isPending}
      title={t("vra.move.title", { name: project.name })}
      onSubmit={() => move.mutate()}
      submitLabel={move.isPending ? t("vra.move.marking") : t("vra.move.mark")}
    >
      <p className="text-body">{t("vra.move.body", { project: project.rvav_project_name })}</p>
      {!project.ready && <p className="text-warn">{t("vra.move.notReady")}</p>}
      <Field label={t("vra.move.note")} htmlFor="vra-move-note" hint={t("vra.move.noteHint")}>
        <Textarea id="vra-move-note" rows={3} maxLength={500} value={note} onChange={(e) => setNote(e.target.value)} />
      </Field>
    </Sheet>
  );
}

function UndoMove({ project, onClose }: { project: S["VraCutoverProject"]; onClose: () => void }) {
  const { t } = useTranslation("admin");
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
      toast.success(t("vra.undo.toast", { name: project.name }));
      onClose();
    },
    onError: (e) => {
      onClose();
      failed(e, t("vra.undo.notChanged"));
    },
  });
  return (
    <ConfirmDialog
      open
      onOpenChange={(o) => !o && onClose()}
      title={t("vra.undo.title", { name: project.name })}
      description={t("vra.undo.description")}
      consequences={t("vra.undo.consequences")}
      confirmLabel={t("vra.undo.confirm")}
      pending={undo.isPending}
      onConfirm={() => undo.mutate()}
    />
  );
}

function CutoverTab({ plan, busy }: { plan: Plan; busy: boolean }) {
  const { t } = useTranslation("admin");
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
      if (r.adopted.length)
        toast.success(
          t("vra.cutover.adoptedOf", {
            count: r.adopted.length,
            deployments: t("vra.n.deployment", { count: r.adopted.length }),
            project: cp.name,
          }),
        );
      if (r.failed.length)
        toast.error(
          t("vra.cutover.notAdopted", {
            count: r.failed.length,
            deployments: t("vra.n.deployment", { count: r.failed.length }),
          }),
          { description: r.failed.map((f) => t("vra.failure", { name: f.name, reason: f.reason })).join(" ") },
        );
      if (!r.adopted.length && !r.failed.length) toast(t("vra.cutover.nothingToAdopt", { project: cp.name }));
    },
    onError: (e) => {
      void qc.invalidateQueries({ queryKey: ["admin", "vra", "plan"] });
      failed(e, t("vra.cutover.stopped"));
    },
  });
  const toAdopt = (cp: S["VraCutoverProject"]) =>
    plan.deployments.filter((d) => d.vra_project === cp.name && d.status === "adopt").length;
  return (
    <div className="flex flex-col gap-3 p-4">
      <p className="text-body" aria-live="polite">
        <span className="font-medium">
          {t("vra.cutover.movedOf", {
            count: c.total,
            moved: c.moved,
            projects: t("vra.n.project", { count: c.total }),
          })}
        </span>
        {" · "}
        {c.deployments_left.length === 0
          ? t("vra.cutover.everyAdopted")
          : t("vra.cutover.stillInVra", {
              deployments: t("vra.n.deployment", { count: c.deployments_left.length }),
            })}
        {" · "}
        {c.switch_off ? (
          <span className="text-ok">{t("vra.cutover.canSwitchOff")}</span>
        ) : (
          <span className="text-muted">{t("vra.cutover.switchOffWhen")}</span>
        )}
      </p>
      {c.projects.map((cp) => {
        const deployments = t("vra.n.deployment", { count: toAdopt(cp) });
        return (
          <section key={cp.vra_id} className="rounded-md border border-line" aria-label={cp.name}>
            <div className="flex flex-wrap items-center gap-2 border-b border-line px-3 py-2">
              <h3 className="text-body font-medium">{cp.name}</h3>
              {cp.rvav_project_name && (
                <span className="text-secondary text-muted">
                  {t("vra.cutover.to", { project: cp.rvav_project_name })}
                </span>
              )}
              {cp.moved_at ? (
                <Badge tone="ok">
                  <Trans
                    t={t}
                    i18nKey={cp.moved_by ? "vra.cutover.movedAtBy" : "vra.cutover.movedAt"}
                    values={{ name: cp.moved_by }}
                    components={{ time: <Time iso={cp.moved_at} /> }}
                  />
                </Badge>
              ) : cp.ready ? (
                <Badge tone="accent">{t("vra.cutover.ready")}</Badge>
              ) : (
                <Badge tone="outline">{t("vra.cutover.notReady")}</Badge>
              )}
              <span className="ml-auto flex flex-wrap gap-1.5">
                {toAdopt(cp) > 0 && (
                  <Button
                    size="sm"
                    variant="secondary"
                    disabled={busy}
                    aria-label={t("vra.cutover.adoptFor", { deployments, project: cp.name })}
                    onClick={() => adopt.mutate(cp)}
                  >
                    {t("vra.cutover.adopt", { deployments })}
                  </Button>
                )}
                {cp.moved_at ? (
                  <Button
                    size="sm"
                    variant="quiet"
                    aria-label={t("vra.cutover.notMovedFor", { project: cp.name })}
                    onClick={() => setUndoing(cp)}
                  >
                    {t("vra.cutover.notMoved")}
                  </Button>
                ) : cp.rvav_project_id ? (
                  <Button
                    size="sm"
                    variant="secondary"
                    aria-label={t("vra.cutover.markMovedFor", { project: cp.name })}
                    onClick={() => setMoving(cp)}
                  >
                    {t("vra.cutover.markMoved")}
                  </Button>
                ) : (
                  <span className="self-center text-secondary text-muted">{t("vra.cutover.onceMapped")}</span>
                )}
              </span>
            </div>
            {cp.note && <p className="px-3 pt-2 text-secondary text-muted">{cp.note}</p>}
            <ul className="flex flex-col gap-1.5 px-3 py-2">
              {cp.checks.map((ch) => {
                const [Icon, tone, state] = checkIcons[ch.state] ?? notYet;
                return (
                  <li key={ch.key} className="flex items-start gap-2 text-secondary">
                    <Icon
                      className={"mt-px size-3.5 shrink-0 " + tone}
                      aria-label={t(`vra.check.${state}`)}
                      role="img"
                    />
                    <span>
                      <span className="font-medium">{ch.label}.</span> {ch.detail}
                    </span>
                  </li>
                );
              })}
            </ul>
          </section>
        );
      })}
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
  const { t } = useTranslation("admin");
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
      failed(e, t("vra.importSheet.stopped"));
    },
  });
  const catalogHint = [
    s.templates_create > 0 &&
      t("vra.importSheet.templates", { templates: t("vra.n.template", { count: s.templates_create }) }),
    s.property_groups_create > 0 &&
      t("vra.importSheet.groups", { groups: t("vra.n.propertyGroup", { count: s.property_groups_create }) }),
    s.form_library_create > 0 &&
      t("vra.importSheet.library", { entries: t("vra.n.libraryEntry", { count: s.form_library_create }) }),
    t("vra.importSheet.items", {
      items: t("vra.n.item", { count: s.items_create }),
      drafts: t("vra.n.newDraft", { count: s.items_update }),
      shares: t("vra.n.share", { count: s.shares_add }),
    }),
  ]
    .filter(Boolean)
    .join(" ");
  return (
    <Sheet
      open
      onOpenChange={(o) => !o && onClose()}
      pending={run.isPending}
      title={t("vra.importSheet.title")}
      description={t("vra.importSheet.description")}
      onSubmit={() => run.mutate()}
      submitLabel={run.isPending ? t("vra.importSheet.importing") : t("vra.importSheet.submit")}
      submitDisabled={!catalog && !deployments}
    >
      <CheckField
        id="vra-catalog"
        label={<span className="font-medium">{t("vra.importSheet.catalog")}</span>}
        hint={catalogHint}
        checked={catalog}
        onChange={setCatalog}
      />
      <CheckField
        id="vra-deployments"
        label={<span className="font-medium">{t("vra.importSheet.deployments")}</span>}
        hint={t("vra.importSheet.deploymentsHint", {
          deployments: t("vra.n.deployment", { count: s.deployments_adopt }),
        })}
        checked={deployments}
        onChange={setDeployments}
      />
      {!catalog && !deployments && <p className="text-secondary text-muted">{t("vra.importSheet.choose")}</p>}
    </Sheet>
  );
}

function Result({ r, onClose }: { r: S["VraImportResult"]; onClose: () => void }) {
  const { t } = useTranslation(["admin", "common"]);
  const line = (label: string, names: string[]) =>
    names.length > 0 && (
      <p>
        <span className="font-medium">{t("vra.result.lead", { label })}</span> {names.join(", ")}
      </p>
    );
  return (
    <Panel
      title={t("vra.result.title")}
      actions={
        <Button size="sm" variant="quiet" onClick={onClose}>
          {t("common:actions.close")}
        </Button>
      }
    >
      <section aria-label={t("vra.result.title")} className="flex flex-col gap-1 text-body">
        {line(t("vra.result.templates"), r.templates_created)}
        {line(t("vra.result.groups"), r.property_groups_created)}
        {line(t("vra.result.library"), r.form_library_created)}
        {line(t("vra.result.projects"), r.projects_updated)}
        {line(t("vra.result.items"), r.created)}
        {line(t("vra.result.drafts"), r.updated)}
        {r.shared > 0 && (
          <p>{t("vra.result.shared", { count: r.shared, shares: t("vra.n.share", { count: r.shared }) })}</p>
        )}
        {line(t("vra.result.adopted"), r.adopted)}
        {r.failed.map((f) => (
          <p key={f.name} className="text-bad">
            {t("vra.failure", { name: f.name, reason: f.reason })}
          </p>
        ))}
        {r.templates_created.length +
          r.property_groups_created.length +
          r.form_library_created.length +
          r.projects_updated.length +
          r.created.length +
          r.updated.length +
          r.shared +
          r.adopted.length +
          r.failed.length ===
          0 && <p className="text-muted">{t("vra.result.nothing")}</p>}
      </section>
    </Panel>
  );
}

export function VraImportPage({ tab: asked }: { tab?: VraTab }) {
  const { t } = useTranslation(["admin", "shell"]);
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
      toast.success(s.source === "vra" ? t("vra.readToast") : t("vra.fileRead"));
    },
    onError: (e) => failed(e, t("vra.notRead")),
  });
  const p = plan.data;
  // Chosen once, as the plan first comes: mapping the last project mustn't move the tab away.
  const [opened, setOpened] = useState<VraTab>();
  if (p && !opened) setOpened(tabForStep(p));
  // What the plan was made from; the status's until there is one.
  const shown = p?.snapshot ?? snap;
  const configured = Boolean(status.data?.configured);
  // Before a plan, reading is the page's main action; after, importing.
  const readFirst = !p;
  return (
    <Page>
      <PageHeader
        title={t("shell:nav.admin.vraImport")}
        description={
          <>
            {t("vra.description")}
            {status.data && !configured && (
              <>
                {" "}
                <Trans
                  t={t}
                  i18nKey="vra.notConfigured"
                  components={{
                    integrations: (
                      <Link
                        to="/admin/integrations/$kind"
                        params={{ kind: "vra" }}
                        className="text-accent underline underline-offset-2"
                      />
                    ),
                  }}
                />
              </>
            )}
          </>
        }
        meta={
          shown && (
            <>
              <span>
                <Trans
                  t={t}
                  i18nKey={
                    shown.source === "file"
                      ? shown.created_by
                        ? "vra.readFromFileBy"
                        : "vra.readFromFile"
                      : shown.created_by
                        ? "vra.readFromBy"
                        : "vra.readFrom"
                  }
                  values={{ url: shown.url ?? "vRA", name: shown.created_by }}
                  components={{ time: <Time iso={shown.read_at} /> }}
                />
              </span>
              <span>
                {t("vra.counts", {
                  projects: t("vra.n.project", { count: shown.counts.projects }),
                  templates: t("vra.n.template", { count: shown.counts.templates }),
                  deployments: t("vra.n.deployment", { count: shown.counts.deployments }),
                  workflows: t("vra.n.vroWorkflow", { count: shown.counts.vro_workflows }),
                })}
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
              aria-label={t("vra.fileLabel")}
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
              <FileUp /> {t("vra.upload")}
            </Button>
            {configured && (
              <Button
                variant={readFirst ? "primary" : "secondary"}
                disabled={busy}
                onClick={() => read.mutate(undefined)}
              >
                <RefreshCw /> {read.isPending ? t("vra.reading") : t("vra.readVra")}
              </Button>
            )}
            {p && (
              <Button variant="primary" disabled={busy} onClick={() => setImporting(true)}>
                {t("vra.importMore")}
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
            title={t("vra.nothingRead")}
            body={configured ? t("vra.nothingReadConfigured") : t("vra.nothingReadFile")}
            action={
              configured ? (
                <Button size="sm" variant="primary" disabled={busy} onClick={() => read.mutate(undefined)}>
                  <RefreshCw /> {t("vra.readVra")}
                </Button>
              ) : (
                <Button size="sm" variant="primary" disabled={busy} onClick={() => file.current?.click()}>
                  <FileUp /> {t("vra.upload")}
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
              value={asked ?? opened ?? tabForStep(p)}
              onValueChange={(v) =>
                void navigate({ to: "/admin/vra-import", search: { tab: v as VraTab }, replace: true })
              }
            >
              <TabsList aria-label={t("vra.thePlan")} className="px-4 pt-2">
                <TabsTrigger value="projects">{t("vra.tab.projects")}</TabsTrigger>
                <TabsTrigger value="items">{t("vra.tab.items", { n: p.items.length })}</TabsTrigger>
                <TabsTrigger value="deployments">{t("vra.tab.deployments", { n: p.deployments.length })}</TabsTrigger>
                <TabsTrigger value="policies">{t("vra.tab.policies", { n: p.policies.length })}</TabsTrigger>
                <TabsTrigger value="vro">{t("vra.tab.vro", { n: p.triage.items.length })}</TabsTrigger>
                <TabsTrigger value="cutover">
                  {t("vra.tab.cutover", { moved: p.cutover.moved, total: p.cutover.total })}
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
            toast.success(t("vra.importedToast"));
          }}
        />
      )}
    </Page>
  );
}
