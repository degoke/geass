import { useEffect, useState } from "react";
import { Link, useNavigate, useRouterState } from "@tanstack/react-router";
import { Burger, Menu, useMantineColorScheme } from "@mantine/core";
import {
  BarChart3,
  Check,
  ChevronDown,
  ExternalLink,
  FolderKanban,
  Layers,
  LayoutTemplate,
  LifeBuoy,
  LogOut,
  Monitor,
  Moon,
  PanelLeftClose,
  PanelLeftOpen,
  Settings,
  Sun,
} from "lucide-react";
import { action, list, resourceName } from "@/lib/api";
import { alertUser } from "@/lib/notify";
import { canMutate, dashboardSession, useBootstrap } from "@/lib/dashboard";
import { openResourceDialog } from "@/lib/services";
import {
  ACCOUNT_POOLS,
  ACCOUNT_PRIMARY,
  ACCOUNT_USAGE,
  FOOTER_LINKS,
  settingsActive,
} from "@/lib/platform-nav";
import { ClusterHealth } from "@/components/ClusterHealth";
import { GeassLogo } from "@/components/GeassLogo";
import { DeployControls } from "@/components/DeployControls";
import { DomainSetupBanner } from "@/components/DomainSetupBanner";

const NAV_ICONS = {
  projects: FolderKanban,
  templates: LayoutTemplate,
  cluster: BarChart3,
  pools: Layers,
  general: Settings,
};

const FOOTER_ICONS = {
  docs: ExternalLink,
  support: LifeBuoy,
};

const ACCOUNT_NAV_COLLAPSED_KEY = "geass-account-nav-collapsed";
const PROJECT_SECTIONS = ["monitor", "logs", "settings"];

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

/** "auto" follows the system. The setting is kept by Mantine, in this browser. */
const THEME_CHOICES = [
  { value: "auto", label: "System", Icon: Monitor },
  { value: "light", label: "Light", Icon: Sun },
  { value: "dark", label: "Dark", Icon: Moon },
];

function initial(username) {
  return (username || "?").slice(0, 1).toUpperCase();
}

export function AppLayout({ children }) {
  const { data, refetch } = useBootstrap();
  const pathname = useRouterState({ select: (state) => state.location.pathname });
  const search = useRouterState({ select: (state) => state.location.search || {} });
  const navigate = useNavigate();
  const session = dashboardSession(data);
  const username = session.username || "Signed in";
  const [navOpen, setNavOpen] = useState(false);
  const [accountNavCollapsed, toggleAccountNav] = useAccountNavCollapsed();

  useEffect(() => {
    setNavOpen(false);
  }, [pathname]);

  const parts = pathname.split("/").filter(Boolean);
  const projectSlug = parts[0] === "projects" ? parts[1] : "";
  const projects = list(data, "projects");
  const project = projectSlug ? projects.find((item) => resourceName(item) === projectSlug) : null;
  const inProject = Boolean(project);
  const onResourceRoute = inProject && parts.length > 3;
  const environment = search.environment || project?.spec?.environments?.[0] || "";
  const envSearch = environment ? { environment } : undefined;

  // A resource that exists in one environment may not exist in another, so switching environments from a
  // resource page goes back to the project instead of to a page that would be empty.
  const setEnvironment = (next) => {
    if (onResourceRoute) {
      navigate({ to: `/projects/${projectSlug}`, search: next ? { environment: next } : undefined });
      return;
    }
    navigate({ search: (previous) => ({ ...previous, environment: next || undefined }) });
  };

  const signOut = () => {
    action("/logout")
      .then(() => window.location.reload())
      .catch((error) => alertUser(error.message));
  };

  return (
    <div className="rw-shell">
      <TopBar
        data={data}
        project={project}
        projectSlug={projectSlug}
        projects={projects}
        environment={environment}
        onEnvironmentChange={setEnvironment}
        onProjectChange={(name) => navigate({ to: `/projects/${name}` })}
        onAllProjects={() => navigate({ to: "/projects" })}
        username={username}
        onSignOut={signOut}
        reload={refetch}
        navOpened={navOpen}
        onToggleNav={() => setNavOpen((open) => !open)}
      />
      <div className="rw-shell-body">
        <nav className={`rw-nav rw-nav-home${accountNavCollapsed ? " is-collapsed" : ""}`} data-open={navOpen ? "true" : "false"}>
          <AccountNav
            pathname={pathname}
            collapsed={accountNavCollapsed}
            onToggleCollapsed={toggleAccountNav}
            data={data}
          />
        </nav>
        <main className="rw-main">
          {inProject ? (
            <>
              <ProjectTabs
                projectSlug={projectSlug}
                envSearch={envSearch}
                pathname={pathname}
                canAdd={canMutate(data)}
              />
              <div className="rw-stage rw-stage-project">
                <div className="rw-frame rw-frame-flush">
                  <div className="rw-frame-body">{children}</div>
                </div>
              </div>
            </>
          ) : (
            <div className="rw-account-pane">
              <DomainSetupBanner />
              <div className="rw-account-main">{children}</div>
            </div>
          )}
        </main>
      </div>
    </div>
  );
}

function TopBar({
  data,
  project,
  projectSlug,
  projects,
  environment,
  onEnvironmentChange,
  onProjectChange,
  onAllProjects,
  username,
  onSignOut,
  reload,
  navOpened,
  onToggleNav,
}) {
  const { colorScheme, setColorScheme } = useMantineColorScheme();
  const inProject = Boolean(project && projectSlug);
  const environments = project?.spec?.environments || [];
  const pullRequests = project?.status?.pullRequests || [];
  return (
    <header className="rw-top">
      <Burger className="rw-top-burger" opened={navOpened} onClick={onToggleNav} hiddenFrom="sm" size="sm" aria-label="Open navigation" />
      <Link to="/projects" className="rw-top-brand" aria-label="Geass home">
        <GeassLogo height={24} width={91} />
      </Link>

      {inProject ? (
        <>
          <Menu position="bottom-start">
            <Menu.Target>
              <button type="button" className="rw-top-project" aria-label="Switch project">
                {project.spec?.displayName || projectSlug}
                <ChevronDown size={14} aria-hidden="true" />
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
              <Menu.Divider />
              <Menu.Item leftSection={<FolderKanban size={14} />} onClick={() => onAllProjects?.()}>
                All projects
              </Menu.Item>
            </Menu.Dropdown>
          </Menu>

          <nav className="rw-env-tabs" aria-label="Environments">
            {environments.map((env) => {
              const pullRequest = pullRequests.find((entry) => entry.environment === env);
              const active = env === environment;
              return (
                <button
                  key={env}
                  type="button"
                  className={active ? "rw-env-tab is-on" : "rw-env-tab"}
                  aria-current={active ? "true" : undefined}
                  onClick={() => onEnvironmentChange?.(env)}
                >
                  {env}
                  {pullRequest ? <span className="rw-env-badge">PR #{pullRequest.number}</span> : null}
                </button>
              );
            })}
            {canMutate(data) ? (
              <Link to={`/projects/${projectSlug}/settings`} search={environment ? { environment } : undefined} className="rw-env-tab rw-env-add">
                + Environment
              </Link>
            ) : null}
          </nav>
        </>
      ) : null}

      <div className="rw-top-end">
        {inProject ? <DeployControls projectName={projectSlug} environment={environment} data={data} reload={reload} /> : null}
        <Menu position="bottom-end" width={200}>
          <Menu.Target>
            <button type="button" className="rw-top-avatar" aria-label="Account menu" title={username}>
              {initial(username)}
            </button>
          </Menu.Target>
          <Menu.Dropdown>
            <Menu.Label>{username}</Menu.Label>
            <Menu.Divider />
            <Menu.Label>Theme</Menu.Label>
            {THEME_CHOICES.map(({ value, label, Icon }) => (
              <Menu.Item
                key={value}
                aria-current={colorScheme === value ? "true" : undefined}
                leftSection={<Icon size={14} />}
                rightSection={colorScheme === value ? <Check size={14} /> : null}
                onClick={() => setColorScheme(value)}
              >
                {label}
              </Menu.Item>
            ))}
            <Menu.Divider />
            <Menu.Item leftSection={<LogOut size={14} />} color="red" onClick={onSignOut}>Sign out</Menu.Item>
          </Menu.Dropdown>
        </Menu>
      </div>
    </header>
  );
}

function ProjectTabs({ projectSlug, envSearch, pathname, canAdd }) {
  const section = pathname.split("/").filter(Boolean)[2];
  const onResources = !PROJECT_SECTIONS.includes(section);
  const tabs = [
    { id: "resources", label: "Resources", to: `/projects/${projectSlug}`, on: onResources },
    { id: "monitor", label: "Metrics", to: `/projects/${projectSlug}/monitor`, on: section === "monitor" },
    { id: "logs", label: "Logs", to: `/projects/${projectSlug}/logs`, on: section === "logs" },
    { id: "settings", label: "Settings", to: `/projects/${projectSlug}/settings`, on: section === "settings" },
  ];
  return (
    <div className="rw-ptabs">
      <nav className="rw-ptabs-list" aria-label="Project">
        {tabs.map((tab) => (
          <Link
            key={tab.id}
            to={tab.to}
            search={envSearch}
            className={tab.on ? "rw-ptab is-on" : "rw-ptab"}
            aria-current={tab.on ? "page" : undefined}
          >
            {tab.label}
          </Link>
        ))}
      </nav>
      {onResources && canAdd ? (
        <div className="rw-ptabs-actions">
          <Link to="/templates" className="rw-btn-ghost">From template</Link>
          <button type="button" className="rw-btn-white" onClick={openResourceDialog}>+ Add resource</button>
        </div>
      ) : null}
    </div>
  );
}

function AccountNav({ pathname, collapsed, onToggleCollapsed, data }) {
  const usageActive = ACCOUNT_USAGE.active(pathname);
  const settingsOn = settingsActive(pathname) && !usageActive;
  const UsageIcon = NAV_ICONS.cluster;
  const PoolsIcon = NAV_ICONS.pools;
  const sideLink = (to, active, icon, label) => (
    <Link
      key={label}
      to={to}
      className={active ? "rw-side-link rw-side-on" : "rw-side-link"}
      title={label}
      aria-label={label}
      aria-current={active ? "page" : undefined}
    >
      {icon}
      <span className="rw-side-link-label">{label}</span>
    </Link>
  );

  return (
    <div className={collapsed ? "rw-account is-collapsed" : "rw-account"}>
      <div className="rw-account-body">
        <nav className="rw-nav-stack" aria-label="Account">
          {ACCOUNT_PRIMARY.map((item) => {
            const Icon = NAV_ICONS[item.icon] || FolderKanban;
            return sideLink(item.to, item.active(pathname), <Icon size={16} />, item.label);
          })}
          {sideLink(ACCOUNT_USAGE.to, usageActive, <UsageIcon size={16} />, ACCOUNT_USAGE.label)}
          {sideLink(ACCOUNT_POOLS.to, ACCOUNT_POOLS.active(pathname), <PoolsIcon size={16} />, ACCOUNT_POOLS.label)}
          {sideLink("/settings", settingsOn, <Settings size={16} />, "Settings")}
        </nav>

        <div className="rw-account-foot">
          {!collapsed ? <ClusterHealth data={data} /> : null}
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
            <button
              type="button"
              className="rw-footer-link rw-collapse-toggle"
              onClick={() => onToggleCollapsed?.()}
              aria-label={collapsed ? "Expand sidebar" : "Collapse sidebar"}
              title={collapsed ? "Expand sidebar" : "Collapse sidebar"}
            >
              <span className="rw-side-link-label">Collapse</span>
              {collapsed ? <PanelLeftOpen size={14} /> : <PanelLeftClose size={14} />}
            </button>
          </div>
        </div>
      </div>
    </div>
  );
}
