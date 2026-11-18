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

  it("says the resource pool, datastore cluster and strategy that placed a component (ADR-0036)", () => {
    const placed: PreviewComponent = {
      ...db,
      placement: {
        cluster: "GVA-PRD-CL02",
        resource_pool: "rvav/prd",
        hosts: ["esx-gva-prd2-02.corp.example"],
        datastore: "nfs-gva-dev-sdrs-01",
        datastore_cluster: "dsc-gva-dev-01",
        strategy: "binpack",
        chosen: true,
      },
    };
    render(<ComponentsPreview components={[placed]} />);
    expect(screen.getByRole("listitem", { name: "Database" })).toHaveTextContent(
      "GVA-PRD-CL02/rvav/prd · hosts esx-gva-prd2-02.corp.example · nfs-gva-dev-sdrs-01 in dsc-gva-dev-01 (chosen by rvav, binpack)",
    );
  });

  it("says the vCenters its catalogue template has no template in (ADR-0037)", () => {
    const image = { key: "rhel9", label: "RHEL 9.4", excluded: ["vc02.corp.example"] };
    render(<ComponentsPreview components={[{ ...db, placement: { cluster: "GVA-PRD-CL01", image } }]} />);
    const lines = within(screen.getByRole("listitem", { name: "Database" })).getByRole("list", {
      name: "Placed by tags",
    });
    expect(lines).toHaveTextContent("RHEL 9.4: no template in vCenter vc02.corp.example, ruled out");
  });
});
