// Several vCenters (S12.6, ADR-0024): the vCenter integration's first, then
// those administrators add, each with its account (the password sealed),
// synced and placed like the first.
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { CircleCheck, CircleX, Server } from "lucide-react";
import { useId, useState } from "react";
import { toast } from "sonner";
import { ErrorState } from "@/components/common/feedback";
import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import { Checkbox } from "@/components/ui/checkbox";
import { ConfirmDialog } from "@/components/ui/confirm-dialog";
import { DataTable, type Column } from "@/components/ui/data-table";
import { MenuItem, MenuSeparator } from "@/components/ui/dropdown-menu";
import { EmptyState } from "@/components/ui/empty-state";
import { Field, Input } from "@/components/ui/input";
import { Panel } from "@/components/ui/panel";
import { Sheet } from "@/components/ui/sheet";
import { api, call } from "@/lib/api/client";
import type { components } from "@/lib/api/schema";
import { adm } from "@/lib/query";
import { fullDate, relative } from "@/lib/utils";
import { failed, fieldErrors } from "./governance/common";
import { ServerCertificate } from "./ServerCertificate";

type S = components["schemas"];
type VCenter = S["VCenter"];

function TestResult({ test }: { test: S["IntegrationTest"] }) {
  return (
    <ul
      aria-label="Test"
      aria-live="polite"
      className="flex flex-col gap-0.5 rounded-md border border-line p-3 text-secondary break-words"
    >
      {test.checks.map((c) => (
        <li key={c.name} className="flex items-start gap-1.5">
          {c.ok ? (
            <CircleCheck className="mt-px size-3.5 shrink-0 text-ok" aria-label="Passed" />
          ) : (
            <CircleX className="mt-px size-3.5 shrink-0 text-bad" aria-label="Failed" />
          )}
          <span>
            <span className="font-medium">{c.name}:</span> {c.message}
          </span>
        </li>
      ))}
    </ul>
  );
}

function VCenterSheet({ vc, onClose }: { vc?: VCenter; onClose: () => void }) {
  const qc = useQueryClient();
  const formId = useId();
  const [f, setFNow] = useState({
    name: vc?.name ?? "",
    url: vc?.url ?? "",
    username: vc?.username ?? "",
    insecure: vc?.insecure ?? false,
    cert_sha256: vc?.cert_sha256 ?? "",
  });
  const [password, setPasswordNow] = useState("");
  const [errors, setErrors] = useState<Record<string, string>>({});
  const body = (): S["VCenterWrite"] => ({ ...f, ...(password ? { password } : {}) });
  const save = useMutation({
    mutationFn: () =>
      vc
        ? call(api.PUT("/admin/vcenters/{name}", { params: { path: { name: vc.name } }, body: body() }))
        : call(api.POST("/admin/vcenters", { body: body() })),
    onSuccess: () => {
      void qc.invalidateQueries({ queryKey: ["admin", "vcenters"] });
      toast.success(`${f.name} saved`);
      onClose();
    },
    onError: (e) => {
      setErrors(fieldErrors(e));
      failed(e, "Not saved");
    },
  });
  const test = useMutation({
    mutationFn: () =>
      call(api.POST("/admin/vcenters/{name}/test", { params: { path: { name: f.name || "new" } }, body: body() })),
    onMutate: () => setErrors({}),
    onSuccess: () => setErrors({}),
    onError: (e) => {
      setErrors(fieldErrors(e));
      failed(e, "The test didn't run");
    },
  });
  // A test result describes the settings it ran with: changed, it goes.
  const setF = (next: typeof f) => {
    setFNow(next);
    test.reset();
  };
  const setPassword = (next: string) => {
    setPasswordNow(next);
    test.reset();
  };
  return (
    <Sheet
      open
      onOpenChange={(o) => !o && onClose()}
      pending={save.isPending}
      title={vc ? `Change ${vc.name}` : "Add a vCenter"}
      description="rvav syncs its inventory and builds VMs in it with this account."
      footer={
        <>
          <Button type="button" variant="secondary" onClick={onClose} disabled={save.isPending}>
            Cancel
          </Button>
          <Button type="button" disabled={test.isPending} onClick={() => test.mutate()}>
            {test.isPending ? "Testing…" : "Test connection"}
          </Button>
          <Button type="submit" form={formId} variant="primary" disabled={save.isPending}>
            {save.isPending ? "Saving…" : "Save vCenter"}
          </Button>
        </>
      }
    >
      <form
        id={formId}
        className="flex flex-col gap-4"
        onSubmit={(e) => {
          e.preventDefault();
          setErrors({});
          save.mutate();
        }}
      >
        <Field
          label="Name"
          htmlFor="vc-name"
          hint={vc ? "Fixed once added" : "How rvav calls it: placement rules and VMs name it"}
          error={errors["/name"]}
        >
          <Input
            id="vc-name"
            className="font-mono text-secondary"
            disabled={Boolean(vc)}
            placeholder="vc02.corp.example"
            value={f.name}
            onChange={(e) => setF({ ...f, name: e.target.value })}
          />
        </Field>
        <Field label="SDK address" htmlFor="vc-url" error={errors["/url"]}>
          <Input
            id="vc-url"
            className="font-mono text-secondary"
            placeholder="https://vc02.corp.example/sdk"
            value={f.url}
            onChange={(e) => setF({ ...f, url: e.target.value })}
          />
        </Field>
        <div className="grid grid-cols-1 gap-3 sm:grid-cols-2">
          <Field label="Account" htmlFor="vc-user" error={errors["/username"]}>
            <Input id="vc-user" value={f.username} onChange={(e) => setF({ ...f, username: e.target.value })} />
          </Field>
          <Field
            label="Password"
            htmlFor="vc-password"
            error={errors["/password"]}
            hint={vc?.password_set ? "Set; type a new one to replace it" : undefined}
          >
            <Input
              id="vc-password"
              type="password"
              autoComplete="new-password"
              placeholder={vc?.password_set ? "••••••••" : undefined}
              value={password}
              onChange={(e) => setPassword(e.target.value)}
            />
          </Field>
        </div>
        <Field
          label="Trusted certificate (SHA-256)"
          htmlFor="vc-pin"
          error={errors["/cert_sha256"]}
          hint="A self-signed one, trusted besides the CAs: Trust this certificate, after a test, sets it"
        >
          <Input
            id="vc-pin"
            className="font-mono text-secondary"
            value={f.cert_sha256}
            onChange={(e) => setF({ ...f, cert_sha256: e.target.value })}
          />
        </Field>
        <label htmlFor="vc-insecure" className="flex items-center gap-1.5 text-body">
          <Checkbox id="vc-insecure" checked={f.insecure} onChange={(on) => setF({ ...f, insecure: on })} />
          Don't check its certificate
        </label>
        {test.data && <TestResult test={test.data} />}
        {test.data?.certificate && (
          <ServerCertificate
            cert={test.data.certificate}
            onTrust={(sha256) => {
              // The settings change: test again with them.
              setF({ ...f, cert_sha256: sha256, insecure: false });
              toast.info("Its SHA-256 is in the form", { description: "Test again, then save the vCenter." });
            }}
          />
        )}
      </form>
    </Sheet>
  );
}

const columns: Column<VCenter>[] = [
  {
    id: "name",
    header: "Name",
    sortValue: (vc) => vc.name,
    cell: (vc) => (
      <>
        <span className="font-mono text-secondary">{vc.name}</span>
        {vc.primary && (
          <Badge tone="neutral" className="ml-2">
            The connection above
          </Badge>
        )}
      </>
    ),
  },
  {
    id: "address",
    header: "Address and account",
    cell: (vc) => (
      <>
        <div className="font-mono text-secondary">{vc.url}</div>
        <div className="text-secondary text-muted">
          {vc.username || "no account"}
          {vc.insecure ? " · certificate not checked" : vc.cert_sha256 ? " · certificate pinned" : ""}
        </div>
      </>
    ),
  },
  {
    id: "inventory",
    header: "Inventory",
    cell: (vc) =>
      vc.inventory ? (
        <span className="text-secondary">
          {vc.inventory.clusters ?? 0} clusters, {vc.inventory.vms ?? 0} VMs
          {vc.inventory.synced_at && (
            <span className="block text-muted" title={fullDate(vc.inventory.synced_at)}>
              Synced {relative(vc.inventory.synced_at)}
            </span>
          )}
        </span>
      ) : (
        <span className="text-secondary text-muted">Not synced yet</span>
      ),
  },
];

export function VCenters() {
  const qc = useQueryClient();
  const list = useQuery(adm.vcenters());
  const [editing, setEditing] = useState<VCenter | "new" | null>(null);
  const [removing, setRemoving] = useState<VCenter | null>(null);
  const remove = useMutation({
    mutationFn: (name: string) => call(api.DELETE("/admin/vcenters/{name}", { params: { path: { name } } })),
    onSuccess: (_, name) => {
      void qc.invalidateQueries({ queryKey: ["admin", "vcenters"] });
      toast.success(`${name} removed`);
    },
    onError: (e) => failed(e, "Not removed"),
  });
  return (
    <Panel
      title="vCenters"
      description="rvav syncs and builds in every vCenter here. A placement rule may name the one its clusters are in; a cluster name in two vCenters needs it."
      flush
      actions={
        <Button size="sm" variant="primary" onClick={() => setEditing("new")}>
          Add a vCenter
        </Button>
      }
    >
      <DataTable
        label="vCenters"
        columns={columns}
        rows={list.data?.items}
        rowKey={(vc) => vc.name}
        minWidth={640}
        loading={list.isPending}
        error={list.isError && <ErrorState error={list.error} onRetry={() => void list.refetch()} />}
        empty={
          <EmptyState
            icon={Server}
            title="No vCenter yet"
            body="Set the vCenter connection above, then add others here."
          />
        }
        rowMenuLabel={(vc) => `Actions for ${vc.name}`}
        rowMenu={(vc) =>
          vc.primary ? null : (
            <>
              <MenuItem onSelect={() => setEditing(vc)}>Change</MenuItem>
              <MenuSeparator />
              <MenuItem danger onSelect={() => setRemoving(vc)}>
                Remove
              </MenuItem>
            </>
          )
        }
      />
      {editing && <VCenterSheet vc={editing === "new" ? undefined : editing} onClose={() => setEditing(null)} />}
      {removing && (
        <ConfirmDialog
          open
          onOpenChange={(o) => !o && setRemoving(null)}
          title={`Stop managing ${removing.name}?`}
          description="rvav stops syncing it and building in it, and forgets its inventory."
          consequences="It is refused while a deployment has VMs there or a placement rule names it. Its VMs stay in vCenter."
          confirmLabel="Stop managing it"
          pending={remove.isPending}
          onConfirm={() => remove.mutate(removing.name, { onSuccess: () => setRemoving(null) })}
        />
      )}
    </Panel>
  );
}
