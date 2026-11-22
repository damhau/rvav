// Onboarding (S13.3, X1.5): administrators adopt VMs rvav didn't build into
// new deployments, with an owner, project, environment, catalog item and
// lease, or into an existing deployment; their addresses and DNS records come
// from EfficientIP. Nothing changes in vCenter or EfficientIP. Plans adopt
// the VMs a filter matches, by hand or every few hours.
import { useInfiniteQuery, useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { Link, useNavigate } from "@tanstack/react-router";
import type { TFunction } from "i18next";
import { ServerCog } from "lucide-react";
import { useState } from "react";
import { useTranslation } from "react-i18next";
import { toast } from "sonner";
import { ErrorState } from "@/components/common/feedback";
import { Page } from "@/components/common/page";
import { PowerDot } from "@/components/common/status";
import { Button } from "@/components/ui/button";
import { CheckField } from "@/components/ui/checkbox";
import { DataTable, type Column } from "@/components/ui/data-table";
import { EmptyState } from "@/components/ui/empty-state";
import { Field, Input } from "@/components/ui/input";
import { PageHeader } from "@/components/ui/page-header";
import { Panel } from "@/components/ui/panel";
import { Select } from "@/components/ui/select";
import { Sheet } from "@/components/ui/sheet";
import { Tabs, TabsContent, TabsList, TabsTrigger } from "@/components/ui/tabs";
import { api, call } from "@/lib/api/client";
import type { components } from "@/lib/api/schema";
import { formatSize } from "@/lib/format";
import { useDebounced } from "@/lib/hooks";
import { adm } from "@/lib/query";
import { failed, fieldErrors } from "./governance/common";
import { PlansTab, type Plan } from "./onboarding/plans";
import { emptyTarget, inputProblems, inputsOf, TargetFields, useFocusRefused, type Target } from "./onboarding/target";

type S = components["schemas"];
type Vm = S["OnboardingVm"];

export const onboardingTabs = ["vms", "plans"] as const;
export type OnboardingTab = (typeof onboardingTabs)[number];

const keyOf = (v: Vm) => `${v.vcenter}/${v.moref}`;
/** How many VMs one adoption takes (the server's limit). */
const MAX_ADOPT = 200;
const memory = (mb: number) => (mb % 1024 === 0 ? formatSize(mb / 1024, "GB") : formatSize(mb, "MB"));
const folderExample = "/GVA/vm/legacy";

function AdoptSheet({
  vms,
  onClose,
  onDone,
}: {
  vms: Vm[];
  onClose: () => void;
  onDone: (r: S["OnboardingResult"]) => void;
}) {
  const { t } = useTranslation("admin");
  const [target, setTarget] = useState<Target>(emptyTarget);
  const [errors, setErrors] = useState<Record<string, string>>({});
  const [local, setLocal] = useState<Record<number, string>>({});
  const [keep, setKeep] = useState(false);
  useFocusRefused(errors);
  const refs = vms.map((v) => ({ vcenter: v.vcenter, moref: v.moref, ...(keep ? { prevent_delete: true } : {}) }));
  const adopt = useMutation({
    mutationFn: () => {
      const component = target.component.trim() || undefined;
      if (target.into === "existing")
        return call(
          api.POST("/admin/onboarding/into", { body: { vms: refs, deployment_id: target.deployment, component } }),
        );
      return call(
        api.POST("/admin/onboarding", {
          body: {
            vms: refs,
            project_id: target.project,
            environment: target.environment,
            owner_id: target.owner,
            catalog_item_id: target.item,
            ...(target.lease ? { lease_days: Number(target.lease) } : {}),
            ...(target.into === "one" ? { deployment_name: target.name } : {}),
            component,
            inputs: inputsOf(target),
          },
        }),
      );
    },
    onSuccess: onDone,
    onError: (e) => {
      setErrors(fieldErrors(e));
      failed(e, t("onboarding.notAdopted"));
    },
  });
  // A VM's error names it.
  const vmErrors = Object.entries(errors)
    .filter(([k]) => k.startsWith("/vms"))
    .map(([k, msg]) => {
      const i = Number(k.split("/")[2]);
      return Number.isNaN(i) ? msg : t("onboarding.vmError", { name: vms[i]?.name ?? k, message: msg });
    });
  return (
    <Sheet
      open
      onOpenChange={(o) => !o && onClose()}
      pending={adopt.isPending}
      title={t("onboarding.adopt", { count: vms.length })}
      description={t("onboarding.sheetDescription")}
      className="w-[min(640px,100vw)]"
      onSubmit={() => {
        setErrors({});
        const problems = inputProblems(target);
        setLocal(problems);
        if (Object.keys(problems).length === 0) adopt.mutate();
      }}
      submitLabel={adopt.isPending ? t("onboarding.adopting") : t("onboarding.adopt", { count: vms.length })}
    >
      <TargetFields
        t={target}
        onChange={(next) => {
          // A row gone: the problems found by row no longer line up.
          if (next.inputs.length < target.inputs.length) setLocal({});
          setTarget(next);
        }}
        errors={errors}
        at=""
        id="ob"
        local={local}
      />
      {vmErrors.length > 0 && (
        <ul
          role="alert"
          className="flex flex-col gap-0.5 rounded-md border border-bad/40 bg-bad-soft p-3 text-secondary"
        >
          {vmErrors.map((m) => (
            <li key={m}>{m}</li>
          ))}
        </ul>
      )}
      <section aria-labelledby="ob-vms" className="flex flex-col gap-1.5">
        <h3 id="ob-vms" className="label">
          {t("onboarding.theVms")}
        </h3>
        <ul aria-label={t("onboarding.toAdopt")} className="max-h-48 overflow-y-auto font-mono text-caption">
          {vms.map((v) => (
            <li key={keyOf(v)}>
              {v.name} <span className="text-faint">{v.vcenter}</span>
            </li>
          ))}
        </ul>
        <CheckField
          id="ob-keep"
          className="mt-1"
          label={<span className="font-medium">{t("onboarding.keep")}</span>}
          hint={t("onboarding.keepHint")}
          checked={keep}
          onChange={setKeep}
        />
      </section>
    </Sheet>
  );
}

function Adopted({ result, onClose }: { result: S["OnboardingResult"]; onClose: () => void }) {
  const { t } = useTranslation(["admin", "common"]);
  const vms = result.deployments.reduce((n, d) => n + d.vms.length, 0);
  return (
    <Panel
      title={t("onboarding.adopted", {
        count: vms,
        vms: t("onboarding.vms", { count: vms }),
        deployments: t("onboarding.deployments", { count: result.deployments.length }),
      })}
      description={t("onboarding.adoptedDescription")}
      actions={
        <Button size="sm" variant="quiet" onClick={onClose}>
          {t("common:actions.close")}
        </Button>
      }
      flush
    >
      <ul className="max-h-64 overflow-y-auto text-body">
        {result.deployments.map((d) => (
          <li key={d.id} className="flex flex-wrap gap-x-3 border-b border-line px-4 py-1.5 last:border-0">
            <Link
              to="/deployments/$deploymentId"
              params={{ deploymentId: d.id }}
              className="font-medium text-accent underline underline-offset-2"
            >
              {d.name}
            </Link>
            <span className="text-muted">
              {d.vms.length > 1 ? t("onboarding.vmsOf", { count: d.vms.length }) : ""}
              {t("onboarding.counts", {
                addresses: t("onboarding.addresses", { count: d.addresses }),
                records: t("onboarding.records", { count: d.records }),
              })}
            </span>
          </li>
        ))}
      </ul>
    </Panel>
  );
}

const columnsOf = (t: TFunction<"admin">): Column<Vm>[] => [
  {
    id: "name",
    header: t("onboarding.name"),
    cell: (v) => (
      <span className="flex items-center gap-2">
        <PowerDot on={v.power_state === "on"} />
        <span className="flex flex-col">
          <span className="font-medium">{v.name}</span>
          <span className="font-mono text-caption text-faint">{v.moref}</span>
        </span>
      </span>
    ),
  },
  {
    id: "address",
    header: t("onboarding.address"),
    cell: (v) => (
      <span className="font-mono text-secondary">
        {v.ip_address ?? <span className="text-faint">{t("onboarding.none")}</span>}
        {v.hostname && <div className="text-caption text-faint">{v.hostname}</div>}
      </span>
    ),
  },
  {
    id: "size",
    header: t("onboarding.size"),
    cell: (v) => (
      <span className="tnum">{t("onboarding.shape", { cpu: v.cpu_count, memory: memory(v.memory_mb) })}</span>
    ),
  },
  { id: "os", header: t("onboarding.os"), cell: (v) => <span className="text-muted">{v.guest_os ?? ""}</span> },
  {
    id: "where",
    header: t("onboarding.where"),
    cell: (v) => (
      <span className="text-secondary text-muted">
        {v.cluster ?? ""}
        <div className="text-caption text-faint">{v.folder || v.vcenter}</div>
      </span>
    ),
  },
];

function VmsTab() {
  const { t } = useTranslation(["admin", "common"]);
  const qc = useQueryClient();
  const [search, setSearch] = useState("");
  const [vcenter, setVcenter] = useState("any");
  const [power, setPower] = useState("any");
  const [folder, setFolder] = useState("");
  const debounced = useDebounced(search, 250);
  const debouncedFolder = useDebounced(folder.trim(), 250);
  const vcenters = useQuery(adm.vcenters());
  // Loaded with the page: the side panel then opens on its Project field,
  // which stays disabled while it loads.
  useQuery(adm.projectNames());
  const filters = {
    q: debounced || undefined,
    vcenter: vcenter === "any" ? undefined : vcenter,
    power_state: power === "any" ? undefined : (power as S["VmPowerState"]),
    folder: debouncedFolder || undefined,
  };
  const filtered = search !== "" || vcenter !== "any" || power !== "any" || folder !== "";
  const list = useInfiniteQuery({
    queryKey: ["admin", "onboarding", filters],
    initialPageParam: undefined as string | undefined,
    queryFn: ({ pageParam, signal }) =>
      call(
        api.GET("/admin/onboarding/vms", { params: { query: { ...filters, limit: 100, cursor: pageParam } }, signal }),
      ),
    getNextPageParam: (last) => last.next_cursor,
  });
  const [selected, setSelected] = useState<Map<string, Vm>>(new Map());
  const [adopting, setAdopting] = useState(false);
  const [result, setResult] = useState<S["OnboardingResult"] | null>(null);
  const vms = list.data?.pages.flatMap((p) => p.items);
  const total = list.data?.pages[0]?.total ?? 0;
  // A selection outlives the filters: what it holds that isn't shown is said.
  const shown = new Map((vms ?? []).map((v) => [keyOf(v), v]));
  const hidden = [...selected.keys()].filter((k) => !shown.has(k)).length;
  const clearFilters = () => {
    setSearch("");
    setVcenter("any");
    setPower("any");
    setFolder("");
  };
  return (
    <div className="flex flex-col gap-4">
      {result && <Adopted result={result} onClose={() => setResult(null)} />}
      {selected.size > 0 && (
        <div
          className="flex flex-wrap items-center gap-2 rounded-lg border border-accent bg-accent-soft px-3 py-2"
          role="region"
          aria-label={t("onboarding.selectedVms")}
        >
          <span className="font-medium">{t("onboarding.selected", { count: selected.size })}</span>
          {selected.size > MAX_ADOPT && (
            <span className="text-secondary text-muted">{t("onboarding.atMost", { n: MAX_ADOPT })}</span>
          )}
          <span className="flex-1" />
          <Button size="sm" variant="primary" disabled={selected.size > MAX_ADOPT} onClick={() => setAdopting(true)}>
            {t("onboarding.adoptMore", { count: selected.size })}
          </Button>
          <Button size="sm" variant="quiet" onClick={() => setSelected(new Map())}>
            {t("onboarding.clearSelection")}
          </Button>
        </div>
      )}
      <Panel
        flush
        footer={
          list.hasNextPage && (
            <Button size="sm" disabled={list.isFetchingNextPage} onClick={() => void list.fetchNextPage()}>
              {list.isFetchingNextPage
                ? t("common:loadingEllipsis")
                : t("onboarding.showMore", { shown: vms?.length ?? 0, total })}
            </Button>
          )
        }
      >
        <DataTable
          label={t("onboarding.toAdopt")}
          columns={columnsOf(t)}
          rows={vms}
          rowKey={keyOf}
          minWidth={760}
          loading={list.isPending}
          error={list.isError && <ErrorState error={list.error} onRetry={() => void list.refetch()} />}
          selection={{
            selected: new Set(selected.keys()),
            onChange: (next) => setSelected(new Map([...next].map((k) => [k, selected.get(k) ?? shown.get(k)!]))),
            label: (v) => t("onboarding.select", { name: v.name }),
            allLabel: t("onboarding.selectAll"),
          }}
          toolbar={
            <div className="flex w-full flex-wrap items-end gap-2">
              <Field label={t("onboarding.search")} htmlFor="ob-search" className="w-56">
                <Input
                  id="ob-search"
                  value={search}
                  onChange={(e) => setSearch(e.target.value)}
                  placeholder={t("onboarding.searchPlaceholder")}
                />
              </Field>
              <Field label={t("onboarding.folder")} htmlFor="ob-folder" className="w-48">
                <Input
                  id="ob-folder"
                  className="font-mono"
                  value={folder}
                  onChange={(e) => setFolder(e.target.value)}
                  placeholder={folderExample}
                />
              </Field>
              <Field label="vCenter" htmlFor="ob-vcenter" className="w-48">
                <Select
                  id="ob-vcenter"
                  value={vcenter}
                  onChange={setVcenter}
                  options={[
                    { value: "any", label: t("onboarding.any") },
                    ...(vcenters.data?.items ?? []).map((v) => ({ value: v.name, label: v.name })),
                  ]}
                />
              </Field>
              <Field label={t("onboarding.power")} htmlFor="ob-power" className="w-32">
                <Select
                  id="ob-power"
                  value={power}
                  onChange={setPower}
                  options={[
                    { value: "any", label: t("onboarding.any") },
                    { value: "on", label: t("onboarding.on") },
                    { value: "off", label: t("onboarding.off") },
                    { value: "suspended", label: t("onboarding.suspended") },
                  ]}
                />
              </Field>
              <span className="ml-auto self-center text-secondary text-muted" aria-live="polite">
                {list.data ? t("onboarding.vms", { count: total }) : ""}
                {selected.size > 0 ? t("onboarding.selectedSuffix", { count: selected.size }) : ""}
                {hidden > 0 ? t("onboarding.hiddenSuffix", { count: hidden }) : ""}
                {selected.size > MAX_ADOPT ? t("onboarding.atMostSuffix", { n: MAX_ADOPT }) : ""}
              </span>
            </div>
          }
          empty={
            <EmptyState
              icon={ServerCog}
              title={t("onboarding.nothing")}
              body={filtered ? t("onboarding.noMatch") : t("onboarding.allManaged")}
              action={
                filtered && (
                  <Button size="sm" onClick={clearFilters}>
                    {t("onboarding.clear")}
                  </Button>
                )
              }
            />
          }
        />
      </Panel>
      {adopting && (
        <AdoptSheet
          vms={[...selected.values()]}
          onClose={() => setAdopting(false)}
          onDone={(r) => {
            setAdopting(false);
            setSelected(new Map());
            setResult(r);
            void qc.invalidateQueries({ queryKey: ["admin", "onboarding"] });
            void qc.invalidateQueries({ queryKey: ["deployments"] });
            void qc.invalidateQueries({ queryKey: ["vms"] });
            toast.success(t("onboarding.adoptedToast", { count: r.deployments.reduce((n, d) => n + d.vms.length, 0) }));
          }}
        />
      )}
    </div>
  );
}

export function OnboardingPage({ tab = "vms" }: { tab?: OnboardingTab }) {
  const { t } = useTranslation(["admin", "shell"]);
  const navigate = useNavigate();
  const plans = useQuery(adm.onboardingPlans());
  const [editing, setEditing] = useState<Plan | "new" | null>(null);
  return (
    <Page>
      <PageHeader
        title={t("shell:nav.admin.onboarding")}
        description={t("onboarding.description")}
        actions={
          tab === "plans" &&
          Boolean(plans.data?.items.length) && (
            <Button variant="primary" onClick={() => setEditing("new")}>
              {t("onboarding.newPlan")}
            </Button>
          )
        }
      />
      <Tabs
        value={tab}
        onValueChange={(next) =>
          void navigate({ to: "/admin/onboarding", search: { tab: next as OnboardingTab }, replace: true })
        }
      >
        <TabsList aria-label={t("onboarding.sections")}>
          <TabsTrigger value="vms">{t("onboarding.toAdopt")}</TabsTrigger>
          <TabsTrigger value="plans">{t("onboarding.plans")}</TabsTrigger>
        </TabsList>
        <TabsContent value="vms" className="pt-4">
          <VmsTab />
        </TabsContent>
        <TabsContent value="plans" className="pt-4">
          <PlansTab editing={editing} setEditing={setEditing} />
        </TabsContent>
      </Tabs>
    </Page>
  );
}
