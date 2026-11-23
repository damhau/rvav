// API tokens (ADR-0020): one's own, made, seen and revoked here, the value
// shown once; administrators see everyone's.
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { useState } from "react";
import { toast } from "sonner";
import { useTranslation } from "react-i18next";
import { ErrorState } from "@/components/common/feedback";
import { Page } from "@/components/common/page";
import { EmptyState } from "@/components/ui/empty-state";
import { PageHeader } from "@/components/ui/page-header";
import { Panel } from "@/components/ui/panel";
import { DataTable, type Column } from "@/components/ui/data-table";
import { Button } from "@/components/ui/button";
import { Checkbox } from "@/components/ui/checkbox";
import { ConfirmDialog } from "@/components/ui/confirm-dialog";
import { Field, Input } from "@/components/ui/input";
import { Select } from "@/components/ui/select";
import { api, call, isApiError } from "@/lib/api/client";
import type { components } from "@/lib/api/schema";
import { can } from "@/lib/can";
import { useCurrentProject } from "@/lib/project";
import { q } from "@/lib/query";
import { copyText } from "@/lib/utils";
import { relative } from "@/lib/format";

const durations = [30, 90, 180];

type ApiToken = components["schemas"]["ApiToken"];

/** A time to sort by, none for a token never used or one that never expires. */
const when = (iso?: string | null) => (iso ? Date.parse(iso) : null);

function NewToken({ onMade }: { onMade: (token: string) => void }) {
  const { t } = useTranslation("account");
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
    onSuccess: (made) => {
      void qc.invalidateQueries({ queryKey: ["tokens"] });
      setName("");
      onMade(made.token);
      // The value lives on the page only, not in the query cache.
      make.reset();
    },
    onError: (e) => toast.error(isApiError(e) ? (e.fieldErrors[0]?.message ?? e.problem.title) : t("tokens.notMade")),
  });
  return (
    <form
      className="flex flex-wrap items-end gap-3"
      onSubmit={(e) => {
        e.preventDefault();
        make.mutate();
      }}
    >
      <Field label={t("tokens.forWhat")} htmlFor="token-name" className="w-64">
        <Input id="token-name" value={name} onChange={(e) => setName(e.target.value)} maxLength={80} />
      </Field>
      <Field label={t("tokens.expiresIn")} htmlFor="token-days" className="w-36">
        <Select
          id="token-days"
          value={days}
          onChange={setDays}
          options={[
            ...durations.map((d) => ({ value: String(d), label: t("tokens.days", { count: d }) })),
            { value: "365", label: t("tokens.year") },
          ]}
        />
      </Field>
      <label htmlFor="token-inventory" className="flex items-center gap-2 pb-2 text-body">
        <Checkbox id="token-inventory" checked={inventoryOnly} onChange={setInventoryOnly} />
        {t("tokens.inventoryOnly")}
      </label>
      <Button type="submit" variant="primary" disabled={!name.trim() || make.isPending}>
        {t(make.isPending ? "tokens.making" : "tokens.make")}
      </Button>
    </form>
  );
}

export function TokensPage() {
  const { t } = useTranslation(["account", "shell"]);
  const qc = useQueryClient();
  const { me } = useCurrentProject();
  const isAdmin = can(me, "tokens.manage");
  const [everyone, setEveryone] = useState(false);
  const [made, setMade] = useState<string | null>(null);
  const [revoking, setRevoking] = useState<{ id: string; name: string } | null>(null);
  const tokens = useQuery(q.tokens(everyone));
  const revoke = useMutation({
    mutationFn: (id: string) => call(api.DELETE("/tokens/{id}", { params: { path: { id } } })),
    onSuccess: () => {
      void qc.invalidateQueries({ queryKey: ["tokens"] });
      toast.success(t("tokens.revoked"));
      setRevoking(null);
    },
    onError: (e) => toast.error(isApiError(e) ? e.problem.title : t("tokens.notRevoked")),
  });
  const all: Column<ApiToken>[] = [
    {
      id: "token",
      header: t("tokens.token"),
      sortValue: (token) => token.name,
      cell: (token) => (
        <>
          <div className="font-medium">{token.name}</div>
          <div className="text-caption text-faint">
            {t(token.scope === "inventory" ? "tokens.inventoryOnly" : "tokens.everything")} ·{" "}
            {t("tokens.made", { when: relative(token.created_at) })}
          </div>
        </>
      ),
    },
    {
      id: "owner",
      header: t("tokens.owner"),
      sortValue: (token) => token.user.name,
      className: "text-secondary",
      cell: (token) => token.user.name,
    },
    {
      id: "lastUsed",
      header: t("tokens.lastUsed"),
      sortValue: (token) => when(token.last_used_at),
      className: "text-secondary text-muted",
      cell: (token) => (token.last_used_at ? relative(token.last_used_at) : t("tokens.never")),
    },
    {
      id: "expires",
      header: t("tokens.expires"),
      sortValue: (token) => when(token.expires_at),
      className: "text-secondary text-muted",
      cell: (token) => (token.expires_at ? relative(token.expires_at) : t("tokens.never")),
    },
    {
      id: "revoke",
      header: t("tokens.actions"),
      hideHeader: true,
      align: "end",
      cell: (token) => (
        <Button
          size="sm"
          variant="quiet"
          aria-label={t("tokens.revokeName", { name: token.name })}
          onClick={() => setRevoking({ id: token.id, name: token.name })}
        >
          {t("tokens.revoke")}
        </Button>
      ),
    },
  ];
  const columns = all.filter((c) => everyone || c.id !== "owner");
  return (
    <Page>
      <PageHeader title={t("shell:topBar.apiTokens")} description={t("tokens.lead")} />
      <Panel title={t("tokens.new")}>
        <NewToken onMade={setMade} />
        {made && (
          <section
            aria-label={t("tokens.yourNew")}
            className="mt-3 flex flex-col gap-1.5 rounded-md border border-ok bg-ok-soft p-3 text-secondary"
          >
            {/* Announced without the value itself. */}
            <span role="status">{t("tokens.copyNow")}</span>
            <div className="flex items-center gap-2">
              <code className="min-w-0 flex-1 rounded bg-surface px-2 py-1 font-mono break-all">{made}</code>
              <Button
                size="sm"
                onClick={() =>
                  copyText(made).then(
                    () => toast.success(t("tokens.copied")),
                    () => toast.error(t("tokens.notCopied"), { description: t("tokens.notCopiedBody") }),
                  )
                }
              >
                {t("tokens.copy")}
              </Button>
              <Button size="sm" variant="quiet" onClick={() => setMade(null)}>
                {t("tokens.done")}
              </Button>
            </div>
          </section>
        )}
      </Panel>
      <Panel
        title={t(everyone ? "tokens.everyones" : "tokens.yours")}
        flush
        actions={
          isAdmin && (
            <label htmlFor="tokens-everyone" className="flex items-center gap-2 text-secondary">
              <Checkbox id="tokens-everyone" checked={everyone} onChange={setEveryone} />
              {t("tokens.everyone")}
            </label>
          )
        }
      >
        <DataTable
          label={t(everyone ? "tokens.everyones" : "tokens.yours")}
          columns={columns}
          rows={tokens.data?.items}
          rowKey={(token) => token.id}
          minWidth={640}
          loading={tokens.isPending}
          error={tokens.isError && <ErrorState error={tokens.error} onRetry={() => void tokens.refetch()} />}
          empty={<EmptyState title={t("tokens.none")} body={t("tokens.noneBody")} />}
        />
      </Panel>
      {revoking && (
        <ConfirmDialog
          open
          onOpenChange={(o) => !o && setRevoking(null)}
          title={t("tokens.revokeTitle", { name: revoking.name })}
          description={t("tokens.revokeBody")}
          consequences={t("tokens.revokeConsequences")}
          confirmLabel={t("tokens.revokeConfirm")}
          pending={revoke.isPending}
          onConfirm={() => revoke.mutate(revoking.id)}
        />
      )}
    </Page>
  );
}
