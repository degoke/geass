import { Button, Center, Loader, Stack, Text, Title } from "@mantine/core";
import { useNavigate, useRouterState } from "@tanstack/react-router";
import { list, resourceName } from "@/lib/api";
import { useBootstrap } from "@/lib/dashboard";
import { useDashboardLiveSync } from "@/lib/dashboard-events";
import { AppLayout } from "@/layout/AppLayout";
import { Empty } from "@/components/geass-ui";
import { GeassLogo } from "@/components/GeassLogo";
import { useDashboardOptions, pageHidden } from "@/lib/options";
import { LoginPage } from "@/pages/LoginPage";
import { SetupPage } from "@/pages/SetupPage";
import { DeviceApprovePage } from "@/pages/DeviceApprovePage";
import { ProjectsPage } from "@/pages/ProjectsPage";
import { TemplatesPage } from "@/pages/TemplatesPage";
import { ProjectWorkspaceShell } from "@/pages/ProjectWorkspaceShell";
import { ProjectSettingsPage } from "@/pages/ProjectSettingsPage";
import { ProjectMonitorPage } from "@/pages/ProjectMonitorPage";
import { ProjectLogsPage } from "@/pages/ProjectLogsPage";
import { PlatformSettingsPage } from "@/pages/platform/PlatformSettingsPage";
import { PoolsPage } from "@/pages/platform/PoolsPage";
import { PoolDetailPage } from "@/pages/platform/PoolDetailPage";
import { PlatformSettingsLayout } from "@/layout/PlatformSettingsLayout";

export function DashboardScreen({ mode }) {
  const { data, error, isPending, refetch } = useBootstrap();
  const { embedded, hidden, onUnauthorized } = useDashboardOptions();
  // A page of its own fills the window; one inside another app fills the room the host gave it.
  const fill = embedded ? "100%" : "100vh";
  useDashboardLiveSync(Boolean(data) && !error);
  const navigate = useNavigate();
  const routeState = useRouterState({ select: (state) => ({ location: state.location, params: state.matches.at(-1)?.params || {} }) });
  const search = routeState.location.search || {};
  const params = routeState.params;
  // Mounted in another app there is no sign-in page: the host signs people in, so what is left is to ask it for a new token.
  if (error?.status === 401 && embedded) {
    return (
      <Center mih={fill} px="md">
        <Stack align="center" gap="sm" maw={520} ta="center">
          <GeassLogo symbol alt="" width={40} height={40} />
          <Title order={2}>Session ended</Title>
          <Text size="sm">This dashboard could not sign in to the cluster. Your access may have expired.</Text>
          <Button onClick={() => { onUnauthorized?.(); refetch(); }}>Reconnect</Button>
        </Stack>
      </Center>
    );
  }
  if (mode === "setup" && !embedded) return <SetupPage onSuccess={() => { navigate({ to: "/projects" }); refetch(); }} />;
  if (error?.status === 401) return <LoginPage onSuccess={() => refetch()} />;
  if (error) {
    const devHint = error.status === 404 && !embedded
      ? "Run make dashboard-backend, or make run for UI and API."
      : null;
    return (
      <Center mih={fill} px="md">
        <Stack align="center" gap="sm" maw={520} ta="center">
          <GeassLogo symbol alt="" width={40} height={40} />
          <Title order={2}>Dashboard unavailable</Title>
          <Text size="sm">{error.message}</Text>
          {devHint && <Text c="dimmed" size="xs">{devHint}</Text>}
          <Button onClick={() => refetch()}>Try again</Button>
        </Stack>
      </Center>
    );
  }
  if (isPending || !data) {
    return (
      <Center mih={fill}>
        <Stack align="center" gap="sm">
          <GeassLogo symbol alt="" width={36} height={36} />
          <Loader color="signal" type="dots" />
          <Text c="dimmed" size="sm">Loading Geass…</Text>
        </Stack>
      </Center>
    );
  }
  const projects = list(data, "projects");
  const project = projects.find((item) => resourceName(item) === params.projectName);
  let content;
  let environment = search.environment || project?.spec?.environments?.[0] || "";
  const setEnvironment = (next) => navigate({ search: (previous) => ({ ...previous, environment: next || undefined }) });
  if (pageHidden(mode, hidden)) {
    content = <Empty title="Not available here" description="This page is managed for you, so it is not part of this dashboard." action={<Button onClick={() => navigate({ to: "/projects" })}>Open projects</Button>} />;
  } else if (mode === "projects") { content = <ProjectsPage data={data} />; }
  else if (mode === "templates") { content = <TemplatesPage data={data} />; }
  else if (mode === "pools") { content = <PoolsPage data={data} />; }
  else if (mode === "pool") { content = <PoolDetailPage name={params.poolName} data={data} />; }
  else if (mode === "device") { content = <DeviceApprovePage data={data} code={search.code} />; }
  else if (mode === "workspace" && project) {
    content = (
      <ProjectWorkspaceShell
        project={project}
        data={data}
        environment={environment}
        reload={refetch}
      />
    );
  } else if (mode.startsWith("resource:") && project) {
    const kind = mode.slice("resource:".length);
    content = (
      <ProjectWorkspaceShell
        project={project}
        data={data}
        environment={environment}
        resourceKind={kind}
        resourceName={params.name}
        reload={refetch}
      />
    );
  } else if (mode === "project-monitor" && project) {
    content = <ProjectMonitorPage project={project} data={data} environment={environment} setEnvironment={setEnvironment} />;
  } else if (mode === "project-logs" && project) {
    content = <ProjectLogsPage project={project} data={data} environment={environment} />;
  } else if (mode === "project-settings" && project) {
    content = <ProjectSettingsPage project={project} data={data} reload={refetch} environment={environment} setEnvironment={setEnvironment} />;
  } else if (mode === "settings" || mode.startsWith("settings:")) {
    const page = mode.split(":")[1] || "general";
    content = (
      <PlatformSettingsLayout>
        <PlatformSettingsPage data={data} page={page} reload={refetch} />
      </PlatformSettingsLayout>
    );
  } else {
    content = <Empty title="Page not found" action={<Button onClick={() => navigate({ to: "/projects" })}>Open projects</Button>} />;
  }
  return (
    <AppLayout>{content}</AppLayout>
  );
}
