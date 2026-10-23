import { useQuery } from "@tanstack/react-query";
import { api } from "@/lib/api";
import { memoryBytes, sizeLabel, typicalValue } from "@/lib/capacity";
import { formatBytes } from "@/lib/usage";
import { Muted } from "@/components/geass-ui";

// One sample every 15 minutes in the 24-hour window, so an hour of use: less than that is a service still starting up.
const MIN_SAMPLES = 4;

/**
 * Says so when a service's copies usually use more memory than the form reserves for them. Such a service is among the first
 * stopped when its server runs short, whatever its ceiling is. The median is what counts, not the peak: going above what is
 * reserved for a while is what the ceiling is for. Shows nothing without Prometheus, or without data to go by.
 */
export function MemoryReserveHint({ app, memory, data }) {
  // The key is the one the Metrics tab uses for the same window, so the two share what they fetched.
  const metrics = useQuery({
    queryKey: ["metrics", "apps", app, "", "24h"],
    queryFn: () => api(`/api/apps/${app}/metrics?window=24h`),
    staleTime: 60000,
    retry: false,
  });
  if (!metrics.data?.available) return null;

  const typical = typicalValue(metrics.data.series?.memoryPerReplica, MIN_SAMPLES);
  const reserved = memoryBytes(memory);
  if (typical === null || !reserved || typical <= reserved) return null;

  const reservedLabel = sizeLabel(data?.platform?.capacity?.memorySizes || [], memory, formatBytes(reserved));
  return (
    <Muted size="xs" mt="sm">
      Each copy typically uses about {formatBytes(typical)} but only {reservedLabel} is reserved, so it is stopped first when memory
      runs short.
    </Muted>
  );
}
