import { useRouterState } from "@tanstack/react-router";
import { BarChart3, Bell, Building2, Container, Globe, HardDrive, KeyRound, LogIn, Plug, Settings } from "lucide-react";
import { settingsLinksFor } from "@/lib/platform-nav";
import { isPlatformSession, useBootstrap } from "@/lib/dashboard";
import { useDashboardOptions, visibleSettingsLinks } from "@/lib/options";
import { SettingsShell } from "@/layout/SettingsShell";

const SETTINGS_ICONS = {
  general: Settings,
  usage: BarChart3,
  domain: Globe,
  connectors: Plug,
  tokens: KeyRound,
  auth: LogIn,
  organizations: Building2,
  alerts: Bell,
  registry: Container,
  object: HardDrive,
};

export function PlatformSettingsLayout({ children }) {
  const pathname = useRouterState({ select: (state) => state.location.pathname });
  const { data } = useBootstrap();
  const { hidden } = useDashboardOptions();
  const nav = visibleSettingsLinks(settingsLinksFor(isPlatformSession(data)), hidden).map((item) => ({
    id: item.id,
    label: item.label,
    to: item.to,
    icon: SETTINGS_ICONS[item.id] || Settings,
    active: item.active(pathname),
  }));

  return (
    <SettingsShell title="Settings" nav={nav}>
      {children}
    </SettingsShell>
  );
}
