import { useQuery } from "@tanstack/react-query";
import { createContext, useCallback, useContext, useMemo, useState, type ReactNode } from "react";
import type { Me, ProjectRef, ProjectRole } from "./api/types";
import { q } from "./query";

const KEY = "rvav.project";

interface ProjectContext {
  me: Me;
  /** Empty for someone in no project, who only approves. */
  projectId: string;
  project: ProjectRef;
  role: ProjectRole;
  setProjectId: (id: string) => void;
}

const Ctx = createContext<ProjectContext | null>(null);

function stored(): string | null {
  try {
    return globalThis.localStorage?.getItem(KEY) ?? null;
  } catch {
    return null;
  }
}

/** Loads the signed-in user and tracks the project they're working in. */
export function ProjectProvider({
  children,
  fallback,
}: {
  children: ReactNode;
  fallback: (state: "loading" | Error) => ReactNode;
}) {
  const me = useQuery(q.me());
  const [chosen, setChosen] = useState<string | null>(stored);

  const setProjectId = useCallback((id: string) => {
    setChosen(id);
    try {
      globalThis.localStorage?.setItem(KEY, id);
    } catch {
      // Not persisted when storage is blocked.
    }
  }, []);

  const value = useMemo<ProjectContext | null>(() => {
    if (!me.data) return null;
    const membership = me.data.projects.find((p) => p.project.id === chosen) ?? me.data.projects[0];
    if (!membership) {
      // Approvers need not be in a project: the app works, project pages say why they are empty.
      return {
        me: me.data,
        projectId: "",
        project: { id: "", code: "", name: "No project" },
        role: "viewer",
        setProjectId,
      };
    }
    return {
      me: me.data,
      projectId: membership.project.id,
      project: membership.project,
      role: membership.role,
      setProjectId,
    };
  }, [me.data, chosen, setProjectId]);

  if (me.isError) return <>{fallback(me.error)}</>;
  if (!value) return <>{fallback("loading")}</>;
  return <Ctx.Provider value={value}>{children}</Ctx.Provider>;
}

export function useCurrentProject(): ProjectContext {
  const v = useContext(Ctx);
  if (!v) throw new Error("useCurrentProject needs a ProjectProvider");
  return v;
}
