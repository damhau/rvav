import { createFileRoute } from "@tanstack/react-router";
import { PlanPage } from "@/features/admin/onboarding/PlanPage";

export const Route = createFileRoute("/admin/onboarding/plans/$planId")({
  component: function OnboardingPlanRoute() {
    const { planId } = Route.useParams();
    return <PlanPage planId={planId} />;
  },
});
