import { useEffect, useMemo, useRef, useState } from "react";
import { Modal } from "@mantine/core";
import { useQuery, useQueryClient } from "@tanstack/react-query";
import { Link, useNavigate, useRouterState } from "@tanstack/react-router";
import { ArrowLeft, Container, Database, GitBranch, HardDrive, Info, Layers, Search, Sparkles, SquareFunction } from "lucide-react";
import { action, api, condition, list, resourceDisplayName, resourceName } from "@/lib/api";
import { validateContainerImageReference } from "@/lib/container-image";
import { FUNCTION_RUNTIMES } from "@/lib/functions";
import { alertUser, notifyTemplateAdded } from "@/lib/notify";
import { availableConnections } from "@/lib/project-resources";
import { projectRegion, sharedServerRegion } from "@/lib/placement";
import { Button, Muted } from "@/components/geass-ui";

const IMAGE_EXAMPLES = [
  "hello-world",
  "ghcr.io/username/repo:latest",
  "quay.io/username/repo:tag",
  "registry.gitlab.com/username/repo:tag",
  "mcr.microsoft.com/username/repo:tag",
];

function openResourceSettings(navigate, project, environment, kind, createdName, view = "settings") {
  navigate({
    to: `/projects/${project}/${kind}/${createdName}`,
    search: { environment, view },
  });
}

function draftPayload(project, environment, extra = {}) {
  const projectName = String(project || "").trim();
  if (!projectName) {
    return null;
  }
  return {
    project: projectName,
    environment: String(environment || "").trim(),
    draft: "on",
    ...extra,
  };
}

/** Parses "owner/repo" or a github.com URL (with optional .git or /tree/branch). */
export function parseGitHubRepository(text) {
  const value = String(text || "").trim();
  const url = value.match(/^(?:https?:\/\/)?(?:www\.)?github\.com\/([\w.-]+)\/([\w.-]+?)(?:\.git)?(?:\/(?:tree|blob)\/([^\s]+?))?\/?$/i);
  if (url) return { repository: `${url[1]}/${url[2]}`, branch: url[3] ? url[3].split("/")[0] : "" };
  const short = value.match(/^([\w.-]+)\/([\w.-]+)$/);
  if (short && !value.includes(".") && !value.includes(":")) return { repository: value, branch: "" };
  return null;
}

function looksLikeImage(text) {
  const value = String(text || "").trim();
  if (!value || /\s/.test(value)) return false;
  return validateContainerImageReference(value).ok;
}

const STEP_PLACEHOLDER = {
  root: "Search, or paste a repo link or image name",
  database: "Choose a database…",
  bucket: "Choose a bucket…",
  function: "Choose a runtime…",
  template: "Choose a template…",
  github: "Search your repositories…",
  image: "kennethreitz/httpbin",
};

export function ResourceDialog({ project, environment, data }) {
  const queryClient = useQueryClient();
  const navigate = useNavigate();
  const inputRef = useRef(null);
  const platform = data?.platform || {};
  const currentProject = list(data, "projects").find((item) => resourceName(item) === project);
  const githubLinked = Boolean(currentProject?.spec?.githubConnectionRef?.name);
  const githubReady = Boolean(platform.hasGitHubApp);
  const servers = list(data, "databases").filter((item) => item.spec?.project === project && item.spec?.environment === environment && (item.spec?.engine === "Postgres" || item.spec?.engine === "MySQL" || !item.spec?.engine));
  // Servers shared by every project: a database with no project. A project can only use one in its own
  // region, and one that is still starting cannot take a database yet.
  const region = projectRegion(currentProject, data);
  const sharedServers = list(data, "databases").filter((item) => {
    if (item.spec?.project || !(item.spec?.engine === "Postgres" || item.spec?.engine === "MySQL")) return false;
    const where = sharedServerRegion(item);
    return !where || !region || where === region;
  });
  const awsConnections = availableConnections(data, "AWS", project);
  const planetConnections = availableConnections(data, "PlanetScale", project);
  const objectStorageAvailable = Boolean(platform.objectStorageAvailable);
  const objectStorageEngine = platform.objectStorageEngine || "";
  const clusterBucketDescription = objectStorageAvailable
    ? `Stored on this cluster with ${objectStorageEngine}`
    : "Set up object storage in platform settings first";
  const [open, setOpen] = useState(false);
  const [step, setStep] = useState("root");
  const [text, setText] = useState("");
  const [active, setActive] = useState(0);
  const [imageError, setImageError] = useState("");
  const [creating, setCreating] = useState(false);

  const resetDialog = (nextStep = "root") => {
    setStep(nextStep);
    setText("");
    setActive(0);
    setImageError("");
    setCreating(false);
  };

  useEffect(() => {
    const openDialog = (event) => { resetDialog(event?.detail?.step || "root"); setOpen(true); };
    window.addEventListener("geass:open-resource", openDialog);
    return () => window.removeEventListener("geass:open-resource", openDialog);
  }, []);

  useEffect(() => {
    if (open) setTimeout(() => inputRef.current?.focus(), 30);
    setActive(0);
  }, [open, step]);

  // The GitHub install callback returns here with ?create=app-git to continue picking a repository.
  const createParam = useRouterState({ select: (state) => state.location.search?.create });
  useEffect(() => {
    if (createParam !== "app-git") return;
    resetDialog("github");
    setOpen(true);
    navigate({ search: (previous) => ({ ...previous, create: undefined }), replace: true });
  }, [createParam]);

  const repos = useQuery({
    queryKey: ["github-repos", project],
    queryFn: () => api(`/api/projects/${project}/github/repos`),
    enabled: open && step === "github" && githubReady && githubLinked,
  });

  const templates = useQuery({
    queryKey: ["templates"],
    queryFn: () => api("/api/templates"),
    enabled: open && step === "template",
  });

  const deployTemplate = (template) => {
    setCreating(true);
    return action(`/templates/${template.id}/deploy`, { project, environment })
      .then(() => {
        queryClient.invalidateQueries({ queryKey: ["dashboard", "bootstrap"] });
        setOpen(false);
        notifyTemplateAdded(template);
      })
      .catch((error) => alertUser(error.message))
      .finally(() => setCreating(false));
  };

  const afterCreate = (result, kind, message, view = "settings") => {
    queryClient.invalidateQueries({ queryKey: ["dashboard", "bootstrap"] });
    setOpen(false);
    const createdName = result?.name;
    if (createdName) {
      openResourceSettings(navigate, project, environment, kind, createdName, view);
      alertUser(message);
      return;
    }
    alertUser("Resource created");
  };

  const missingProject = () => alertUser("Project is missing. Open a project workspace and try again.");

  const createBucket = (kind) => {
    const values = draftPayload(project, environment, {
      engine: kind === "s3" ? "S3" : objectStorageEngine,
      placement: kind === "s3" ? "External" : "InCluster",
    });
    if (!values) return missingProject();
    if (kind === "s3" && awsConnections[0]) values.connectionRef = resourceName(awsConnections[0]);
    return action("/object-stores/create", values)
      .then((result) => afterCreate(result, "object-stores", "Bucket saved as a draft. Configure it in settings, then deploy."))
      .catch((error) => alertUser(error.message));
  };

  const createGitService = (repository, branch) => {
    const values = draftPayload(project, environment, { source: "git", repository: repository || "", branch: branch || "" });
    if (!values) return missingProject();
    setCreating(true);
    return action("/apps/create", values)
      .then((result) => afterCreate(result, "apps", "Service saved as a draft. Configure it in settings, then deploy."))
      .catch((error) => alertUser(error.message))
      .finally(() => setCreating(false));
  };

  const createFunction = (runtime) => {
    const values = draftPayload(project, environment, { source: "function", runtime });
    if (!values) return missingProject();
    setCreating(true);
    return action("/apps/create", values)
      .then((result) => afterCreate(result, "apps", "Function saved as a draft. Edit the code, then deploy.", "code"))
      .catch((error) => alertUser(error.message))
      .finally(() => setCreating(false));
  };

  const createDockerService = (value) => {
    const checked = validateContainerImageReference(value);
    if (!checked.ok) {
      setImageError(checked.message);
      return undefined;
    }
    setImageError("");
    const values = draftPayload(project, environment, { image: checked.value });
    if (!values) return missingProject();
    setCreating(true);
    return action("/apps/create", values)
      .then((result) => afterCreate(result, "apps", "Service saved as a draft. Adjust settings or variables, then deploy."))
      .catch((error) => alertUser(error.message))
      .finally(() => setCreating(false));
  };

  const createDatabase = (row) => {
    if (row.value === "logical") {
      const values = draftPayload(project, environment, { server: row.server || resourceName(servers[0]) });
      if (!values) return missingProject();
      return action("/logical-databases/create", values)
        .then((result) => afterCreate(result, "logical-databases", "Logical database saved as a draft. Configure it in settings."))
        .catch((error) => alertUser(error.message));
    }
    const values = draftPayload(project, environment, {
      engine: row.engine || "Postgres",
      placement: row.placement || "InCluster",
      provider: row.provider || "",
    });
    if (!values) return missingProject();
    if (row.provider === "PlanetScale" && planetConnections[0]) values.connectionRef = resourceName(planetConnections[0]);
    if (row.provider === "AWS" && awsConnections[0]) values.connectionRef = resourceName(awsConnections[0]);
    return action("/databases/create", values)
      .then((result) => afterCreate(result, "databases", "Database saved as a draft. Configure it in settings, then deploy."))
      .catch((error) => alertUser(error.message));
  };

  const startGitHubInstall = () => {
    action(`/projects/${project}/github/install`, { environment })
      .then((result) => {
        if (result?.url) window.location.assign(result.url);
      })
      .catch((error) => alertUser(error.message));
  };

  const query = text.trim();
  const lower = query.toLowerCase();
  const parsedRepo = parseGitHubRepository(query);

  /** Rows for the current step: { key, label, description, icon, chevron, disabled, onSelect }. */
  const rows = useMemo(() => {
    const matches = (label) => !lower || label.toLowerCase().includes(lower);
    if (step === "root") {
      const out = [];
      if (parsedRepo) {
        out.push({
          key: "repo-url",
          label: `Deploy ${parsedRepo.repository}`,
          description: githubReady ? (githubLinked ? "From GitHub" : "Link this project to GitHub first") : "Connect GitHub in platform settings first",
          icon: GitBranch,
          disabled: !githubReady,
          onSelect: () => (githubLinked ? createGitService(parsedRepo.repository, parsedRepo.branch) : setStep("github")),
        });
      } else if (looksLikeImage(query) && (query.includes("/") || query.includes(":"))) {
        out.push({
          key: "image-text",
          label: `Deploy image ${query}`,
          description: "From a container registry",
          icon: Container,
          onSelect: () => createDockerService(query),
        });
      }
      const base = [
        { key: "github", label: "GitHub Repository", icon: GitBranch, chevron: true, onSelect: () => { setText(""); setStep("github"); } },
        { key: "database", label: "Database", icon: Database, chevron: true, onSelect: () => { setText(""); setStep("database"); } },
        { key: "template", label: "Template", icon: Layers, chevron: true, onSelect: () => { setText(""); setStep("template"); } },
        { key: "image", label: "Docker Image", icon: Container, chevron: true, onSelect: () => { setText(""); setStep("image"); } },
        { key: "function", label: "Function", icon: SquareFunction, chevron: true, onSelect: () => { setText(""); setStep("function"); } },
        { key: "bucket", label: "Bucket", icon: HardDrive, chevron: true, onSelect: () => { setText(""); setStep("bucket"); } },
      ];
      return [...out, ...base.filter((row) => matches(row.label))];
    }
    if (step === "database") {
      const dbRows = [
        { key: "postgres", label: "PostgreSQL", engine: "Postgres", placement: "InCluster" },
        { key: "mysql", label: "MySQL", engine: "MySQL", placement: "InCluster" },
        { key: "redis", label: "Redis", engine: "Redis", placement: "InCluster" },
        { key: "sqlite", label: "SQLite", engine: "SQLite", placement: "InCluster" },
        {
          key: "planetscale", label: "PlanetScale", engine: "MySQL", placement: "External", provider: "PlanetScale",
          description: "Coming soon",
          disabled: true,
        },
        {
          key: "awsdb", label: "AWS RDS", engine: "Postgres", placement: "External", provider: "AWS",
          description: awsConnections.length ? "Managed Postgres" : "Add an AWS connection in platform settings",
          disabled: awsConnections.length === 0,
        },
        {
          key: "logical", label: "Logical database", value: "logical",
          description: servers.length ? "Create inside an existing server" : "Create a database server first",
          disabled: servers.length === 0,
        },
        ...sharedServers.map((server) => ({
          key: `shared-${resourceName(server)}`,
          label: `${resourceDisplayName(server)} (shared)`,
          value: "logical",
          server: resourceName(server),
          description: `A database of its own on the shared ${server.spec?.engine === "MySQL" ? "MySQL" : "Postgres"} server`,
          disabled: condition(server) !== "True",
        })),
      ];
      return dbRows
        .filter((row) => matches(row.label))
        .map((row) => ({ ...row, icon: Database, onSelect: () => createDatabase({ value: row.value || row.key, ...row }) }));
    }
    if (step === "template") {
      return (templates.data?.items || [])
        .filter((template) => matches(template.name))
        .map((template) => ({
          key: template.id,
          label: template.name,
          description: template.resources.length === 1 ? "1 resource" : `${template.resources.length} resources`,
          icon: Layers,
          onSelect: () => deployTemplate(template),
        }));
    }
    if (step === "function") {
      return FUNCTION_RUNTIMES
        .filter((runtime) => matches(runtime.label))
        .map((runtime) => ({
          key: runtime.id,
          label: runtime.label,
          description: runtime.file,
          icon: SquareFunction,
          onSelect: () => createFunction(runtime.id),
        }));
    }
    if (step === "bucket") {
      return [
        {
          key: "cluster", label: "In-cluster bucket", icon: HardDrive, disabled: !objectStorageAvailable,
          description: clusterBucketDescription,
          onSelect: () => createBucket("cluster"),
        },
        {
          key: "s3", label: "AWS S3", icon: HardDrive, disabled: awsConnections.length === 0,
          description: awsConnections.length ? "In your AWS account" : "Add an AWS connection in platform settings",
          onSelect: () => createBucket("s3"),
        },
      ].filter((row) => matches(row.label));
    }
    if (step === "github") {
      return (repos.data?.items || [])
        .filter((repo) => matches(repo.FullName || ""))
        .slice(0, 50)
        .map((repo) => ({
          key: repo.FullName,
          label: repo.FullName,
          description: repo.Private ? "Private" : "Public",
          icon: GitBranch,
          onSelect: () => createGitService(repo.FullName, repo.DefaultBranch),
        }));
    }
    return [];
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [step, lower, query, parsedRepo?.repository, githubReady, githubLinked, objectStorageAvailable, objectStorageEngine, awsConnections.length, planetConnections.length, servers.length, sharedServers.length, repos.data, templates.data]);

  const goBack = () => {
    setText("");
    setActive(0);
    setImageError("");
    setStep("root");
  };

  const onKeyDown = (event) => {
    if (event.key === "ArrowDown") {
      event.preventDefault();
      setActive((value) => Math.min(rows.length - 1, value + 1));
    } else if (event.key === "ArrowUp") {
      event.preventDefault();
      setActive((value) => Math.max(0, value - 1));
    } else if (event.key === "Enter") {
      event.preventDefault();
      if (step === "image") {
        createDockerService(text);
        return;
      }
      const row = rows[Math.min(active, rows.length - 1)];
      if (row && !row.disabled) row.onSelect();
    } else if (event.key === "Backspace" && !text && step !== "root") {
      event.preventDefault();
      goBack();
    }
  };

  const imageValid = validateContainerImageReference(text).ok;
  const LeadingIcon = step === "root" ? Sparkles : ArrowLeft;

  return (
    <Modal
      opened={open}
      onClose={() => { setOpen(false); resetDialog(); }}
      withCloseButton={false}
      size={520}
      yOffset="22vh"
      classNames={{ content: "rw-palette", body: "rw-palette-body" }}
    >
      <div className={`rw-palette-input${step === "image" && imageError ? " is-invalid" : ""}${step === "image" && imageValid ? " is-valid" : ""}`}>
        {step === "root" ? (
          <LeadingIcon size={16} aria-hidden />
        ) : (
          <button type="button" className="rw-palette-back" onClick={goBack} aria-label="Back">
            <LeadingIcon size={16} />
          </button>
        )}
        <input
          ref={inputRef}
          value={text}
          onChange={(event) => { setText(event.currentTarget.value); setActive(0); if (imageError) setImageError(""); }}
          onKeyDown={onKeyDown}
          placeholder={STEP_PLACEHOLDER[step]}
          aria-label="Add a resource"
          autoComplete="off"
          spellCheck={false}
        />
      </div>

      {step === "image" ? (
        <div className="rw-palette-panel">
          {imageError ? (
            <p className="rw-palette-error">{imageError}</p>
          ) : (
            <p className="rw-palette-hint"><Info size={14} aria-hidden /> Enter a Docker image from a supported registry</p>
          )}
          <div className="rw-palette-examples">
            <strong>Examples</strong>
            <ul>
              {IMAGE_EXAMPLES.map((example) => (
                <li key={example}>
                  <button type="button" onClick={() => { setText(example); setImageError(""); inputRef.current?.focus(); }}>{example}</button>
                </li>
              ))}
            </ul>
          </div>
          <div className="rw-palette-actions">
            <Button loading={creating} disabled={!imageValid} onClick={() => createDockerService(text)}>Create</Button>
          </div>
        </div>
      ) : step === "github" && (!githubReady || !githubLinked) ? (
        <div className="rw-palette-panel">
          {!githubReady ? (
            <>
              <p className="rw-palette-hint"><Info size={14} aria-hidden /> GitHub is not connected to this cluster yet.</p>
              <div className="rw-palette-actions">
                <Button component={Link} to="/settings/connectors" onClick={() => setOpen(false)}>Open connectors</Button>
              </div>
            </>
          ) : (
            <>
              <p className="rw-palette-hint"><Info size={14} aria-hidden /> Link this project to your GitHub account to deploy a repository.</p>
              <div className="rw-palette-actions">
                <Button onClick={startGitHubInstall}>Connect GitHub</Button>
              </div>
            </>
          )}
        </div>
      ) : (
        <div className="rw-palette-list" role="listbox" aria-label="Resources">
          {step === "template" && templates.isPending ? <Muted size="sm" className="rw-palette-empty">Loading templates…</Muted> : null}
          {step === "github" && repos.isPending ? <Muted size="sm" className="rw-palette-empty">Loading repositories…</Muted> : null}
          {step === "github" && repos.isError ? <Muted size="sm" className="rw-palette-empty">{repos.error.message}</Muted> : null}
          {!rows.length && !(step === "github" && (repos.isPending || repos.isError)) ? (
            <Muted size="sm" className="rw-palette-empty">
              {step === "github" ? "No repositories match." : "Nothing matches that."}
            </Muted>
          ) : null}
          {rows.map((row, index) => {
            const Icon = row.icon;
            return (
              <button
                key={row.key}
                type="button"
                role="option"
                aria-selected={index === active}
                aria-disabled={row.disabled || undefined}
                disabled={row.disabled}
                className={`rw-palette-row${index === active ? " is-active" : ""}${row.disabled ? " is-disabled" : ""}`}
                onMouseEnter={() => setActive(index)}
                onClick={() => row.onSelect()}
              >
                <Icon size={16} aria-hidden />
                <span className="rw-palette-row-label">{row.label}</span>
                {row.description ? <span className="rw-palette-row-desc">{row.description}</span> : null}
                {row.chevron ? <span className="rw-palette-row-chevron" aria-hidden>›</span> : null}
              </button>
            );
          })}
        </div>
      )}
      <span className="rw-palette-scope"><Search size={11} aria-hidden /> Adding to {environment}</span>
    </Modal>
  );
}
