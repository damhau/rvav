import { render, screen, within } from "@testing-library/react";
import { describe, expect, it } from "vitest";
import type { PreviewComponent } from "@/lib/api/types";

import { ComponentsPreview } from "./ComponentsPreview";

const db: PreviewComponent = {
  key: "db",
  title: "Database",
  count: 1,
  names: ["web-prd-shopdb01"],
  vcpu: 8,
  memory_gb: 32,
  storage_gb: 120,
  disks: [{ size_gb: 120 }],
  nics: [{ key: "nic1", network: "dvpg-app-prd-vl220", ipam: "efficientip" }],
  anti_affinity: false,
  placement: {
    cluster: "GVA-PRD-CL01",
    datastore: "nfs-gva-silver-02",
    chosen: true,
    constraints: [
      {
        kind: "compute",
        tag: "rvav-env/prod",
        negated: false,
        soft: false,
        met: true,
        object: "GVA-PRD-CL01",
        excluded: ["GVA-DEV-CL01"],
      },
      {
        kind: "compute",
        tag: "rvav-zone/pci",
        negated: true,
        soft: false,
        met: true,
        object: "GVA-PRD-CL01",
        excluded: ["GVA-PRD-CL02"],
      },
      {
        kind: "storage",
        tag: "rvav-tier/platinum",
        negated: false,
        soft: true,
        met: false,
        object: "nfs-gva-silver-02",
        excluded: [],
      },
    ],
  },
};

describe("ComponentsPreview", () => {
  it("says which constraint chose or ruled out what", () => {
    render(<ComponentsPreview components={[db, { ...db, key: "web", title: "Web servers", placement: {} }]} />);
    const lines = within(screen.getByRole("listitem", { name: "Database" })).getByRole("list", {
      name: "Placed by tags",
    });
    expect(
      within(lines)
        .getAllByRole("listitem")
        .map((li) => li.textContent),
    ).toEqual([
      "rvav-env/prod: cluster GVA-PRD-CL01, ruled out GVA-DEV-CL01",
      "not rvav-zone/pci: cluster GVA-PRD-CL01, ruled out GVA-PRD-CL02",
      "rvav-tier/platinum (preferred): let go, datastore nfs-gva-silver-02 doesn't meet it",
    ]);
    expect(
      within(screen.getByRole("listitem", { name: "Web servers" })).queryByRole("list", { name: "Placed by tags" }),
    ).toBeNull();
  });
});
