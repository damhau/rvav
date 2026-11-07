import { describe, expect, it } from "vitest";
import { answerText, dueText } from "./approvals";

describe("a step's wait for a person", () => {
  it("shows a yes or no answer in words, and others as they are", () => {
    expect(answerText(true)).toBe("Yes");
    expect(answerText(false)).toBe("No");
    expect(answerText("delete")).toBe("delete");
    expect(answerText(3)).toBe("3");
  });

  it("says what its timeout does, not what an approval's would", () => {
    const step = { step_key: "manager", escalate_to: null, escalated: false };
    expect(dueText({ ...step, on_timeout: "approve" })).toBe("then it goes on with no answer");
    expect(dueText({ ...step, on_timeout: "reject" })).toBe("then the step fails");
    expect(dueText({ ...step, on_timeout: "escalate", escalated: true })).toBe("then the step fails");
  });
});
