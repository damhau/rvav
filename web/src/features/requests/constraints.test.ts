import { describe, expect, it } from "vitest";
import type { PlacementConstraint } from "@/lib/api/types";

import { constraintText } from "./constraints";

const k: PlacementConstraint = {
  kind: "compute",
  tag: "rvav-env/prod",
  negated: false,
  soft: false,
  met: true,
  excluded: [],
};

describe("constraintText", () => {
  it("says what a hard constraint chose and ruled out", () => {
    expect(constraintText({ ...k, object: "GVA-PRD-CL01", excluded: ["GVA-DEV-CL01"] })).toBe(
      "rvav-env/prod: cluster GVA-PRD-CL01, ruled out GVA-DEV-CL01",
    );
  });

  it("says a negation", () => {
    expect(
      constraintText({ ...k, tag: "rvav-zone/pci", negated: true, object: "GVA-PRD-CL01", excluded: ["GVA-PRD-CL02"] }),
    ).toBe("not rvav-zone/pci: cluster GVA-PRD-CL01, ruled out GVA-PRD-CL02");
  });

  it("says a soft constraint met, and one let go", () => {
    const gold = { ...k, kind: "storage", tag: "rvav-tier/gold", soft: true } as const;
    expect(constraintText({ ...gold, object: "vsan-gva-prd-cl01", excluded: ["nfs-gva-silver-02"] })).toBe(
      "rvav-tier/gold (preferred): datastore vsan-gva-prd-cl01, passed over nfs-gva-silver-02",
    );
    expect(constraintText({ ...gold, met: false, object: "nfs-gva-silver-02" })).toBe(
      "rvav-tier/gold (preferred): let go, datastore nfs-gva-silver-02 doesn't meet it",
    );
  });

  it("says a disk's own, a network's, and one nothing meets", () => {
    expect(constraintText({ ...k, kind: "storage", tag: "rvav-tier/gold", disk: 1, object: "vsan-gva-prd-cl01" })).toBe(
      "rvav-tier/gold: disk 2's datastore vsan-gva-prd-cl01",
    );
    expect(constraintText({ ...k, kind: "network", tag: "pci/yes", met: false, object: "dvpg-web-prd-vl210" })).toBe(
      "pci/yes: network dvpg-web-prd-vl210 doesn't meet it",
    );
    expect(constraintText({ ...k, tag: "rvav-env/test", met: false })).toBe(
      "rvav-env/test: no cluster allowed meets it",
    );
  });
});
