import { createFileRoute } from "@tanstack/react-router";
import { NotInSlice } from "@/features/NotInSlice";

export const Route = createFileRoute("/approvals")({
  component: () => (
    <NotInSlice
      title="Approvals"
      what="The approvals inbox comes with the approvals API. Requests that need approval already show “Awaiting approval” and wait until then."
    />
  ),
});
