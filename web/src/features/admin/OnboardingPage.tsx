// Onboarding (S13.3): administrators adopt VMs rvav didn't build into
// deployments, with an owner, project, environment, catalog item and lease;
// their addresses and DNS records come from EfficientIP. Nothing changes in
// vCenter or EfficientIP.
import { useInfiniteQuery, useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { Link } from "@tanstack/react-router";
import { ServerCog } from "lucide-react";
import { useState } from "react";
import { toast } from "sonner";
import { ErrorState } from "@/components/common/feedback";
import { Page } from "@/components/common/page";
import { PowerDot } from "@/components/common/status";
import { Button } from "@/components/ui/button";
import { Checkbox } from "@/components/ui/checkbox";
import { DataTable, type Column } from "@/components/ui/data-table";
import { EmptyState } from "@/components/ui/empty-state";
import { Field, Input } from "@/components/ui/input";
import { PageHeader } from "@/components/ui/page-header";
import { Panel } from "@/components/ui/panel";
import { Select } from "@/components/ui/select";
import { Sheet } from "@/components/ui/sheet";
import { api, call } from "@/lib/api/client";
import type { components } from "@/lib/api/schema";
import { useDebounced } from "@/lib/hooks";
import { adm, q } from "@/lib/query";
import { failed, fieldErrors } from "./governance/common";

type S = components["schemas"];
type Vm = S["OnboardingVm"];

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
  const [f, setF] = useState({ project: "", environment: "", owner: "", item: "", lease: "", one: false, name: "" });
  const [errors, setErrors] = useState<Record<string, string>>({});
  const projects = useQuery(adm.projectNames());
  const project = useQuery({ ...q.project(f.project), enabled: f.project !== "" });
  const members = useQuery({ ...adm.members(f.project), enabled: f.project !== "" });
  // Owners run the actions on their deployments: members who may request
  // there. Without the roles to read, every member is offered; the server
  // decides.
  const roles = useQuery(adm.roles()).data?.items;
  const requests = (m: S["ProjectMember"]) =>
    !roles || m.roles.some((r) => roles.find((x) => x.key === r.key)?.permissions.includes("request.submit"));
  const items = useQuery({ ...q.catalog({ projectId: f.project, type: "vm_blueprint" }), enabled: f.project !== "" });
  const adopt = useMutation({
    mutationFn: () =>
      call(
        api.POST("/admin/onboarding", {
          body: {
            vms: vms.map((v) => ({ vcenter: v.vcenter, moref: v.moref })),
            project_id: f.project,
            environment: f.environment,
            owner_id: f.owner,
            catalog_item_id: f.item,
            ...(f.lease ? { lease_days: Number(f.lease) } : {}),
            ...(f.one ? { deployment_name: f.name } : {}),
          },
        }),
      ),
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
      onSubmit={() => {
        setErrors({});
        adopt.mutate();
      }}
      submitLabel={adopt.isPending ? "Adopting…" : `Adopt ${vmsText(vms.length)}`}
    >
      <div className="grid grid-cols-1 gap-3 sm:grid-cols-2">
        <Field label="Project" htmlFor="ob-project" error={errors["/project_id"]}>
          <Select
            id="ob-project"
            value={f.project}
            loading={projects.isPending}
            onChange={(v) => setF({ ...f, project: v, environment: "", owner: "", item: "" })}
            options={(projects.data?.items ?? []).map((p) => ({ value: p.id, label: p.name }))}
          />
        </Field>
        <Field label="Environment" htmlFor="ob-env" error={errors["/environment"]}>
          <Select
            id="ob-env"
            value={f.environment}
            disabled={!f.project}
            onChange={(v) => setF({ ...f, environment: v })}
            loading={project.isFetching}
            options={(project.data?.environments ?? []).map((e) => ({ value: e, label: e }))}
          />
        </Field>
        <Field label="Owner" htmlFor="ob-owner" hint="A member of the project" error={errors["/owner_id"]}>
          <Select
            id="ob-owner"
            value={f.owner}
            disabled={!f.project}
            loading={members.isFetching}
            onChange={(v) => setF({ ...f, owner: v })}
            options={(members.data?.items ?? [])
              .filter(requests)
              .map((m) => ({ value: m.user.id, label: m.user.name }))}
          />
        </Field>
        <Field
          label="Catalog item"
          htmlFor="ob-item"
          hint="Their OS family and day-2 actions"
          error={errors["/catalog_item_id"]}
        >
          <Select
            id="ob-item"
            value={f.item}
            disabled={!f.project}
            loading={items.isFetching}
            onChange={(v) => setF({ ...f, item: v })}
            options={(items.data?.items ?? []).map((i) => ({ value: i.id, label: i.name }))}
          />
        </Field>
        <Field
          label="Lease (days)"
          htmlFor="ob-lease"
          hint="Empty: the lease policy's default"
          error={errors["/lease_days"]}
        >
          <Input
            id="ob-lease"
            type="number"
            min={1}
            value={f.lease}
            onChange={(e) => setF({ ...f, lease: e.target.value })}
          />
        </Field>
      </div>
      <label htmlFor="ob-one" className="flex items-center gap-1.5 text-body">
        <Checkbox id="ob-one" checked={f.one} onChange={(on) => setF({ ...f, one: on })} />
        All in one deployment (else one each, named after its VM)
      </label>
      {f.one && (
        <Field label="Deployment name" htmlFor="ob-name" error={errors["/deployment_name"]}>
          <Input
            id="ob-name"
            className="font-mono text-secondary"
            placeholder="erp-prd"
            value={f.name}
            onChange={(e) => setF({ ...f, name: e.target.value })}
          />
        </Field>
      )}
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
      description="Each is a deployment like any other: its owner runs its day-2 actions, and its lease runs from today."
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
        <div className="text-caption text-faint">{v.vcenter}</div>
      </span>
    ),
  },
];

export function OnboardingPage() {
  const qc = useQueryClient();
  const [search, setSearch] = useState("");
  const [vcenter, setVcenter] = useState("any");
  const [power, setPower] = useState("any");
  const debounced = useDebounced(search, 250);
  const vcenters = useQuery(adm.vcenters());
  // Loaded with the page: the side panel then opens on its Project field,
  // which stays disabled while it loads.
  useQuery(adm.projectNames());
  const filters = {
    q: debounced || undefined,
    vcenter: vcenter === "any" ? undefined : vcenter,
    power_state: power === "any" ? undefined : (power as S["VmPowerState"]),
  };
  const filtered = search !== "" || vcenter !== "any" || power !== "any";
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
  };
  return (
    <Page>
      <PageHeader
        title="Onboarding"
        description="VMs in vCenter that rvav doesn't manage. Select them, then adopt them into deployments with an owner, a project and a lease."
      />
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
            toast.success(`Adopted ${vmsText(r.deployments.reduce((n, d) => n + d.vms.length, 0))}`);
          }}
        />
      )}
    </Page>
  );
}
