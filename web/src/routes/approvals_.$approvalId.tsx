import { createFileRoute, redirect } from "@tanstack/react-router";

// Links in notifications name an approval level: it opens in the inbox.
export const Route = createFileRoute("/approvals_/$approvalId")({
  beforeLoad: ({ params }) => {
    // TanStack Router's redirect is thrown, as its documentation does.
    // eslint-disable-next-line @typescript-eslint/only-throw-error
    throw redirect({ to: "/approvals", search: { id: params.approvalId } });
  },
});
