import { describe, expect, it } from "vitest";
import { MANAGED_AREAS, MANAGED_BY_DEFAULT, managedBy, managedFor, managedMessage, managedOf } from "@/lib/managed";
import { showDomainSetupBanner } from "@/lib/domain-setup";
import { versionDescription } from "@/components/ClusterVersion";

const admin = { username: "admin", role: "admin", canMutate: true, platformAdmin: true };
const managed = (extra = {}) => ({ enabled: true, provider: "rungeass", locked: [...MANAGED_AREAS], ...extra });
const bootstrap = (platform = {}) => ({ platform: { session: admin, ...platform } });

describe("managed mode, from the bootstrap", () => {
  it("is nothing unless the server says it is on", () => {
    expect(managedOf(bootstrap())).toBeNull();
    expect(managedOf(bootstrap({ managed: { enabled: false, locked: [...MANAGED_AREAS] } }))).toBeNull();
    expect(managedOf(undefined)).toBeNull();
    for (const area of MANAGED_AREAS) expect(managedFor(bootstrap(), area), area).toBeNull();
    expect(managedBy(bootstrap())).toBe("");
  });

  it("holds the areas the server lists, and no other", () => {
    const data = bootstrap({ managed: managed({ locked: ["signIn"] }) });
    expect(managedFor(data, "signIn")).toEqual({ area: "signIn", by: "rungeass" });
    for (const area of MANAGED_AREAS.filter((name) => name !== "signIn")) expect(managedFor(data, area), area).toBeNull();
  });

  it("names the platform, and says so when it has no name", () => {
    expect(managedFor(bootstrap({ managed: managed() }), "dashboardDomain").by).toBe("rungeass");
    expect(managedFor(bootstrap({ managed: managed({ provider: undefined }) }), "dashboardDomain").by).toBe(MANAGED_BY_DEFAULT);
    expect(managedBy(bootstrap({ managed: managed({ provider: "" }) }))).toBe("the platform that runs this cluster");
  });

  it("says what the server's refusal says, word for word", () => {
    const by = "rungeass";
    expect(managedMessage({ area: "signIn", by })).toBe("Sign-in settings are managed by rungeass and cannot be changed here.");
    expect(managedMessage({ area: "dashboardDomain", by })).toBe("The dashboard's domain and DNS are managed by rungeass and cannot be changed here.");
    expect(managedMessage({ area: "installer", by })).toBe("The backup of the cluster's state, which the installer sets up, is managed by rungeass and cannot be changed here.");
    expect(managedMessage({ area: "upgrades", by })).toBe("Updating Geass is managed by rungeass and cannot be changed here.");
    expect(managedMessage({ area: "k3sVersion", by })).toBe("The K3s version is managed by rungeass and cannot be changed here.");
  });
});

describe("what managed mode changes in the pages' logic", () => {
  it("stops asking the person to set the dashboard's domain", () => {
    expect(showDomainSetupBanner(bootstrap({ hasDashboardURL: false }), "/projects")).toBe(true);
    expect(showDomainSetupBanner(bootstrap({ hasDashboardURL: false, managed: managed() }), "/projects")).toBe(false);
    expect(showDomainSetupBanner(bootstrap({ hasDashboardURL: false, managed: managed({ locked: ["signIn"] }) }), "/projects")).toBe(true);
  });

  it("stops telling the person to run the installer to update", () => {
    expect(versionDescription(bootstrap())).toBe("Update by running a newer installer on the control plane.");
    expect(versionDescription(bootstrap({ managed: managed() }))).toBe("Updating Geass and the K3s version is managed by rungeass and cannot be changed here.");
    expect(versionDescription(bootstrap({ managed: managed({ locked: ["upgrades"] }) }))).toBe("Updating Geass is managed by rungeass and cannot be changed here.");
    expect(versionDescription(bootstrap({ managed: managed({ locked: ["k3sVersion"] }) }))).toBe("The K3s version is managed by rungeass and cannot be changed here.");
    expect(versionDescription(bootstrap({ managed: managed({ locked: ["signIn"] }) }))).toBe("Update by running a newer installer on the control plane.");
  });
});
