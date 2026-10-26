import { createFileRoute } from "@tanstack/react-router";
import { NotInSlice } from "@/features/NotInSlice";

export const Route = createFileRoute("/reports")({
  component: () => (
    <NotInSlice
      title="Reports"
      what="Usage, success rate, provisioning time and lease reports come in a later milestone."
    />
  ),
});
