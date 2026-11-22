import { describe, expect, it } from "vitest";
import { buildVariables, celRoots, undeclared } from "./celReads";

describe("celRoots", () => {
  it("gives the variables read, not fields, functions, keywords, literals or a macro's own", () => {
    expect(celRoots("inputs.network")).toEqual(["inputs"]);
    expect(celRoots("dvpg-web")).toEqual(["dvpg", "web"]);
    expect(celRoots('"dvpg-web"')).toEqual([]);
    expect(celRoots("size(inputs.disks) > 2 ? sizes[inputs.size].vcpu : 1e3")).toEqual(["inputs", "sizes"]);
    expect(celRoots("inputs.nets.filter(n, n.ok).map(x, x.name)[0]")).toEqual(["inputs"]);
    expect(celRoots("has(self.addresses) && type(1u) == uint && true != null")).toEqual(["self"]);
    expect(celRoots("project // the project's\n + 'environment'")).toEqual(["project"]);
  });

  it("finds what the server's variables don't have", () => {
    expect(undeclared("inputs.network")).toBeUndefined();
    expect(undeclared("segments.app")).toBeUndefined();
    expect(undeclared("dvpg-web")).toBe("dvpg");
    expect(undeclared("components.db.addresses")).toBe("components");
    expect(undeclared("components.db.addresses", buildVariables)).toBeUndefined();
  });
});
