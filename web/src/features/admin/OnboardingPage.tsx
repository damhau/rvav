// Onboarding (S13.3, X1.5): administrators adopt VMs rvav didn't build into
// new deployments, with an owner, project, environment, catalog item and
// lease, or into an existing deployment; their addresses and DNS records come
// from EfficientIP. Nothing changes in vCenter or EfficientIP. Plans adopt
// the VMs a filter matches, by hand or every few hours.
import { useInfiniteQuery, useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { Link, useNavigate } from "@tanstack/react-router";
import { ServerCog } from "lucide-react";
import { useState } from "react";
import { toast } from "sonner";
import { ErrorState } from "@/components/common/feedback";
import { Page } from "@/components/common/page";
import { PowerDot } from "@/components/common/status";
import { Button } from "@/components/ui/button";
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
const memory = (mb: number) => (mb % 1024 === 0 ? `${mb / 1024} GB` : `${mb} MB`);
const vmsText = (n: number) => `${n} VM${n === 1 ? "" : "s"}`;

function AdoptSheet({
  vms,
  onClose,
  onDone,
}: {
  vms: Vm[];
  onClose: () => void;
  onDone: (r: S["OnboardingResult"]) => void;
}) {
  const [t, setT] = useState<Target>(emptyTarget);
  const [errors, setErrors] = useState<Record<string, string>>({});
  const [local, setLocal] = useState<Record<number, string>>({});
  useFocusRefused(errors);
  const refs = vms.map((v) => ({ vcenter: v.vcenter, moref: v.moref }));
  const adopt = useMutation({
    mutationFn: () => {
      const component = t.component.trim() || undefined;
      if (t.into === "existing")
        return call(
          api.POST("/admin/onboarding/into", { body: { vms: refs, deployment_id: t.deployment, component } }),
        );
      return call(
        api.POST("/admin/onboarding", {
          body: {
            vms: refs,
            project_id: t.project,
            environment: t.environment,
            owner_id: t.owner,
            catalog_item_id: t.item,
            ...(t.lease ? { lease_days: Number(t.lease) } : {}),
            ...(t.into === "one" ? { deployment_name: t.name } : {}),
            component,
            inputs: inputsOf(t),
          },
        }),
      );
    },
    onSuccess: onDone,
    onError: (e) => {
      setErrors(fieldErrors(e));
      failed(e, "Nothing was adopted");
    },
  });
  // A VM's error names it.
  const vmErrors = Object.entries(errors)
    .filter(([k]) => k.startsWith("/vms"))
    .map(([k, msg]) => {
      const i = Number(k.split("/")[2]);
      return Number.isNaN(i) ? msg : `${vms[i]?.name ?? k}: ${msg}`;
    });
  return (
    <Sheet
      open
      onOpenChange={(o) => !o && onClose()}
      pending={adopt.isPending}
      title={`Adopt ${vmsText(vms.length)}`}
      description="Nothing changes in vCenter or EfficientIP: rvav records the VMs, and the addresses and DNS records EfficientIP has for them. Deleting a deployment later deletes its VMs and releases those."
      className="w-[min(640px,100vw)]"
      onSubmit={() => {
        setErrors({});
        const problems = inputProblems(t);
        setLocal(problems);
        if (Object.keys(problems).length === 0) adopt.mutate();
      }}
      submitLabel={adopt.isPending ? "Adopting…" : `Adopt ${vmsText(vms.length)}`}
    >
      <TargetFields
        t={t}
        onChange={(next) => {
          // A row gone: the problems found by row no longer line up.
          if (next.inputs.length < t.inputs.length) setLocal({});
          setT(next);
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
          The VMs
        </h3>
        <ul aria-label="VMs to adopt" className="max-h-48 overflow-y-auto font-mono text-caption">
          {vms.map((v) => (
            <li key={keyOf(v)}>
              {v.name} <span className="text-faint">{v.vcenter}</span>
            </li>
          ))}
        </ul>
      </section>
    </Sheet>
  );
}

function Adopted({ result, onClose }: { result: S["OnboardingResult"]; onClose: () => void }) {
  const vms = result.deployments.reduce((n, d) => n + d.vms.length, 0);
  return (
    <Panel
      title={`Adopted ${vmsText(vms)} into ${result.deployments.length} deployment${result.deployments.length === 1 ? "" : "s"}`}
      description="Each is a deployment like any other: its owner runs its day-2 actions, and its lease runs from when it was made."
      actions={
        <Button size="sm" variant="quiet" onClick={onClose}>
          Close
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
              {d.vms.length > 1 ? `${d.vms.length} VMs · ` : ""}
              {d.addresses} address{d.addresses === 1 ? "" : "es"}, {d.records} DNS record{d.records === 1 ? "" : "s"}
            </span>
          </li>
        ))}
      </ul>
    </Panel>
  );
}

const columns: Column<Vm>[] = [
  {
    id: "name",
    header: "Name",
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
    header: "Address",
    cell: (v) => (
      <span className="font-mono text-secondary">
        {v.ip_address ?? <span className="text-faint">none</span>}
        {v.hostname && <div className="text-caption text-faint">{v.hostname}</div>}
      </span>
    ),
  },
  {
    id: "size",
    header: "Size",
    cell: (v) => (
      <span className="tnum">
        {v.cpu_count} vCPU · {memory(v.memory_mb)}
      </span>
    ),
  },
  { id: "os", header: "Guest OS", cell: (v) => <span className="text-muted">{v.guest_os ?? ""}</span> },
  {
    id: "where",
    header: "Where",
    cell: (v) => (
      <span className="text-secondary text-muted">
        {v.cluster ?? ""}
        <div className="text-caption text-faint">{v.folder || v.vcenter}</div>
      </span>
    ),
  },
];

function VmsTab() {
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
          aria-label="Selected VMs"
        >
          <span className="font-medium">{vmsText(selected.size)} selected</span>
          {selected.size > MAX_ADOPT && (
            <span className="text-secondary text-muted">At most {MAX_ADOPT} at once: select fewer to adopt them.</span>
          )}
          <span className="flex-1" />
          <Button size="sm" variant="primary" disabled={selected.size > MAX_ADOPT} onClick={() => setAdopting(true)}>
            Adopt {vmsText(selected.size)}…
          </Button>
          <Button size="sm" variant="quiet" onClick={() => setSelected(new Map())}>
            Clear selection
          </Button>
        </div>
      )}
      <Panel
        flush
        footer={
          list.hasNextPage && (
            <Button size="sm" disabled={list.isFetchingNextPage} onClick={() => void list.fetchNextPage()}>
              {list.isFetchingNextPage ? "Loading…" : `Show more (${vms?.length ?? 0} of ${total})`}
            </Button>
          )
        }
      >
        <DataTable
          label="VMs to adopt"
          columns={columns}
          rows={vms}
          rowKey={keyOf}
          minWidth={760}
          loading={list.isPending}
          error={list.isError && <ErrorState error={list.error} onRetry={() => void list.refetch()} />}
          selection={{
            selected: new Set(selected.keys()),
            onChange: (next) => setSelected(new Map([...next].map((k) => [k, selected.get(k) ?? shown.get(k)!]))),
            label: (v) => `Select ${v.name}`,
            allLabel: "Select all shown",
          }}
          toolbar={
            <div className="flex w-full flex-wrap items-end gap-2">
              <Field label="Search" htmlFor="ob-search" className="w-56">
                <Input
                  id="ob-search"
                  value={search}
                  onChange={(e) => setSearch(e.target.value)}
                  placeholder="Name, address or host name"
                />
              </Field>
              <Field label="Folder" htmlFor="ob-folder" className="w-48">
                <Input
                  id="ob-folder"
                  className="font-mono"
                  value={folder}
                  onChange={(e) => setFolder(e.target.value)}
                  placeholder="/GVA/vm/legacy"
                />
              </Field>
              <Field label="vCenter" htmlFor="ob-vcenter" className="w-48">
                <Select
                  id="ob-vcenter"
                  value={vcenter}
                  onChange={setVcenter}
                  options={[
                    { value: "any", label: "Any" },
                    ...(vcenters.data?.items ?? []).map((v) => ({ value: v.name, label: v.name })),
                  ]}
                />
              </Field>
              <Field label="Power" htmlFor="ob-power" className="w-32">
                <Select
                  id="ob-power"
                  value={power}
                  onChange={setPower}
                  options={[
                    { value: "any", label: "Any" },
                    { value: "on", label: "On" },
                    { value: "off", label: "Off" },
                    { value: "suspended", label: "Suspended" },
                  ]}
                />
              </Field>
              <span className="ml-auto self-center text-secondary text-muted" aria-live="polite">
                {list.data ? vmsText(total) : ""}
                {selected.size > 0 ? ` · ${selected.size} selected` : ""}
                {hidden > 0 ? `, ${hidden} not shown` : ""}
                {selected.size > MAX_ADOPT ? ` · at most ${MAX_ADOPT} at once` : ""}
              </span>
            </div>
          }
          empty={
            <EmptyState
              icon={ServerCog}
              title="Nothing to adopt"
              body={
                filtered
                  ? "No VM rvav doesn't manage matches these filters."
                  : "Every VM the inventory has is managed by rvav."
              }
              action={
                filtered && (
                  <Button size="sm" onClick={clearFilters}>
                    Clear filters
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
            toast.success(`Adopted ${vmsText(r.deployments.reduce((n, d) => n + d.vms.length, 0))}`);
          }}
        />
      )}
    </div>
  );
}

export function OnboardingPage({ tab = "vms" }: { tab?: OnboardingTab }) {
  const navigate = useNavigate();
  const plans = useQuery(adm.onboardingPlans());
  const [editing, setEditing] = useState<Plan | "new" | null>(null);
  return (
    <Page>
      <PageHeader
        title="Onboarding"
        description="VMs in vCenter that rvav doesn't manage. Adopt them into deployments with an owner, a project and a lease, or save a plan that adopts those its filter matches, as they appear."
        actions={
          tab === "plans" &&
          Boolean(plans.data?.items.length) && (
            <Button variant="primary" onClick={() => setEditing("new")}>
              New plan
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
        <TabsList aria-label="Onboarding sections">
          <TabsTrigger value="vms">VMs to adopt</TabsTrigger>
          <TabsTrigger value="plans">Plans</TabsTrigger>
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
