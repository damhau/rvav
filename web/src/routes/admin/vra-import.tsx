import { createFileRoute } from "@tanstack/react-router";
import { VraImportPage } from "@/features/admin/VraImportPage";

export const Route = createFileRoute("/admin/vra-import")({
  component: VraImportPage,
});
