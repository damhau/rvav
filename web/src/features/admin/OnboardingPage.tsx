// Onboarding (S13.3): administrators adopt VMs rvav didn't build into
// deployments, with an owner, project, environment, catalog item and lease;
// their addresses and DNS records come from EfficientIP. Nothing changes in
// vCenter or EfficientIP.
import { useInfiniteQuery, useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { Link } from "@tanstack/react-router";
import { useState } from "react";
import { toast } from "sonner";
import { ErrorState, SkeletonRows } from "@/components/common/feedback";
import { EmptyState, Page, PageHeader, Panel } from "@/components/common/page";
import { PowerDot } from "@/components/common/status";
import { Button } from "@/components/ui/button";
import { Checkbox } from "@/components/ui/checkbox";
import { Dialog, DialogContent } from "@/components/ui/dialog";
import { Field, Input } from "@/components/ui/input";
import { Select } from "@/components/ui/select";
import { api, call } from "@/lib/api/client";
import type { components } from "@/lib/api/schema";
import { useDebounced } from "@/lib/hooks";
import { adm, q } from "@/lib/query";
import { failed, fieldErrors, td, th } from "./governance/common";

type S = components["schemas"];
type Vm = S["OnboardingVm"];

const keyOf = (v: Vm) => `${v.vcenter}/${v.moref}`;
const memory = (mb: number) => (mb % 1024 === 0 ? `${mb / 1024} GB` : `${mb} MB`);

function AdoptDialog({
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
  const projects = useQuery(adm.projects("", false));
  const project = projects.data?.items.find((p) => p.id === f.project);
  const members = useQuery({ ...adm.members(f.project), enabled: f.project !== "" });
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
    <Dialog open onOpenChange={(o) => !o && onClose()}>
      <DialogContent
        title={`Adopt ${vms.length} VM${vms.length === 1 ? "" : "s"}`}
        className="w-[min(560px,calc(100vw-32px))]"
      >
        <form
          className="flex flex-col gap-3 p-4"
          onSubmit={(e) => {
            e.preventDefault();
            setErrors({});
            adopt.mutate();
          }}
        >
          <p className="text-xs text-muted">
            Nothing changes in vCenter or EfficientIP: rvav records the VMs, and the addresses and DNS records
            EfficientIP has for them. Deleting a deployment later deletes its VMs and releases those.
          </p>
          <div className="grid grid-cols-2 gap-3">
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
                disabled={!project}
                onChange={(v) => setF({ ...f, environment: v })}
                options={(project?.environments ?? []).map((e) => ({ value: e, label: e }))}
              />
            </Field>
            <Field label="Owner" htmlFor="ob-owner" hint="A member of the project" error={errors["/owner_id"]}>
              <Select
                id="ob-owner"
                value={f.owner}
                disabled={!project}
                loading={members.isFetching}
                onChange={(v) => setF({ ...f, owner: v })}
                options={(members.data?.items ?? [])
                  .filter((m) => m.role !== "viewer")
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
                disabled={!project}
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
          <label htmlFor="ob-one" className="flex items-center gap-1.5 text-[13px]">
            <Checkbox id="ob-one" checked={f.one} onChange={(on) => setF({ ...f, one: on })} />
            All in one deployment (else one each, named after its VM)
          </label>
          {f.one && (
            <Field label="Deployment name" htmlFor="ob-name" error={errors["/deployment_name"]}>
              <Input
                id="ob-name"
                className="font-mono text-xs"
                placeholder="erp-prd"
                value={f.name}
                onChange={(e) => setF({ ...f, name: e.target.value })}
              />
            </Field>
          )}
          {vmErrors.length > 0 && (
            <ul role="alert" className="flex flex-col gap-0.5 rounded-md border border-bad/40 bg-bad-soft p-3 text-xs">
              {vmErrors.map((m) => (
                <li key={m}>{m}</li>
              ))}
            </ul>
          )}
          <div className="flex justify-end gap-2">
            <Button type="button" variant="ghost" onClick={onClose}>
              Cancel
            </Button>
            <Button type="submit" variant="primary" disabled={adopt.isPending}>
              {adopt.isPending ? "Adopting…" : `Adopt ${vms.length} VM${vms.length === 1 ? "" : "s"}`}
            </Button>
          </div>
        </form>
      </DialogContent>
    </Dialog>
  );
}

function Adopted({ result, onClose }: { result: S["OnboardingResult"]; onClose: () => void }) {
  const vms = result.deployments.reduce((n, d) => n + d.vms.length, 0);
  return (
    <Panel
      title={`Adopted ${vms} VM${vms === 1 ? "" : "s"} into ${result.deployments.length} deployment${result.deployments.length === 1 ? "" : "s"}`}
      actions={
        <Button size="sm" variant="ghost" onClick={onClose}>
          Close
        </Button>
      }
      flush
    >
      <ul className="max-h-64 overflow-y-auto text-[13px]">
        {result.deployments.map((d) => (
          <li key={d.id} className="flex flex-wrap gap-x-3 border-b border-line px-4 py-1.5 last:border-0">
            <Link to="/deployments/$deploymentId" params={{ deploymentId: d.id }} className="font-medium">
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

export function OnboardingPage() {
  const qc = useQueryClient();
  const [search, setSearch] = useState("");
  const [vcenter, setVcenter] = useState("any");
  const [power, setPower] = useState("any");
  const debounced = useDebounced(search, 250);
  const vcenters = useQuery(adm.vcenters());
  const filters = {
    q: debounced || undefined,
    vcenter: vcenter === "any" ? undefined : vcenter,
    power_state: power === "any" ? undefined : (power as S["VmPowerState"]),
  };
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
  const vms = list.data?.pages.flatMap((p) => p.items) ?? [];
  const total = list.data?.pages[0]?.total ?? 0;
  const allShown = vms.length > 0 && vms.every((v) => selected.has(keyOf(v)));
  const toggle = (v: Vm, on: boolean) => {
    const next = new Map(selected);
    if (on) next.set(keyOf(v), v);
    else next.delete(keyOf(v));
    setSelected(next);
  };
  return (
    <Page>
      <PageHeader
        title="Onboarding"
        meta="VMs in vCenter that rvav doesn't manage. Adopt them into deployments, with an owner, a project and a lease."
        actions={
          <Button variant="primary" size="sm" disabled={selected.size === 0} onClick={() => setAdopting(true)}>
            Adopt {selected.size || ""} VM{selected.size === 1 ? "" : "s"}…
          </Button>
        }
      />
      {result && <Adopted result={result} onClose={() => setResult(null)} />}
      <Panel flush>
        <div className="flex flex-wrap items-end gap-2 border-b border-line px-4 py-3">
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
              ]}
            />
          </Field>
          <span className="ml-auto text-xs text-muted" aria-live="polite">
            {list.data ? `${total} VM${total === 1 ? "" : "s"}` : ""}
            {selected.size > 0 ? ` · ${selected.size} selected` : ""}
          </span>
        </div>
        {list.isPending && <SkeletonRows rows={6} cols={6} />}
        {list.isError && <ErrorState error={list.error} onRetry={() => void list.refetch()} />}
        {list.data && vms.length === 0 && (
          <EmptyState title="Nothing to adopt" body="Every VM the inventory has that matches is managed by rvav." />
        )}
        {vms.length > 0 && (
          <div className="overflow-x-auto">
            <table className="w-full min-w-[760px] border-collapse text-[13px]" aria-label="VMs to adopt">
              <thead>
                <tr className="label text-left">
                  <th scope="col" className={th + " w-8"}>
                    <Checkbox
                      aria-label="Select all shown"
                      checked={allShown}
                      onChange={(on) => {
                        const next = new Map(selected);
                        for (const v of vms) {
                          if (on) next.set(keyOf(v), v);
                          else next.delete(keyOf(v));
                        }
                        setSelected(next);
                      }}
                    />
                  </th>
                  <th scope="col" className={th}>
                    Name
                  </th>
                  <th scope="col" className={th}>
                    Address
                  </th>
                  <th scope="col" className={th}>
                    Size
                  </th>
                  <th scope="col" className={th}>
                    Guest OS
                  </th>
                  <th scope="col" className={th}>
                    Where
                  </th>
                </tr>
              </thead>
              <tbody>
                {vms.map((v) => (
                  <tr key={keyOf(v)} className="hover:bg-sunken">
                    <td className={td}>
                      <Checkbox
                        aria-label={`Select ${v.name}`}
                        checked={selected.has(keyOf(v))}
                        onChange={(on) => toggle(v, on)}
                      />
                    </td>
                    <td className={td}>
                      <span className="flex items-center gap-2">
                        <PowerDot on={v.power_state === "on"} />
                        <span className="flex flex-col">
                          <span className="font-medium">{v.name}</span>
                          <span className="font-mono text-[11px] text-faint">{v.moref}</span>
                        </span>
                      </span>
                    </td>
                    <td className={td + " font-mono text-xs"}>
                      {v.ip_address ?? <span className="text-faint">none</span>}
                      {v.hostname && <div className="text-[11px] text-faint">{v.hostname}</div>}
                    </td>
                    <td className={td + " tnum"}>
                      {v.cpu_count} vCPU · {memory(v.memory_mb)}
                    </td>
                    <td className={td + " text-muted"}>{v.guest_os ?? ""}</td>
                    <td className={td + " text-xs text-muted"}>
                      {v.cluster ?? ""}
                      <div className="text-[11px] text-faint">{v.vcenter}</div>
                    </td>
                  </tr>
                ))}
              </tbody>
            </table>
          </div>
        )}
        {list.hasNextPage && (
          <div className="flex justify-center border-t border-line p-2">
            <Button
              size="sm"
              variant="ghost"
              disabled={list.isFetchingNextPage}
              onClick={() => void list.fetchNextPage()}
            >
              {list.isFetchingNextPage ? "Loading…" : `Show more (${vms.length} of ${total})`}
            </Button>
          </div>
        )}
      </Panel>
      {adopting && (
        <AdoptDialog
          vms={[...selected.values()]}
          onClose={() => setAdopting(false)}
          onDone={(r) => {
            setAdopting(false);
            setSelected(new Map());
            setResult(r);
            void qc.invalidateQueries({ queryKey: ["admin", "onboarding"] });
            void qc.invalidateQueries({ queryKey: ["deployments"] });
            toast.success(`Adopted ${r.deployments.reduce((n, d) => n + d.vms.length, 0)} VMs`);
          }}
        />
      )}
    </Page>
  );
}
