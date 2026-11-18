import { useQuery } from "@tanstack/react-query";
import { useEffect, useRef } from "react";
import { api, call } from "@/lib/api/client";
import type { FieldValuesQuery, UiField, UiWidget } from "@/lib/api/types";
import { keys } from "@/lib/query";

export interface FormContext {
  versionId: string;
  projectId: string;
  environment: string;
  /** The form designer's preview: resolve as this user, any version (ADR-0019). */
  asUserId?: string;
  /** The designer's saved revision of the draft, so a changed source is fetched again. */
  revision?: number;
}

/** The built-in sources these widgets always take their choices from: the server checks them. */
export const ownSources: Partial<Record<UiWidget, string>> = {
  project: "rvav.projects",
  user: "rvav.users",
  deployment: "rvav.deployments",
  vsphere_vm: "rvav.vms",
};

/** The field with the source its widget always has, when it names none (as the server resolves it). */
export function withOwnSource(ui: UiField): UiField {
  const name = ui.value_source ? undefined : ownSources[ui.widget];
  return name ? { ...ui, value_source: { kind: "builtin", name } } : ui;
}

/**
 * Resolve a field's value source on the server. The query key holds the values
 * of the fields it depends on, so a change there fetches fresh options.
 */
export function useFieldValues(
  name: string,
  ui: UiField,
  values: Record<string, unknown>,
  ctx: FormContext,
  search?: { text: string; enabled: boolean },
) {
  const deps = (ui.depends_on ?? []).map((d) => values[d] ?? null);
  // Keep the latest inputs for the request body without putting them in the key.
  const latest = useRef(values);
  latest.current = values;
  // Deliberately keyed on the dependencies only: other inputs are sent but don't trigger a refetch.
  // eslint-disable-next-line @tanstack/query/exhaustive-deps
  return useQuery({
    queryKey: [
      ...keys.fieldValues(ctx.versionId, ctx.projectId, ctx.environment, name, deps),
      ctx.asUserId,
      ctx.revision,
      ...(search ? [search.text] : []),
    ],
    queryFn: ({ signal }) => {
      const body: FieldValuesQuery = {
        version_id: ctx.versionId,
        project_id: ctx.projectId,
        environment: ctx.environment,
        field: name,
        inputs: latest.current,
        ...(ctx.asUserId ? { as_user_id: ctx.asUserId } : {}),
        ...(search ? { search: search.text } : {}),
      };
      return call(api.POST("/forms/values", { body, signal }));
    },
    enabled: Boolean(ui.value_source) && (search?.enabled ?? true),
    staleTime: (query) => (query.state.data?.cache_seconds ?? 30) * 1000,
  });
}

/** Apply a server suggestion (default option or computed value) when the current value doesn't fit. */
export function useApplySuggestion(
  value: unknown,
  suggestion: string | undefined,
  valid: boolean,
  onChange: (v: unknown) => void,
) {
  const onChangeRef = useRef(onChange);
  onChangeRef.current = onChange;
  useEffect(() => {
    if (suggestion !== undefined && !valid && suggestion !== value) onChangeRef.current(suggestion);
  }, [suggestion, valid, value]);
}
