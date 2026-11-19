import { renderHook } from "@testing-library/react";
import { describe, expect, it } from "vitest";
import type { UiField } from "@/lib/api/types";
import { useSecretStandIns } from "./secretKeys";

const fields: Record<string, UiField> = {
  password: { widget: "secret", label: "Password" },
  token: { widget: "text", label: "Token", secret: true },
  name: { widget: "text", label: "Name" },
};

describe("useSecretStandIns", () => {
  it("puts a stand-in for each secret value, the same for the same value", () => {
    const { result } = renderHook(() => useSecretStandIns(fields));
    const hide = result.current;
    const first = hide({ password: "hunter2", token: "t0k", name: "web" });
    expect(first).toEqual({ password: "secret #1", token: "secret #2", name: "web" });
    expect(hide({ password: "hunter2", name: "web" })).toEqual({ password: "secret #1", name: "web" });
    expect(hide({ password: "hunter3" }).password).toBe("secret #3");
    expect(JSON.stringify(first)).not.toMatch(/hunter2|t0k/);
  });

  it("leaves inputs without a secret value as they are", () => {
    const { result } = renderHook(() => useSecretStandIns(fields));
    const plain = { name: "web", password: "" };
    expect(result.current(plain)).toBe(plain);
  });
});
