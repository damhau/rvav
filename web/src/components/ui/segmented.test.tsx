import { render, screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { LayoutGrid, Table2 } from "lucide-react";
import { describe, expect, it, vi } from "vitest";
import { Segmented } from "./segmented";

describe("Segmented", () => {
  it("presses the option chosen, names icon-only ones by their label, and reports a pick", async () => {
    const user = userEvent.setup();
    const onChange = vi.fn();
    render(
      <Segmented
        label="Show as"
        value="table"
        onChange={onChange}
        options={[
          { value: "table", label: "Table", icon: Table2, iconOnly: true },
          { value: "cards", label: "Cards", icon: LayoutGrid, iconOnly: true },
        ]}
      />,
    );
    expect(screen.getByRole("group", { name: "Show as" })).toBeInTheDocument();
    expect(screen.getByRole("button", { name: "Table" })).toHaveAttribute("aria-pressed", "true");
    expect(screen.getByRole("button", { name: "Cards" })).toHaveAttribute("aria-pressed", "false");
    expect(screen.getByRole("button", { name: "Cards" })).toHaveTextContent("");

    await user.click(screen.getByRole("button", { name: "Cards" }));
    expect(onChange).toHaveBeenCalledWith("cards");
  });
});
