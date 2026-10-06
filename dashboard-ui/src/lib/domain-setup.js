import { canMutate } from "@/lib/dashboard";

export function showDomainSetupBanner(data, pathname) {
  if (!data || !canMutate(data)) return false;
  if (data.platform?.hasDashboardURL) return false;
  if (pathname === "/settings/domain" || pathname.startsWith("/settings/domain/")) return false;
  return true;
}
