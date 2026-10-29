// Several vCenters (S12.6, ADR-0024): the vCenter integration's first, then
// those administrators add, each with its account (the password sealed),
// synced and placed like the first.
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { CircleCheck, CircleX } from "lucide-react";
import { useState } from "react";
import { toast } from "sonner";
import { ErrorState, SkeletonRows } from "@/components/common/feedback";
import { EmptyState, Panel } from "@/components/common/page";
import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import { Checkbox } from "@/components/ui/checkbox";
import { Dialog, DialogContent } from "@/components/ui/dialog";
import { Field, Input } from "@/components/ui/input";
import { api, call } from "@/lib/api/client";
import type { components } from "@/lib/api/schema";
import { adm } from "@/lib/query";
import { failed, fieldErrors } from "./governance/common";

type S = components["schemas"];

function TestResult({ test }: { test: S["IntegrationTest"] }) {
  return (
    <ul
      aria-label="Test"
      aria-live="polite"
      className="flex flex-col gap-0.5 rounded-md border border-line p-3 text-xs"
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

function VCenterDialog({ vc, onClose }: { vc?: S["VCenter"]; onClose: () => void }) {
  const qc = useQueryClient();
  const [f, setFNow] = useState({
    name: vc?.name ?? "",
    url: vc?.url ?? "",
    username: vc?.username ?? "",
    insecure: vc?.insecure ?? false,
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
    <Dialog open onOpenChange={(o) => !o && onClose()}>
      <DialogContent title={vc ? `Change ${vc.name}` : "Add a vCenter"} className="w-[min(560px,calc(100vw-32px))]">
        <form
          className="flex flex-col gap-3 p-4"
          onSubmit={(e) => {
            e.preventDefault();
            setErrors({});
            save.mutate();
          }}
        >
          <Field
            label="Name"
            htmlFor="vc-name"
            hint="How rvav calls it: placement rules and VMs name it"
            error={errors["/name"]}
          >
            <Input
              id="vc-name"
              className="font-mono text-xs"
              disabled={Boolean(vc)}
              placeholder="vc02.corp.example"
              value={f.name}
              onChange={(e) => setF({ ...f, name: e.target.value })}
            />
          </Field>
          <Field label="SDK address" htmlFor="vc-url" error={errors["/url"]}>
            <Input
              id="vc-url"
              className="font-mono text-xs"
              placeholder="https://vc02.corp.example/sdk"
              value={f.url}
              onChange={(e) => setF({ ...f, url: e.target.value })}
            />
          </Field>
          <div className="grid grid-cols-2 gap-3">
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
          <label htmlFor="vc-insecure" className="flex items-center gap-1.5 text-[13px]">
            <Checkbox id="vc-insecure" checked={f.insecure} onChange={(on) => setF({ ...f, insecure: on })} />
            Don't check its certificate
          </label>
          {test.data && <TestResult test={test.data} />}
          <div className="flex justify-end gap-2">
            <Button type="button" variant="ghost" onClick={onClose}>
              Cancel
            </Button>
            <Button type="button" disabled={test.isPending} onClick={() => test.mutate()}>
              {test.isPending ? "Testing…" : "Test"}
            </Button>
            <Button type="submit" variant="primary" disabled={save.isPending}>
              Save vCenter
            </Button>
          </div>
        </form>
      </DialogContent>
    </Dialog>
  );
}

const th = "border-b border-line px-4 py-2 font-medium";
const td = "border-b border-line px-4 py-2 align-top";

export function VCenters() {
  const qc = useQueryClient();
  const list = useQuery(adm.vcenters());
  const [editing, setEditing] = useState<S["VCenter"] | "new" | null>(null);
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
      flush
      actions={
        <Button size="sm" onClick={() => setEditing("new")}>
          Add a vCenter
        </Button>
      }
    >
      <p className="px-4 pt-3 text-xs text-muted">
        rvav syncs and builds in every vCenter here. A placement rule may name the one its clusters are in; a cluster
        name in two vCenters needs it.
      </p>
      {list.isPending && <SkeletonRows rows={2} cols={4} />}
      {list.isError && <ErrorState error={list.error} onRetry={() => void list.refetch()} />}
      {list.data && list.data.items.length === 0 && (
        <EmptyState title="No vCenter yet" body="Set the vCenter connection above, then add others here." />
      )}
      {list.data && list.data.items.length > 0 && (
        <div className="overflow-x-auto">
          <table className="w-full min-w-[620px] border-collapse text-[13px]">
            <thead>
              <tr className="label text-left">
                <th scope="col" className={th}>
                  Name
                </th>
                <th scope="col" className={th}>
                  Address and account
                </th>
                <th scope="col" className={th}>
                  Inventory
                </th>
                <th scope="col" className={`${th} text-right`}>
                  <span className="sr-only">Actions</span>
                </th>
              </tr>
            </thead>
            <tbody>
              {list.data.items.map((vc) => (
                <tr key={vc.name}>
                  <td className={`${td} font-mono text-xs`}>
                    {vc.name} {vc.primary && <Badge tone="neutral">The connection above</Badge>}
                  </td>
                  <td className={td}>
                    <div className="font-mono text-xs">{vc.url}</div>
                    <div className="text-xs text-faint">
                      {vc.username || "no account"}
                      {vc.insecure ? " · certificate not checked" : ""}
                    </div>
                  </td>
                  <td className={`${td} text-xs`}>
                    {vc.inventory
                      ? `${vc.inventory.clusters ?? 0} clusters, ${vc.inventory.vms ?? 0} VMs${
                          vc.inventory.synced_at ? ` · ${new Date(vc.inventory.synced_at).toLocaleString()}` : ""
                        }`
                      : "Not synced yet"}
                  </td>
                  <td className={`${td} text-right whitespace-nowrap`}>
                    {!vc.primary && (
                      <span className="inline-flex gap-1">
                        <Button
                          size="sm"
                          variant="ghost"
                          aria-label={`Change ${vc.name}`}
                          onClick={() => setEditing(vc)}
                        >
                          Change
                        </Button>
                        <Button
                          size="sm"
                          variant="ghost"
                          aria-label={`Remove ${vc.name}`}
                          onClick={() =>
                            window.confirm(`Stop managing ${vc.name}? Its inventory is forgotten.`) &&
                            remove.mutate(vc.name)
                          }
                        >
                          Remove
                        </Button>
                      </span>
                    )}
                  </td>
                </tr>
              ))}
            </tbody>
          </table>
        </div>
      )}
      {editing && <VCenterDialog vc={editing === "new" ? undefined : editing} onClose={() => setEditing(null)} />}
    </Panel>
  );
}
