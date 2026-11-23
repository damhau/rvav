import { createFileRoute } from "@tanstack/react-router";
import {
  OnboardingPage,
  onboardingTabs,
  powerStates,
  type OnboardingSearch,
  type OnboardingTab,
} from "@/features/admin/OnboardingPage";
import { optEnum, optString } from "../../-search";

export const Route = createFileRoute("/admin/onboarding/")({
  validateSearch: (s: Record<string, unknown>): { tab?: OnboardingTab } & OnboardingSearch => ({
    tab: optEnum(s.tab, onboardingTabs),
    q: optString(s.q),
    folder: optString(s.folder, 400),
    vcenter: optString(s.vcenter, 120),
    power: optEnum(s.power, powerStates),
  }),
  component: function OnboardingRoute() {
    return <OnboardingPage tab={Route.useSearch().tab} />;
  },
});
