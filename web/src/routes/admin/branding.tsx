import { createFileRoute } from "@tanstack/react-router";
import { BrandingPage } from "@/features/admin/BrandingPage";

export const Route = createFileRoute("/admin/branding")({
  component: BrandingPage,
});
