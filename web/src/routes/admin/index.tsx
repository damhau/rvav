import { createFileRoute, notFound } from "@tanstack/react-router";

// Administration is a menu, not a page.
export const Route = createFileRoute("/admin/")({
  beforeLoad: () => {
    // TanStack Router's notFound is thrown, as its documentation does.
    // eslint-disable-next-line @typescript-eslint/only-throw-error
    throw notFound();
  },
});
