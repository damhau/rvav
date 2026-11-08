import { describe, expect, it } from "vitest";
import { checkAnswer } from "./approvals";

describe("an answer to a step's form in the mocks", () => {
  const form = [{ key: "why", label: "Why", type: "string" as const }];

  it("takes text of 4096 characters at most, counted as the server counts them", () => {
    for (const c of ["é", "𝄞"]) {
      expect(checkAnswer(form, { why: c.repeat(4096) }).errors).toEqual([]);
      expect(checkAnswer(form, { why: c.repeat(4097) }).errors).toEqual([
        { field: "/answer/why", code: "invalid", message: "At most 4096 characters." },
      ]);
    }
  });
});
