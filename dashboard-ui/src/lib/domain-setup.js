import { canMutate } from "@/lib/dashboard";
import { managedFor } from "@/lib/managed";

export function showDomainSetupBanner(data, pathname) {
  if (!data || !canMutate(data)) return false;
  if (data.platform?.hasDashboardURL) return false;
  // The platform that runs this Geass sets the domain, so there is nothing to ask the person for.
  if (managedFor(data, "dashboardDomain")) return false;
  if (pathname === "/settings/domain" || pathname.startsWith("/settings/domain/")) return false;
  return true;
}
