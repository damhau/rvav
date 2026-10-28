// API tokens (ADR-0020): one's own, made, seen and revoked here, the value
// shown once; administrators see everyone's.
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { useState } from "react";
import { toast } from "sonner";
import { ErrorState, SkeletonRows } from "@/components/common/feedback";
import { EmptyState, Page, PageHeader, Panel } from "@/components/common/page";
import { Button } from "@/components/ui/button";
import { Checkbox } from "@/components/ui/checkbox";
import { Field, Input } from "@/components/ui/input";
import { Select } from "@/components/ui/select";
import { api, call, isApiError } from "@/lib/api/client";
import { useCurrentProject } from "@/lib/project";
import { q } from "@/lib/query";
import { relative } from "@/lib/utils";

const durations = [
  { value: "30", label: "30 days" },
  { value: "90", label: "90 days" },
  { value: "180", label: "180 days" },
  { value: "365", label: "A year" },
];

function NewToken({ onMade }: { onMade: (token: string) => void }) {
  const qc = useQueryClient();
  const [name, setName] = useState("");
  const [days, setDays] = useState("90");
  const [inventoryOnly, setInventoryOnly] = useState(false);
  const make = useMutation({
    mutationFn: () =>
      call(
        api.POST("/tokens", {
          body: {
            name,
            scope: inventoryOnly ? "inventory" : "all",
            expires_at: new Date(Date.now() + Number(days) * 86_400_000).toISOString(),
          },
        }),
      ),
    onSuccess: (t) => {
      void qc.invalidateQueries({ queryKey: ["tokens"] });
      setName("");
      onMade(t.token);
    },
    onError: (e) => toast.error(isApiError(e) ? (e.fieldErrors[0]?.message ?? e.problem.title) : "No token was made"),
  });
  return (
    <form
      className="flex flex-wrap items-end gap-3"
      onSubmit={(e) => {
        e.preventDefault();
        make.mutate();
      }}
    >
      <Field label="What it is for" htmlFor="token-name" className="w-64">
        <Input id="token-name" value={name} onChange={(e) => setName(e.target.value)} maxLength={80} />
      </Field>
      <Field label="Expires in" htmlFor="token-days" className="w-36">
        <Select id="token-days" value={days} onChange={setDays} options={durations} />
      </Field>
      <label htmlFor="token-inventory" className="flex items-center gap-2 pb-2 text-[13px]">
        <Checkbox id="token-inventory" checked={inventoryOnly} onChange={setInventoryOnly} />
        Ansible inventory only
      </label>
      <Button type="submit" variant="primary" disabled={!name.trim() || make.isPending}>
        {make.isPending ? "Making…" : "Make a token"}
      </Button>
    </form>
  );
}

export function TokensPage() {
  const qc = useQueryClient();
  const { me } = useCurrentProject();
  const isAdmin = me.global_roles.includes("admin");
  const [everyone, setEveryone] = useState(false);
  const [made, setMade] = useState<string | null>(null);
  const tokens = useQuery(q.tokens(everyone));
  const revoke = useMutation({
    mutationFn: (id: string) => call(api.DELETE("/tokens/{id}", { params: { path: { id } } })),
    onSuccess: () => {
      void qc.invalidateQueries({ queryKey: ["tokens"] });
      toast.success("Token revoked");
    },
    onError: (e) => toast.error(isApiError(e) ? e.problem.title : "Not revoked"),
  });
  const list = tokens.data?.items ?? [];
  return (
    <Page>
      <PageHeader
        title="API tokens"
        meta="Scripts and pipelines call rvav's API with a token, as you. Its value is shown once."
      />
      <Panel title="New token">
        <NewToken onMade={setMade} />
        {made && (
          <div role="status" className="mt-3 flex flex-col gap-1.5 rounded-md border border-ok bg-ok-soft p-3 text-xs">
            <span>Copy it now: it won't be shown again.</span>
            <div className="flex items-center gap-2">
              <code className="min-w-0 flex-1 rounded bg-surface px-2 py-1 font-mono break-all">{made}</code>
              <Button
                size="sm"
                onClick={() => {
                  void navigator.clipboard?.writeText(made);
                  toast.success("Copied");
                }}
              >
                Copy
              </Button>
              <Button size="sm" variant="ghost" onClick={() => setMade(null)}>
                Done
              </Button>
            </div>
          </div>
        )}
      </Panel>
      <Panel
        title={everyone ? "Everyone's tokens" : "Your tokens"}
        flush
        actions={
          isAdmin && (
            <label htmlFor="tokens-everyone" className="flex items-center gap-2 text-xs">
              <Checkbox id="tokens-everyone" checked={everyone} onChange={setEveryone} />
              Everyone's
            </label>
          )
        }
      >
        {tokens.isPending && <SkeletonRows rows={2} cols={5} />}
        {tokens.isError && <ErrorState error={tokens.error} onRetry={() => void tokens.refetch()} />}
        {tokens.data && list.length === 0 && (
          <EmptyState title="No tokens" body="Make one above for a script or a pipeline." />
        )}
        {list.length > 0 && (
          <div className="overflow-x-auto">
            <table className="w-full min-w-[640px] border-collapse text-[13px]">
              <thead>
                <tr className="label text-left">
                  <th scope="col" className="border-b border-line px-4 py-2 font-medium">
                    Token
                  </th>
                  {everyone && (
                    <th scope="col" className="border-b border-line px-3 py-2 font-medium">
                      Owner
                    </th>
                  )}
                  <th scope="col" className="border-b border-line px-3 py-2 font-medium">
                    Last used
                  </th>
                  <th scope="col" className="border-b border-line px-3 py-2 font-medium">
                    Expires
                  </th>
                  <th scope="col" className="border-b border-line px-4 py-2">
                    <span className="sr-only">Actions</span>
                  </th>
                </tr>
              </thead>
              <tbody>
                {list.map((t) => (
                  <tr key={t.id} className="hover:bg-sunken">
                    <td className="border-b border-line px-4 py-2">
                      <div className="font-medium">{t.name}</div>
                      <div className="text-[11px] text-faint">
                        {t.scope === "inventory" ? "Ansible inventory only" : "Everything you may do"} · made{" "}
                        {relative(t.created_at)}
                      </div>
                    </td>
                    {everyone && <td className="border-b border-line px-3 py-2 text-xs">{t.user.name}</td>}
                    <td className="border-b border-line px-3 py-2 text-xs text-muted">
                      {t.last_used_at ? relative(t.last_used_at) : "Never"}
                    </td>
                    <td className="border-b border-line px-3 py-2 text-xs text-muted">
                      {t.expires_at ? relative(t.expires_at) : "Never"}
                    </td>
                    <td className="border-b border-line px-4 py-2 text-right">
                      <Button
                        size="sm"
                        variant="ghost"
                        aria-label={`Revoke ${t.name}`}
                        disabled={revoke.isPending}
                        onClick={() =>
                          window.confirm(`Revoke ${t.name}? What uses it stops working.`) && revoke.mutate(t.id)
                        }
                      >
                        Revoke
                      </Button>
                    </td>
                  </tr>
                ))}
              </tbody>
            </table>
          </div>
        )}
      </Panel>
    </Page>
  );
}
