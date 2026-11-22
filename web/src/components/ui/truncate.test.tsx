import { render, screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { afterEach, describe, expect, it, vi } from "vitest";
import { ScrollRegion } from "./scroll-region";
import { TooltipProvider } from "./tooltip";
import { Truncate } from "./truncate";

const fqdn = "web-prd-front001.corp.example";

function measure(scrollWidth: number, clientWidth: number) {
  vi.spyOn(HTMLElement.prototype, "scrollWidth", "get").mockReturnValue(scrollWidth);
  vi.spyOn(HTMLElement.prototype, "clientWidth", "get").mockReturnValue(clientWidth);
}

afterEach(() => vi.restoreAllMocks());

describe("Truncate", () => {
  it("shows a cut name whole in a tooltip on keyboard focus", async () => {
    measure(320, 120);
    const user = userEvent.setup();
    render(
      <TooltipProvider>
        <Truncate mono>{fqdn}</Truncate>
      </TooltipProvider>,
    );
    await user.tab();
    expect(screen.getByText(fqdn, { selector: "span.truncate" })).toHaveFocus();
    expect(await screen.findByRole("tooltip")).toHaveTextContent(fqdn);
    await user.keyboard("{Escape}");
    expect(screen.queryByRole("tooltip")).toBeNull();
  });

  it("leaves a name that fits out of the tab order, without a tooltip", async () => {
    measure(120, 120);
    const user = userEvent.setup();
    render(
      <TooltipProvider>
        <Truncate>{fqdn}</Truncate>
      </TooltipProvider>,
    );
    const name = screen.getByText(fqdn);
    expect(name).not.toHaveAttribute("tabindex");
    await user.hover(name);
    expect(screen.queryByRole("tooltip")).toBeNull();
  });
});

describe("ScrollRegion", () => {
  it("takes the focus, named, only while what it holds is wider than it", () => {
    measure(900, 300);
    const { unmount } = render(
      <ScrollRegion label="Virtual machines">
        <table aria-label="Virtual machines" />
      </ScrollRegion>,
    );
    const region = screen.getByRole("group", { name: "Virtual machines, scrolls sideways" });
    expect(region).toHaveAttribute("tabindex", "0");
    unmount();

    measure(300, 300);
    render(
      <ScrollRegion label="Virtual machines">
        <table aria-label="Virtual machines" />
      </ScrollRegion>,
    );
    expect(screen.queryByRole("group")).toBeNull();
  });
});
