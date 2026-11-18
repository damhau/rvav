import { render, screen } from "@testing-library/react";
import { describe, expect, it } from "vitest";
import { Markdown } from "./markdown";

describe("Markdown", () => {
  it("renders CommonMark, its headings below the section's", () => {
    render(<Markdown headingLevel={3}>{"# Use\n\nSome **bold** text.\n\n## Then\n\n- one\n- two"}</Markdown>);
    expect(screen.getByRole("heading", { name: "Use", level: 3 })).toBeInTheDocument();
    expect(screen.getByRole("heading", { name: "Then", level: 4 })).toBeInTheDocument();
    expect(screen.getByText("bold").tagName).toBe("STRONG");
    expect(screen.getAllByRole("listitem")).toHaveLength(2);
  });

  it("leaves out HTML, images and script links", () => {
    const { container } = render(
      <Markdown>
        {[
          "<script>alert(1)</script>",
          "",
          'Hello <b>there</b> <img src="x" onerror="alert(1)">',
          "",
          "![logo](https://example.com/logo.png)",
          "",
          "[evil](javascript:alert(1)) and [docs](https://example.com/docs)",
        ].join("\n")}
      </Markdown>,
    );
    expect(container.querySelector("script, b, img, [onerror]")).toBeNull();
    expect(container).not.toHaveTextContent("alert(1)");
    expect(screen.getByRole("link", { name: "docs" })).toHaveAttribute("href", "https://example.com/docs");
    expect(screen.getByRole("link", { name: "docs" })).toHaveAttribute("rel", "noreferrer");
    expect(screen.getByText("evil").closest("a")?.getAttribute("href") ?? "").not.toMatch(/javascript/i);
  });
});
