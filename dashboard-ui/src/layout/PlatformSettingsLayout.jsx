import { useRouterState } from "@tanstack/react-router";
import { BarChart3, Cloud, Globe, HardDrive, Plug, Settings } from "lucide-react";
import { SETTINGS_LINKS } from "@/lib/platform-nav";
import { SettingsShell } from "@/layout/SettingsShell";

const SETTINGS_ICONS = {
  general: Settings,
  usage: BarChart3,
  domain: Globe,
  connectors: Plug,
  object: HardDrive,
  cloud: Cloud,
};

export function PlatformSettingsLayout({ children }) {
  const pathname = useRouterState({ select: (state) => state.location.pathname });
  const nav = SETTINGS_LINKS.map((item) => ({
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
