import { createFileRoute } from "@tanstack/react-router";
import { FormLibraryPage } from "@/features/admin/catalog/FormLibraryPage";

export const Route = createFileRoute("/admin/form-library")({
  component: FormLibraryPage,
});
