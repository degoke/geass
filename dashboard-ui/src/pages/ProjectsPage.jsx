import { useEffect, useMemo, useRef, useState } from "react";
import { useMutation, useQueryClient } from "@tanstack/react-query";
import { Link, useNavigate } from "@tanstack/react-router";
import { Kbd, TextInput } from "@mantine/core";
import { Box, ChevronDown, Database, HardDrive, Plus, Search } from "lucide-react";
import { action, isReady, list, resourceName } from "@/lib/api";
import { canMutate } from "@/lib/dashboard";
import { alertUser } from "@/lib/notify";
import { projectResources } from "@/lib/project-resources";
import { defaultRegion, hasRegionChoice, platformRegions } from "@/lib/placement";
import { Button, Select } from "@/components/geass-ui";

function allResources(resources) {
  return [...resources.apps, ...resources.databases, ...resources.stores, ...resources.logical];
}

function resourceTotal(resources) {
  return allResources(resources).length;
}

const ICONS = [
  { key: "apps", icon: Box, color: "#7ce0a0" },
  { key: "databases", icon: Database, color: "#a1a1a1" },
  { key: "logical", icon: Database, color: "#f2f2f2" },
  { key: "stores", icon: HardDrive, color: "#bdf1d1" },
];

export function ProjectsPage({ data }) {
  const navigate = useNavigate();
  const queryClient = useQueryClient();
  const searchRef = useRef(null);
  const [query, setQuery] = useState("");
  const [sort, setSort] = useState("recent");
  const [region, setRegion] = useState("");
  const create = useMutation({
    mutationFn: () => action("/projects/create", region ? { region } : {}),
    onSuccess: (result) => {
      queryClient.invalidateQueries({ queryKey: ["dashboard", "bootstrap"] });
      navigate({ to: result.project ? `/projects/${result.project}` : "/projects" });
    },
    onError: (error) => alertUser(error.message),
  });
  const projects = useMemo(() => {
    const rows = list(data, "projects").filter((project) => {
      const haystack = `${project.spec?.displayName || ""} ${resourceName(project)}`.toLowerCase();
      return haystack.includes(query.toLowerCase());
    });
    rows.sort((a, b) => {
      if (sort === "name") return (a.spec?.displayName || resourceName(a)).localeCompare(b.spec?.displayName || resourceName(b));
      return new Date(b.metadata?.creationTimestamp || 0) - new Date(a.metadata?.creationTimestamp || 0);
    });
    return rows;
  }, [data, query, sort]);

  useEffect(() => {
    const onKey = (event) => {
      if ((event.metaKey || event.ctrlKey) && event.key.toLowerCase() === "k") {
        event.preventDefault();
        searchRef.current?.focus();
      }
    };
    window.addEventListener("keydown", onKey);
    return () => window.removeEventListener("keydown", onKey);
  }, []);

  return (
    <div className="rw-projects-page">
      <div className="rw-projects-head">
        <h1>Projects</h1>
        <div className="rw-projects-tools">
          <TextInput
            ref={searchRef}
            placeholder="Search projects..."
            value={query}
            onChange={(event) => setQuery(event.currentTarget.value)}
            leftSection={<Search size={14} color="#a1a1a1" />}
            rightSection={<Kbd size="xs">⌘K</Kbd>}
            rightSectionWidth={42}
            aria-label="Search projects"
            classNames={{ input: "rw-search-input" }}
            w={{ base: "100%", sm: 280 }}
          />
          {canMutate(data) && hasRegionChoice(data) ? (
            <Select value={region} onChange={(event) => setRegion(event.currentTarget.value)} aria-label="Region for the new project" w={{ base: "100%", sm: 190 }}>
              <option value="">{defaultRegion(data) ? `Default region (${defaultRegion(data)})` : "Any region"}</option>
              {platformRegions(data).map((entry) => <option key={entry.name} value={entry.name}>{entry.name}</option>)}
            </Select>
          ) : null}
          {canMutate(data) ? (
            <Button onClick={() => create.mutate()} disabled={create.isPending} leftSection={<Plus size={15} />}>
              New project
            </Button>
          ) : null}
        </div>
      </div>
      <p className="rw-count">
        <span>{projects.length} {projects.length === 1 ? "Project" : "Projects"}</span>
        <button type="button" className="rw-sort" onClick={() => setSort(sort === "recent" ? "name" : "recent")}>
          Sort by: {sort === "recent" ? "Recent activity" : "Name"}
          <ChevronDown size={14} />
        </button>
      </p>
      <div className="rw-project-grid">
        {projects.map((project) => {
          const name = resourceName(project);
          const resources = projectResources(data, name);
          const total = resourceTotal(resources);
          const online = allResources(resources).filter((item) => isReady(item)).length;
          const env = (project.spec?.environments || [])[0] || "production";
          const icons = ICONS.filter((entry) => resources[entry.key]?.length);
          return (
            <Link key={name} to={`/projects/${name}`} className="rw-project-card">
              <div className="rw-project-name">{project.spec?.displayName || name}</div>
              <div className="rw-project-dots">
                {(icons.length ? icons : [{ key: "empty", icon: Box, color: "#8f8f8f" }]).map((entry) => {
                  const Icon = entry.icon;
                  return (
                    <span key={entry.key} className="rw-mini">
                      <Icon size={16} color={entry.color} />
                    </span>
                  );
                })}
              </div>
              <div className="rw-project-foot">
                <i className={`rw-dot${total && online === total ? "" : " rw-dot-idle"}${total && online < total ? " rw-dot-warn" : ""}`} />
                <span>
                  {env} · {total ? `${online}/${total} ${total === 1 ? "service" : "services"} online` : "No services yet"}
                </span>
              </div>
            </Link>
          );
        })}
      </div>
    </div>
  );
}
