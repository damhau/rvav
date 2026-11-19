import { createFileRoute } from "@tanstack/react-router";
import { OnboardingPage, onboardingTabs, type OnboardingTab } from "@/features/admin/OnboardingPage";
import { optEnum } from "../../-search";

export const Route = createFileRoute("/admin/onboarding/")({
  validateSearch: (s: Record<string, unknown>): { tab?: OnboardingTab } => ({
    tab: optEnum(s.tab, onboardingTabs),
  }),
  component: function OnboardingRoute() {
    return <OnboardingPage tab={Route.useSearch().tab} />;
  },
});
