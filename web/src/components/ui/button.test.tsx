import { render, screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { useState } from "react";
import { describe, expect, it, vi } from "vitest";
import { Button } from "./button";

function Saving({ onSubmit }: { onSubmit: () => void }) {
  const [pending, setPending] = useState(false);
  return (
    <form
      onSubmit={(e) => {
        e.preventDefault();
        onSubmit();
        setPending(true);
      }}
    >
      <input aria-label="Name" />
      <Button type="submit" disabled={pending}>
        Save
      </Button>
      <Button type="button">Other</Button>
    </form>
  );
}

describe("a button that turns disabled", () => {
  it("keeps the focus, ignores presses until the focus leaves, then is disabled", async () => {
    const user = userEvent.setup();
    const onSubmit = vi.fn();
    render(<Saving onSubmit={onSubmit} />);
    const save = screen.getByRole("button", { name: "Save" });
    save.focus();
    await user.keyboard("{Enter}");
    expect(onSubmit).toHaveBeenCalledTimes(1);
    expect(save).toHaveFocus();
    expect(save).toHaveAttribute("aria-disabled", "true");
    expect(save).toBeEnabled();

    await user.keyboard("{Enter}");
    await user.keyboard(" ");
    await user.click(save);
    expect(onSubmit).toHaveBeenCalledTimes(1);

    await user.click(screen.getByLabelText("Name"));
    await user.keyboard("{Enter}");
    expect(onSubmit).toHaveBeenCalledTimes(1);
    expect(save).toBeDisabled();
    expect(save).not.toHaveAttribute("aria-disabled");
  });

  it("that had no focus is disabled at once", () => {
    const { rerender } = render(<Button disabled={false}>Run</Button>);
    rerender(<Button disabled>Run</Button>);
    expect(screen.getByRole("button", { name: "Run" })).toBeDisabled();
  });
});
