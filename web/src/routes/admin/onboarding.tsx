import { createFileRoute } from "@tanstack/react-router";
import { OnboardingPage } from "@/features/admin/OnboardingPage";

export const Route = createFileRoute("/admin/onboarding")({
  component: OnboardingPage,
});
