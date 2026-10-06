import { Button, Group, Menu, SimpleGrid } from "@mantine/core";
import { Calendar, Info } from "lucide-react";
import { resourceName } from "@/lib/api";
import { projectResources } from "@/lib/project-resources";
import { ProjectChrome } from "@/layout/ProjectChrome";

const GREEN = "#59c77a";
const BLUE = "#888cf8";
const ORANGE = "#a3a3ab";
const PURPLE = "#d8d6ff";

function legendItem(color, label, value) {
  return { color, label, value: String(value) };
}

function cardsFor(metrics, resources) {
  const measured = (metric, color, label) => ({
    title: metric?.title || label,
    legend: [
      legendItem(
        metric?.state === "unavailable" ? "#8d8d8d" : color,
        metric?.state === "unavailable" ? "Unavailable" : "Measured",
        metric?.state === "unavailable" ? "—" : metric?.value ?? "0",
      ),
    ],
  });
  const pods = metrics.find((metric) => /pod/i.test(metric.title));
  const nodes = metrics.find((metric) => /node/i.test(metric.title));
  const cpu = metrics.find((metric) => /cpu/i.test(metric.title));
  return [
    measured(pods, GREEN, "Running pods"),
    measured(nodes, BLUE, "Nodes"),
    measured(cpu, ORANGE, "CPU"),
    {
      title: "Resources",
      legend: [
        legendItem(BLUE, "Services", resources.apps.length),
        legendItem(ORANGE, "Databases", resources.databases.length + resources.logical.length),
        legendItem(PURPLE, "Buckets", resources.stores.length),
      ],
    },
  ];
}

function MetricCard({ title, legend }) {
  return (
    <div className="rw-metric">
      <div className="rw-metric-head">
        <Group gap={6} wrap="nowrap">
          <span>{title}</span>
          <Info size={14} color="#8d8d8d" aria-label={`${title} details`} />
        </Group>
      </div>
      <div className="rw-legend">
        {legend.map((item) => (
          <span key={item.label}>
            <i className="rw-dot" style={{ background: item.color }} />
            {item.label} {item.value}
          </span>
        ))}
      </div>
      <div className="rw-plot">No data is available for this time range</div>
    </div>
  );
}

export function ProjectMonitorPage({ project, data, environment, setEnvironment }) {
  const name = resourceName(project);
  const resources = projectResources(data, name, environment);
  const cards = cardsFor(data?.metrics || [], resources);

  return (
    <ProjectChrome>
      <div className="rw-pad">
      <Group justify="flex-end" align="center" mb="md">
        <Menu position="bottom-end">
          <Menu.Target>
            <Button variant="default" color="gray" leftSection={<Calendar size={14} />}>Current</Button>
          </Menu.Target>
          <Menu.Dropdown>
            <Menu.Item>Current</Menu.Item>
          </Menu.Dropdown>
        </Menu>
      </Group>
      <SimpleGrid cols={{ base: 1, md: 2 }} spacing="md">
        {cards.map((card) => <MetricCard key={card.title} title={card.title} legend={card.legend} />)}
      </SimpleGrid>
      </div>
    </ProjectChrome>
  );
}
