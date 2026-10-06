import { useRouterState } from "@tanstack/react-router";
import { StripBanner } from "@/components/StripBanner";
import { useBootstrap } from "@/lib/dashboard";
import { showDomainSetupBanner } from "@/lib/domain-setup";

export function DomainSetupBanner() {
  const { data } = useBootstrap();
  const pathname = useRouterState({ select: (state) => state.location.pathname });
  if (!showDomainSetupBanner(data, pathname)) return null;

  return (
    <StripBanner
      lead="Dashboard domain"
      message="Set a public host so Geass can verify HTTPS and finish platform setup."
      href="/settings/domain"
      actionLabel="Configure domain"
    />
  );
}
