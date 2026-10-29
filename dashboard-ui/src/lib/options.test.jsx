import { describe, expect, it } from "vitest";
import { HIDEABLE, SELF_HOSTED_ONLY, pageHidden, visibleSettingsLinks } from "@/lib/options";
import { SETTINGS_LINKS, settingsLinksFor } from "@/lib/platform-nav";
import { normalizeBasePath } from "@/lib/base-path";

describe("hide", () => {
  it("takes away what a host that runs Geass for people manages, and leaves the logo", () => {
    expect([...SELF_HOSTED_ONLY].sort()).toEqual(["account", "alerts", "domain", "registry", "servers", "signin", "tokens", "upgrade"]);
    expect(HIDEABLE).toContain("brand");
    expect(SELF_HOSTED_ONLY).not.toContain("brand");
  });

  it("takes away the pages that belong to a hidden part, and no other", () => {
    const hidden = new Set(SELF_HOSTED_ONLY);
    for (const mode of ["settings:domain", "settings:tokens", "settings:auth", "settings:organizations", "settings:alerts", "settings:registry", "device"]) {
      expect(pageHidden(mode, hidden), mode).toBe(true);
      expect(pageHidden(mode, new Set()), mode).toBe(false);
    }
    for (const mode of ["projects", "templates", "settings", "settings:connectors", "settings:cluster", "settings:object", "settings:databases", "pools", "workspace"]) {
      expect(pageHidden(mode, hidden), mode).toBe(false);
    }
  });

  it("takes away the links of the settings menu, and the ones an organization is shown too", () => {
    const hidden = new Set(SELF_HOSTED_ONLY);
    const platform = visibleSettingsLinks(settingsLinksFor(true), hidden).map((link) => link.id);
    expect(platform).toEqual(["general", "usage", "connectors", "object", "databases"]);
    expect(visibleSettingsLinks(settingsLinksFor(false), hidden)).toEqual([]);
    expect(visibleSettingsLinks(SETTINGS_LINKS, new Set())).toEqual(SETTINGS_LINKS);
  });
});

describe("normalizeBasePath", () => {
  it.each([
    ["", "/"],
    ["/", "/"],
    ["acme/clusters/c1", "/acme/clusters/c1"],
    ["/acme/clusters/c1/", "/acme/clusters/c1"],
    ["//acme//c1//", "/acme/c1"],
    [undefined, "/"],
  ])("%j is %j", (input, want) => {
    expect(normalizeBasePath(input)).toBe(want);
  });
});
