import { resourceName } from "@/lib/api";
import { ServiceMetrics } from "@/components/ServiceMetrics";
import { ProjectChrome } from "@/layout/ProjectChrome";

export function ProjectMonitorPage({ project, environment }) {
  return (
    <ProjectChrome>
      <div className="rw-pad">
        <ServiceMetrics scope="projects" name={resourceName(project)} environment={environment} />
      </div>
    </ProjectChrome>
  );
}
