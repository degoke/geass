import { describe, expect, it } from "vitest";
import { clusterStartingNotice } from "./cluster-starting";

const now = Date.parse("2026-10-30T12:00:00Z");
const cluster = (phase, created) => ({ metadata: { creationTimestamp: created }, status: phase ? { phase } : {} });

describe("clusterStartingNotice", () => {
  it("says nothing when the cluster is ready, or when there is no cluster to read", () => {
    expect(clusterStartingNotice({ clusters: { items: [cluster("Ready", "2026-10-30T11:55:00Z")] } }, now)).toBeNull();
    expect(clusterStartingNotice({ clusters: { items: [] } }, now)).toBeNull();
    expect(clusterStartingNotice({}, now)).toBeNull();
  });

  it("says that a new cluster is starting", () => {
    const notice = clusterStartingNotice({ clusters: { items: [cluster("", "2026-10-30T11:55:00Z")] } }, now);
    expect(notice.lead).toBe("Geass is starting");
  });

  it("says that an older cluster is not ready, not that it is starting", () => {
    const notice = clusterStartingNotice({ clusters: { items: [cluster("", "2026-10-29T08:00:00Z")] } }, now);
    expect(notice.lead).toBe("Geass is not ready");
  });
});
