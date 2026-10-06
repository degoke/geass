import { useEffect, useState } from "react";
import { Link, useNavigate, useRouterState } from "@tanstack/react-router";
import { Burger, Kbd, Menu } from "@mantine/core";
import { useDisclosure } from "@mantine/hooks";
import {
  Activity,
  BarChart3,
  Bell,
  ChevronDown,
  ExternalLink,
  FolderKanban,
  LayoutGrid,
  LayoutTemplate,
  LifeBuoy,
  LogOut,
  MoreVertical,
  PanelLeftClose,
  PanelLeftOpen,
  Settings,
} from "lucide-react";
import { action, list, resourceName } from "@/lib/api";
import { alertUser } from "@/lib/notify";
import { dashboardSession, useBootstrap } from "@/lib/dashboard";
import {
  ACCOUNT_PRIMARY,
  ACCOUNT_USAGE,
  FOOTER_LINKS,
  settingsActive,
} from "@/lib/platform-nav";
import { DomainSetupBanner } from "@/components/DomainSetupBanner";
import { EnvironmentMenu } from "@/components/ProjectEnvironmentBar";

const NAV_ICONS = {
  projects: FolderKanban,
  templates: LayoutTemplate,
  cluster: BarChart3,
  general: Settings,
};

const FOOTER_ICONS = {
  docs: ExternalLink,
  support: LifeBuoy,
};

const ACCOUNT_NAV_COLLAPSED_KEY = "geass-account-nav-collapsed";

function useAccountNavCollapsed() {
  const [collapsed, setCollapsed] = useState(() => {
    try {
      return localStorage.getItem(ACCOUNT_NAV_COLLAPSED_KEY) === "1";
    } catch {
      return false;
    }
  });
  const toggleCollapsed = () => {
    setCollapsed((value) => {
      const next = !value;
      try {
        localStorage.setItem(ACCOUNT_NAV_COLLAPSED_KEY, next ? "1" : "0");
      } catch {
        /* ignore */
      }
      return next;
    });
  };
  return [collapsed, toggleCollapsed];
}

function initial(username) {
  return (username || "?").slice(0, 1).toUpperCase();
}

export function AppLayout({ children }) {
  const { data } = useBootstrap();
  const pathname = useRouterState({ select: (state) => state.location.pathname });
  const search = useRouterState({ select: (state) => state.location.search || {} });
  const navigate = useNavigate();
  const session = dashboardSession(data);
  const username = session.username || "Signed in";
  const [opened, { toggle, close }] = useDisclosure();
  const [accountNavCollapsed, toggleAccountNav] = useAccountNavCollapsed();

  useEffect(() => {
    close();
  }, [pathname, close]);

  const parts = pathname.split("/").filter(Boolean);
  const projectSlug = parts[0] === "projects" ? parts[1] : "";
  const project = projectSlug ? list(data, "projects").find((item) => resourceName(item) === projectSlug) : null;
  const inProject = Boolean(project);
  const environment = search.environment || project?.spec?.environments?.[0] || "";
  const setEnvironment = (next) => navigate({ search: (previous) => ({ ...previous, environment: next || undefined }) });
  const envSearch = environment ? { environment } : undefined;

  const signOut = () => {
    action("/logout")
      .then(() => window.location.reload())
      .catch((error) => alertUser(error.message));
  };

  return (
    <div className="rw-shell">
      <Burger className="rw-burger" opened={opened} onClick={toggle} hiddenFrom="sm" size="sm" aria-label="Open navigation" />
      <div className={inProject ? "rw-shell-project" : "rw-shell-account"} style={{ display: "flex", minHeight: "100dvh" }}>
        <nav
          className={`rw-nav rw-nav-home${accountNavCollapsed ? " is-collapsed" : ""}`}
          data-open={opened ? "true" : "false"}
        >
          <AccountNav
            pathname={pathname}
            username={username}
            onSignOut={signOut}
            collapsed={accountNavCollapsed}
            projectSlug={inProject ? projectSlug : ""}
            envSearch={inProject ? envSearch : undefined}
          />
        </nav>
        <main style={{ flex: 1, minWidth: 0, display: "flex", flexDirection: "column" }}>
          {inProject ? (
            <>
              <HomeTopBar
                collapsed={accountNavCollapsed}
                onToggleCollapsed={toggleAccountNav}
                project={project}
                projectSlug={projectSlug}
                projects={list(data, "projects")}
                environment={environment}
                onEnvironmentChange={setEnvironment}
                onProjectChange={(name) => navigate({ to: `/projects/${name}` })}
              />
              <div className="rw-stage rw-stage-project">
                <div className="rw-frame rw-frame-flush">
                  <div className="rw-frame-body">{children}</div>
                </div>
              </div>
            </>
          ) : (
            <>
              <HomeTopBar collapsed={accountNavCollapsed} onToggleCollapsed={toggleAccountNav} />
              <div className="rw-account-pane">
                <DomainSetupBanner />
                <div className="rw-account-main">{children}</div>
              </div>
            </>
          )}
        </main>
      </div>
    </div>
  );
}

function HomeTopBar({
  collapsed,
  onToggleCollapsed,
  project,
  projectSlug,
  projects,
  environment,
  onEnvironmentChange,
  onProjectChange,
}) {
  const inProject = Boolean(project && projectSlug);
  return (
    <header className="rw-home-top">
      <div className="rw-home-top-start">
        <button
          type="button"
          className="rw-icon-btn"
          onClick={() => onToggleCollapsed?.()}
          aria-label={collapsed ? "Expand sidebar" : "Collapse sidebar"}
          title={collapsed ? "Expand sidebar" : "Collapse sidebar"}
        >
          {collapsed ? <PanelLeftOpen size={18} /> : <PanelLeftClose size={18} />}
        </button>
        {inProject ? (
          <div className="rw-home-top-project">
            <Menu position="bottom-start">
              <Menu.Target>
                <button type="button" className="rw-crumb">
                  {project.spec?.displayName || projectSlug}
                  <ChevronDown size={14} />
                </button>
              </Menu.Target>
              <Menu.Dropdown>
                {projects.map((item) => {
                  const name = resourceName(item);
                  return (
                    <Menu.Item key={name} onClick={() => onProjectChange?.(name)}>
                      {item.spec?.displayName || name}
                    </Menu.Item>
                  );
                })}
              </Menu.Dropdown>
            </Menu>
            <EnvironmentMenu project={project} environment={environment} onChange={onEnvironmentChange} />
          </div>
        ) : null}
      </div>
      <div className="rw-home-top-actions">
        <button type="button" className="rw-icon-btn" aria-label="Notifications">
          <Bell size={18} />
        </button>
      </div>
    </header>
  );
}

function AccountNav({ pathname, username, onSignOut, collapsed, projectSlug, envSearch }) {
  const onPlatformSettings = settingsActive(pathname);
  const onProjectSettings = Boolean(projectSlug) && pathname.includes("/settings");
  const onMetrics = Boolean(projectSlug) && pathname.endsWith("/monitor");
  const onCanvas = Boolean(projectSlug) && !onProjectSettings && !onMetrics;
  const UsageIcon = NAV_ICONS.cluster;
  const sideLink = (to, active, icon, label, search) => (
    <Link
      to={to}
      search={search}
      className={active ? "rw-side-link rw-side-on" : "rw-side-link"}
      title={label}
      aria-label={label}
    >
      {icon}
      <span className="rw-side-link-label">{label}</span>
    </Link>
  );

  const inProject = Boolean(projectSlug);

  return (
    <div className={collapsed ? "rw-account is-collapsed" : "rw-account"}>
      <Link
        to="/projects"
        className="rw-side-brand"
        aria-label="Geass home"
        title="Projects"
      >
        <img className="rw-side-brand-logo" src="/geass-logo-dark.svg" alt="Geass" height={20} width={152} />
        <img className="rw-side-brand-mark" src="/geass-symbol.svg" alt="" width={22} height={22} />
      </Link>
      <div className="rw-account-body">
        <nav className="rw-nav-stack" aria-label={inProject ? "Project" : "Account"}>
          {inProject ? (
            <div className="rw-nav-group">
              {sideLink(`/projects/${projectSlug}`, onCanvas, <LayoutGrid size={16} />, "Canvas", envSearch)}
              {sideLink(`/projects/${projectSlug}/monitor`, onMetrics, <Activity size={16} />, "Metrics", envSearch)}
              {sideLink(`/projects/${projectSlug}/settings`, onProjectSettings, <Settings size={16} />, "Settings", envSearch)}
            </div>
          ) : (
            <>
              <div className="rw-nav-group">
                {ACCOUNT_PRIMARY.map((item) => {
                  const Icon = NAV_ICONS[item.icon] || FolderKanban;
                  return sideLink(item.to, item.active(pathname), <Icon size={16} />, item.label);
                })}
              </div>
              <div className="rw-nav-group rw-nav-group-separated">
                {sideLink(ACCOUNT_USAGE.to, ACCOUNT_USAGE.active(pathname), <UsageIcon size={16} />, ACCOUNT_USAGE.label)}
                {sideLink("/settings", onPlatformSettings, <Settings size={16} />, "Settings")}
              </div>
            </>
          )}
        </nav>

        <div className="rw-account-foot">
          {!inProject ? (
            <div className="rw-nav-footer">
              {FOOTER_LINKS.map((item) => {
                const FooterIcon = FOOTER_ICONS[item.id] || ExternalLink;
                return (
                  <a
                    key={item.id}
                    href={item.href}
                    className="rw-footer-link"
                    target="_blank"
                    rel="noreferrer"
                    title={item.label}
                    aria-label={item.label}
                  >
                    <span className="rw-side-link-label">{item.label}</span>
                    <FooterIcon size={14} className="rw-footer-link-icon" />
                  </a>
                );
              })}
            </div>
          ) : null}

          <Menu position={collapsed ? "right-end" : "top-start"} width={200}>
            <Menu.Target>
              <button type="button" className="rw-user-bottom" aria-label="Account menu" title={username}>
                <span className="rw-avatar">{initial(username)}</span>
                <span className="rw-user-name">{username}</span>
                <MoreVertical size={16} className="rw-user-more" />
              </button>
            </Menu.Target>
            <Menu.Dropdown>
              <Menu.Label>{username}</Menu.Label>
              <Menu.Item leftSection={<LogOut size={14} />} color="red" onClick={onSignOut}>Sign out</Menu.Item>
            </Menu.Dropdown>
          </Menu>
        </div>
      </div>
    </div>
  );
}
