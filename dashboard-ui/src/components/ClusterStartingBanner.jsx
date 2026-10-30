import { StripBanner } from "@/components/StripBanner";
import { useBootstrap } from "@/lib/dashboard";
import { clusterStartingNotice } from "@/lib/cluster-starting";

/** Says that Geass is still starting, so that nobody has to ask the server. It goes when the cluster reports ready. */
export function ClusterStartingBanner() {
  const { data } = useBootstrap();
  const notice = clusterStartingNotice(data);
  if (!notice) return null;
  return <StripBanner lead={notice.lead} message={notice.message} />;
}
