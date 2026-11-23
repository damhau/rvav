import { useQuery } from "@tanstack/react-query";
import { useMemo } from "react";
import { q } from "./query";

/**
 * rvav's environments by key, from `GET /environments`: a name for each
 * key (the key while they load, or for one no longer kept) and whether it
 * is production.
 */
export function useEnvironments() {
  const envs = useQuery(q.environments());
  return useMemo(() => {
    const byKey = new Map((envs.data?.items ?? []).map((e) => [e.key, e]));
    return {
      name: (key: string) => byKey.get(key)?.name ?? key,
      production: (key: string) => byKey.get(key)?.production ?? false,
    };
  }, [envs.data]);
}

/** An environment's name from its key. */
export const useEnvName = () => useEnvironments().name;
