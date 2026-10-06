import { Button, Center, Loader, Stack, Text, Title } from "@mantine/core";
import { useNavigate, useRouterState } from "@tanstack/react-router";
import { list, resourceName } from "@/lib/api";
import { useBootstrap } from "@/lib/dashboard";
import { AppLayout } from "@/layout/AppLayout";
import { Empty } from "@/components/geass-ui";
import { LoginPage } from "@/pages/LoginPage";
import { ProjectsPage } from "@/pages/ProjectsPage";
import { TemplatesPage } from "@/pages/TemplatesPage";
import { WorkspacePage } from "@/pages/WorkspacePage";
import { ProjectSettingsPage } from "@/pages/ProjectSettingsPage";
import { ProjectMonitorPage } from "@/pages/ProjectMonitorPage";
import { ResourceDetailPage } from "@/pages/ResourceDetailPage";
import { PlatformSettingsPage } from "@/pages/platform/PlatformSettingsPage";
import { PlatformSettingsLayout } from "@/layout/PlatformSettingsLayout";

export function DashboardScreen({ mode }) {
  const { data, error, isPending, refetch } = useBootstrap();
  const navigate = useNavigate();
  const routeState = useRouterState({ select: (state) => ({ location: state.location, params: state.matches.at(-1)?.params || {} }) });
  const search = routeState.location.search || {};
  const params = routeState.params;
  if (error?.status === 401) return <LoginPage onSuccess={() => refetch()} />;
  if (error) {
    const devHint = error.status === 404
      ? "From the repo root, run make dashboard-backend (API on :8085) while pnpm dev runs the UI on :5176, or use make run for both."
      : null;
    return (
      <Center mih="100vh" px="md">
        <Stack align="center" gap="sm" maw={520} ta="center">
          <img src="/geass-symbol.svg" alt="" width={40} height={40} />
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
      <Center mih="100vh">
        <Stack align="center" gap="sm">
          <img src="/geass-symbol.svg" alt="" width={36} height={36} />
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
  if (mode === "projects") { content = <ProjectsPage data={data} />; }
  else if (mode === "templates") { content = <TemplatesPage />; }
  else if (mode === "workspace" && project) {
    content = <WorkspacePage project={project} data={data} environment={environment} setEnvironment={setEnvironment} />;
  } else if (mode === "project-monitor" && project) {
    content = <ProjectMonitorPage project={project} data={data} environment={environment} setEnvironment={setEnvironment} />;
  } else if (mode === "project-settings" && project) {
    content = <ProjectSettingsPage project={project} data={data} reload={refetch} environment={environment} setEnvironment={setEnvironment} />;
  } else if (mode.startsWith("resource:") && project) {
    const kind = mode.slice("resource:".length);
    content = <ResourceDetailPage project={project} data={data} kind={kind} name={params.name} reload={refetch} />;
  } else if (mode === "settings" || mode.startsWith("settings:")) {
    const page = mode.split(":")[1] || "general";
    content = (
      <PlatformSettingsLayout>
        <PlatformSettingsPage data={data} page={page} reload={refetch} />
      </PlatformSettingsLayout>
    );
  } else {
    content = <Empty title="Page not found" description="The requested dashboard page does not exist." action={<Button onClick={() => navigate({ to: "/projects" })}>Open projects</Button>} />;
  }
  return (
    <AppLayout>{content}</AppLayout>
  );
}
