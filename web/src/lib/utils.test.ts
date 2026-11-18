import { describe, expect, it } from "vitest";
import { withUnits } from "./utils";

describe("withUnits", () => {
  it("keeps numbers on the line of their units", () => {
    expect(withUnits("2 vCPU · 4 GB · 60 GB")).toBe("2 vCPU · 4 GB · 60 GB");
  });

  it("leaves other spaces alone", () => {
    expect(withUnits("Medium · 3 × 2")).toBe("Medium · 3 × 2");
  });
});
