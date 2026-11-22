import { render, screen } from "@testing-library/react";
import { describe, expect, it } from "vitest";
import { fieldControl } from "@/forms/attention";
import { focusInvalid } from "./opener";
import { Sheet } from "./sheet";

// A Button that turns disabled while focused stays aria-disabled until the
// focus leaves it (button.tsx): the focus pickers pass over one.
describe("focus pickers and a held button", () => {
  it("moves the focus into an invalid group past a held button", () => {
    render(
      <div role="radiogroup" aria-label="Size" aria-invalid="true">
        <button type="button" aria-disabled="true">
          Checking…
        </button>
        <input aria-label="Medium" type="radio" />
      </div>,
    );
    expect(focusInvalid(document.body)).toBe(true);
    expect(screen.getByRole("radio", { name: "Medium" })).toHaveFocus();
  });

  it("gives a form field's control, not its held button", () => {
    render(
      <div data-field="network">
        <button type="button" aria-disabled="true">
          Loading…
        </button>
        <input aria-label="Network" />
      </div>,
    );
    expect(fieldControl("network")).toBe(screen.getByRole("textbox", { name: "Network" }));
  });

  it("opens a sheet on its first field, past a held button", () => {
    render(
      <Sheet open onOpenChange={() => {}} title="New size">
        <button type="button" aria-disabled="true">
          Reading…
        </button>
        <input aria-label="Name" />
      </Sheet>,
    );
    expect(screen.getByRole("textbox", { name: "Name" })).toHaveFocus();
  });
});
