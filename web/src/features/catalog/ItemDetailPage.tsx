import { useQuery } from "@tanstack/react-query";
import { Link } from "@tanstack/react-router";
import { lazy, Suspense, useMemo } from "react";
import { blueprintGraph } from "@/components/canvas/blueprint";
import type { CanvasGraph, CanvasNode } from "@/components/canvas/graph";
import { ErrorState, Skeleton } from "@/components/common/feedback";
import { Markdown } from "@/components/common/markdown";
import { ItemIcon, itemTypeLabel, Time } from "@/components/common/misc";
import { KeyValues, Page } from "@/components/common/page";
import { Breadcrumbs } from "@/components/ui/breadcrumbs";
import { Button } from "@/components/ui/button";
import { PageHeader } from "@/components/ui/page-header";
import { Panel } from "@/components/ui/panel";
import type { CatalogItem, RequestForm } from "@/lib/api/types";
import { can } from "@/lib/can";
import { useCurrentProject } from "@/lib/project";
import { q } from "@/lib/query";
import { roughDuration } from "@/lib/utils";
import { approvalText, versionLabel } from "./facts";
import { FavouriteButton } from "./favourite";

// React Flow loads only for an item with more to draw than one VM on a network.
const Canvas = lazy(() => import("@/components/canvas/Canvas").then((m) => ({ default: m.Canvas })));

const listed: CanvasNode["kind"][] = ["component", "step", "network", "segment", "security_group", "nsx_group"];

/** What an item builds, read from its topology: each component or step, with its kind and details. */
function BuildsList({ graph }: { graph: CanvasGraph }) {
  const nodes = graph.nodes.filter((n) => listed.includes(n.kind));
  return (
    <ul className="flex flex-col divide-y divide-line">
      {nodes.map((n) => (
        <li key={n.id} className="flex flex-col gap-1 py-2.5 first:pt-0 last:pb-0">
          <div className="flex flex-wrap items-baseline gap-x-2 gap-y-0.5">
            {n.badge && (
              <span className="rounded-sm bg-sunken px-1.5 py-px text-badge font-medium text-muted">{n.badge}</span>
            )}
            <span className="font-medium">{n.title}</span>
            {n.subtitle && <span className="font-mono text-secondary text-muted">{n.subtitle}</span>}
          </div>
          {n.lines.some((l) => l.label) && (
            <KeyValues
              className="text-secondary"
              items={n.lines
                .filter((l) => l.label)
                .map((l) => ({
                  label: l.label!,
                  value: l.mono ? <span className="font-mono">{l.text}</span> : l.text,
                }))}
            />
          )}
          {n.lines
            .filter((l) => !l.label)
            .map((l) => (
              <p key={l.text} className="text-secondary text-muted">
                {l.text}
              </p>
            ))}
        </li>
      ))}
    </ul>
  );
}

function WhatItBuilds({ item, form }: { item: CatalogItem; form: RequestForm | undefined }) {
  const graph = useMemo(() => (form?.topology ? blueprintGraph(form.topology, "/topology") : null), [form?.topology]);
  if (!form) return null;
  if (!graph) {
    const awx = item.source.awx_template_name;
    return (
      <Panel title="What it does">
        <p className="text-body">
          {awx
            ? `Launches the AWX ${item.type === "awx_workflow" ? "workflow" : "job template"} ${awx} with what you fill in.`
            : "Runs its steps with what you fill in. It builds no VMs of its own."}
        </p>
      </Panel>
    );
  }
  const composite = graph.kind === "composite";
  const drawn = composite || graph.nodes.length > 2;
  return (
    <Panel title={composite ? "Steps, in the order they run" : "What it builds"}>
      {drawn ? (
        <Suspense fallback={<Skeleton className="h-72 rounded-md" />}>
          <Canvas graph={graph} label={`${composite ? "Steps" : "Topology"} of ${item.name}`} />
        </Suspense>
      ) : (
        <BuildsList graph={graph} />
      )}
    </Panel>
  );
}

function Details({ item, category }: { item: CatalogItem; category?: string }) {
  const duration = roughDuration(item.typical_duration_seconds);
  const earlier = (item.versions ?? []).filter((v) => v.id !== item.version.id).map((v) => v.number);
  const rows: { label: string; value: React.ReactNode }[] = [
    { label: "Kind", value: itemTypeLabel[item.type] },
    ...(category ? [{ label: "Category", value: category }] : []),
    {
      label: "Version",
      value: (
        <span className="flex flex-col">
          <span>
            <span className="font-mono">{versionLabel(item)}</span>
            {item.version.published_at && (
              <>
                {" "}
                · published <Time iso={item.version.published_at} />
              </>
            )}
          </span>
          <span className="text-secondary text-muted">
            {earlier.length
              ? `The newest. Requests use it unless you pick ${earlier.join(" or ")}, still offered.`
              : "Requests use the published version."}
          </span>
        </span>
      ),
    },
    { label: "Approval", value: approvalText[item.approval] },
    ...(duration ? [{ label: "Usually takes", value: duration }] : []),
    ...(item.source.awx_template_name
      ? [{ label: "AWX template", value: <span className="font-mono">{item.source.awx_template_name}</span> }]
      : []),
    ...(item.tags?.length ? [{ label: "Tags", value: item.tags.join(", ") }] : []),
    ...(item.requests_last_30_days
      ? [{ label: "Requested", value: `${item.requests_last_30_days} times in the last 30 days` }]
      : []),
  ];
  return (
    <Panel title="Details">
      <KeyValues items={rows} />
    </Panel>
  );
}

export function ItemDetailPage({ itemKey }: { itemKey: string }) {
  const { me, projectId, project } = useCurrentProject();
  const item = useQuery(q.catalogItem(itemKey, projectId));
  const mayRequest = can(me, "request.submit", projectId);
  const form = useQuery({ ...q.form(itemKey, projectId), enabled: mayRequest });
  const categories = useQuery(q.categories(projectId));
  const category = categories.data?.items.find((c) => c.key === item.data?.category)?.name;

  if (item.isError)
    return (
      <Page>
        <PageHeader
          title="Catalog item"
          breadcrumbs={
            <Breadcrumbs>
              <Link to="/catalog">Catalog</Link>
            </Breadcrumbs>
          }
        />
        <ErrorState
          error={item.error}
          onRetry={() => void item.refetch()}
          className="rounded-lg border border-line bg-surface"
        />
      </Page>
    );

  const it = item.data;
  return (
    <Page>
      <PageHeader
        breadcrumbs={
          <Breadcrumbs>
            <Link to="/catalog">Catalog</Link>
          </Breadcrumbs>
        }
        title={
          it ? (
            <span className="flex items-center gap-3">
              <ItemIcon name={it.icon} type={it.type} className="size-9" />
              {it.name}
            </span>
          ) : (
            <Skeleton className="h-6 w-56" />
          )
        }
        description={it?.summary}
        actions={
          it && (
            <>
              <FavouriteButton item={it} />
              {mayRequest ? (
                <Button asChild variant="primary">
                  <Link to="/catalog/$itemId/request" params={{ itemId: it.slug }}>
                    Request
                  </Link>
                </Button>
              ) : (
                <span className="flex flex-col items-end gap-0.5">
                  <Button variant="primary" disabled aria-describedby="request-why">
                    Request
                  </Button>
                  <span id="request-why" className="text-caption text-muted">
                    Your roles in {project.name} don't let you request
                  </span>
                </span>
              )}
            </>
          )
        }
      />
      {!it ? (
        <div className="grid gap-4 lg:grid-cols-[minmax(0,1fr)_340px]">
          <Skeleton className="h-64 rounded-lg" />
          <Skeleton className="h-48 rounded-lg" />
        </div>
      ) : (
        <div className="grid items-start gap-4 lg:grid-cols-[minmax(0,1fr)_340px]">
          <div className="flex min-w-0 flex-col gap-4">
            {it.description_md?.trim() && (
              <Panel title="About this item">
                <Markdown>{it.description_md}</Markdown>
              </Panel>
            )}
            {form.isPending && mayRequest ? (
              <Skeleton className="h-48 rounded-lg" />
            ) : form.isError ? (
              <ErrorState
                error={form.error}
                onRetry={() => void form.refetch()}
                className="rounded-lg border border-line bg-surface"
              />
            ) : (
              <WhatItBuilds item={it} form={form.data} />
            )}
          </div>
          <Details item={it} category={category} />
        </div>
      )}
    </Page>
  );
}
