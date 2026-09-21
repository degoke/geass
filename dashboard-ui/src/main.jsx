import React, { useEffect, useMemo, useState } from "react";
import { createRoot } from "react-dom/client";
import "@fontsource-variable/geist";
import { QueryClient, QueryClientProvider, useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { createRootRoute, createRoute, createRouter, Link, Outlet, RouterProvider, useNavigate, useRouterState } from "@tanstack/react-router";
import { cn } from "@/lib/utils";
import { action, api, condition, list, resourceName } from "@/lib/api";
import { Button as ShadcnButton } from "@/components/ui/button";
import { Badge as ShadcnBadge } from "@/components/ui/badge";
import { Card as ShadcnCard } from "@/components/ui/card";
import { Input as ShadcnInput } from "@/components/ui/input";
import { Textarea as ShadcnTextarea } from "@/components/ui/textarea";
import { Label as ShadcnLabel } from "@/components/ui/label";
import { Select as ShadcnSelect, SelectContent, SelectItem, SelectTrigger, SelectValue } from "@/components/ui/select";
import { Separator as ShadcnSeparator } from "@/components/ui/separator";
import { Table as ShadcnTable, TableBody, TableHead, TableHeader, TableRow, TableCell } from "@/components/ui/table";
import { Dialog, DialogContent, DialogDescription, DialogFooter, DialogHeader, DialogTitle } from "@/components/ui/dialog";
import {
  Activity, ArrowLeft, ArrowRight, Box, Check, Cloud, Cpu, Database, FolderKanban, GitBranch,
  HardDrive, LayoutDashboard, Lock, Menu, Moon, Plus, RefreshCw, Search, Server, Settings,
  ShieldCheck, Sun, Terminal, Trash2, X,
} from "lucide-react";
import "./styles.css";

function Button({ variant = "default", ...props }) {
  return <ShadcnButton variant={variant === "danger" ? "destructive" : variant} {...props} />;
}
function Card({ className = "", ...props }) { return <ShadcnCard className={cn("ui-card", className)} {...props} />; }
function Badge({ children, tone = "neutral", className = "" }) {
  return <ShadcnBadge variant={tone === "danger" ? "destructive" : "outline"} className={cn("ui-badge", `ui-badge-${tone}`, className)}>{children}</ShadcnBadge>;
}
function Input({ className = "", ...props }) { return <ShadcnInput className={cn("ui-input", className)} {...props} />; }
function Select({ className = "", children, value, onChange, ...props }) {
  const options = React.Children.toArray(children).filter(Boolean).map((child) => ({ value: child.props.value ?? child.props.children, label: child.props.children, disabled: child.props.disabled }));
  const selected = options.find((option) => String(option.value) === String(value));
  return (
    <ShadcnSelect value={value} onValueChange={(next) => onChange?.({ target: { value: next } })} {...props}>
      <SelectTrigger className={cn("ui-input ui-select", className)}><SelectValue>{selected?.label ?? value}</SelectValue></SelectTrigger>
      <SelectContent>{options.map((option) => <SelectItem key={option.value} value={String(option.value)} disabled={option.disabled}>{option.label}</SelectItem>)}</SelectContent>
    </ShadcnSelect>
  );
}
function notify(message, tone = "neutral") { window.dispatchEvent(new CustomEvent("geass:notice", { detail: { message: String(message), tone } })); }
function alert(message) { const text = String(message); notify(text, /error|fail|unavailable|request failed|not found/i.test(text) ? "error" : "success"); }
function openResourceDialog() { window.dispatchEvent(new Event("geass:open-resource")); }
function Status({ value }) {
  const ready = value === "True" || value === "Ready" || value === "healthy" || value === true;
  return <Badge tone={ready ? "success" : value === "False" || value === "Failed" ? "danger" : "warning"}>{ready ? <Check size={13} /> : <Activity size={13} />}{String(value || "Pending")}</Badge>;
}
const PENDING_CHANGES_ANNOTATION = "geass.dev/pending-changes";
const PENDING_UPDATES_ANNOTATION = "geass.dev/pending-updates";
function isService(item) {
  return Boolean(item?.spec?.source);
}
function isDraftService(item) {
  return isService(item) && !item?.spec?.deploy?.enabled;
}
function pendingChangeKinds(item) {
  const raw = item?.metadata?.annotations?.[PENDING_CHANGES_ANNOTATION] || "";
  const kinds = raw.split(",").map((part) => part.trim()).filter(Boolean);
  const count = Number(item?.metadata?.annotations?.[PENDING_UPDATES_ANNOTATION] || 0);
  if (isDraftService(item) && !kinds.includes("created")) kinds.unshift("created");
  if (kinds.length) return kinds.filter((kind, index) => kinds.indexOf(kind) === index);
  if (count > 0) return ["settings"];
  return [];
}
function pendingChangeCopy(item) {
  const kinds = pendingChangeKinds(item);
  if (!kinds.length) return null;
  const action = item.spec?.deploy?.enabled ? "Deploy to update" : "Deploy";
  if (kinds.length === 1 && kinds[0] === "created") {
    return { title: "This service is a draft", detail: "Change settings or add variables, then deploy when you are ready.", action };
  }
  const labels = { created: "created as a draft", settings: "settings", variables: "variables" };
  const named = kinds.map((kind) => labels[kind] || kind);
  const list = named.length === 1 ? named[0][0].toUpperCase() + named[0].slice(1) : named.length === 2 ? `${named[0][0].toUpperCase() + named[0].slice(1)} and ${named[1]}` : `${named[0][0].toUpperCase() + named[0].slice(1)}, ${named.slice(1, -1).join(", ")}, and ${named[named.length - 1]}`;
  return { title: "You made these changes", detail: `${list}. Do you want to deploy?`, action };
}
function resourceStatus(item) {
  if (isDraftService(item)) return "Draft";
  return condition(item);
}
function PendingChangesBanner({ item, name, reload, data }) {
  const copy = pendingChangeCopy(item);
  if (!copy) return null;
  return (
    <div className="pending-banner" role="status">
      <div>
        <strong>{copy.title}</strong>
        <p>{copy.detail}</p>
      </div>
      {canMutate(data) && <Button onClick={() => action(`/apps/${name}/deploy`).then(() => { reload(); alert("Deploy requested"); }).catch((error) => alert(error.message))}>{copy.action}</Button>}
    </div>
  );
}
function PageHeader({ eyebrow, title, description, actions }) {
  return <div className="page-header"><div>{eyebrow && <div className="eyebrow">{eyebrow}</div>}<h1>{title}</h1>{description && <p>{description}</p>}</div><div className="page-actions">{actions}</div></div>;
}
function Empty({ icon: Icon = Box, title, description, action }) {
  return <Card className="empty-state"><Icon size={26} /><h3>{title}</h3><p>{description}</p>{action}</Card>;
}
function Field({ label, children }) { return <ShadcnLabel className="field-label">{label}{children}</ShadcnLabel>; }
function AppLink({ href, ...props }) { return <Link to={href} {...props} />; }
function Table({ headers, children }) {
  return <ShadcnTable className="table-scroll"><TableHeader><TableRow>{headers.map((header) => <TableHead key={header}>{header}</TableHead>)}</TableRow></TableHeader><TableBody>{React.Children.map(children, (row) => <TableRow>{React.Children.map(row?.props?.children, (cell) => <TableCell>{cell?.props?.children}</TableCell>)}</TableRow>)}</TableBody></ShadcnTable>;
}

function NoticeHost() {
  const [notices, setNotices] = useState([]);
  useEffect(() => {
    const onNotice = (event) => {
      const notice = { id: `${Date.now()}-${Math.random()}`, ...event.detail };
      setNotices((current) => [...current.slice(-2), notice]);
      window.setTimeout(() => setNotices((current) => current.filter((item) => item.id !== notice.id)), 4200);
    };
    window.addEventListener("geass:notice", onNotice);
    return () => window.removeEventListener("geass:notice", onNotice);
  }, []);
  return <div className="notice-stack" aria-live="polite">{notices.map((notice) => <div className={cn("notice", `notice-${notice.tone || "neutral"}`)} key={notice.id}><span>{notice.message}</span><button type="button" aria-label="Dismiss notification" onClick={() => setNotices((current) => current.filter((item) => item.id !== notice.id))}><X size={14} /></button></div>)}</div>;
}

function useBootstrap() {
  return useQuery({
    queryKey: ["dashboard", "bootstrap"],
    queryFn: () => api("/api/bootstrap"),
    staleTime: 4000,
    retry: (count, error) => error?.status !== 401 && count < 2,
    refetchInterval: (query) => (query.state.error?.status === 401 ? false : 8000),
  });
}
function dashboardSession(data) {
  return data?.platform?.session || { username: "", role: "viewer", canMutate: false };
}
function canMutate(data) {
  return Boolean(dashboardSession(data).canMutate);
}

function Sidebar({ project, path }) {
  const { data } = useBootstrap();
  const session = dashboardSession(data);
  const [open, setOpen] = useState(false);
  const [dark, setDark] = useState(localStorage.getItem("geass.theme") !== "light");
  useEffect(() => { document.documentElement.dataset.theme = dark ? "dark" : "light"; localStorage.setItem("geass.theme", dark ? "dark" : "light"); }, [dark]);
  const links = project
    ? [[`/projects/${project}`, "Workspace", LayoutDashboard], [`/projects/${project}/settings`, "Project settings", Settings]]
    : [["/projects", "Projects", FolderKanban]];
  const settings = [["/settings", "Settings", Settings], ["/cluster", "Cluster", Cpu], ["/ha-readiness", "HA readiness", ShieldCheck], ["/cloud-connections", "Cloud connections", Cloud], ["/object-storage", "Object storage", HardDrive]];
  const item = ([href, label, Icon]) => {
    const base = href.split("?")[0];
    const active = path === base || (base !== "/projects" && path.startsWith(base));
    return <AppLink key={href} href={href} onClick={() => setOpen(false)} className={cn("nav-link", active && "nav-link-active")}><Icon size={17} /><span>{label}</span></AppLink>;
  };
  return (
    <>
      <aside className={cn("app-sidebar", open && "app-sidebar-open")}>
        <div className="brand"><AppLink href="/projects" onClick={() => setOpen(false)}><span className="brand-mark">G</span><span>Geass</span></AppLink></div>
        <nav>{links.map(item)}</nav>
        <div className="sidebar-spacer" />
        <nav>{settings.map(item)}</nav>
        <ShadcnSeparator className="ui-separator" />
        {session.username && <div className="muted" style={{ padding: "0 12px 8px", fontSize: 12 }}>{session.username} · {session.role}</div>}
        <Button variant="ghost" className="theme-button" onClick={() => setDark(!dark)}>{dark ? <Sun size={17} /> : <Moon size={17} />}<span>{dark ? "Light mode" : "Dark mode"}</span></Button>
        <Button variant="ghost" className="theme-button" onClick={() => action("/logout").then(() => window.location.reload()).catch((error) => alert(error.message))}><Lock size={17} /><span>Sign out</span></Button>
      </aside>
      <Button variant="outline" size="icon" className="mobile-nav" onClick={() => setOpen(!open)}>{open ? <X size={18} /> : <Menu size={18} />}</Button>
    </>
  );
}

function Layout({ children, title = "Dashboard", project, environment, setEnvironment }) {
  const path = useRouterState({ select: (state) => state.location.pathname });
  const projectName = project?.spec?.displayName || project?.metadata?.name;
  return (
    <div className="app-shell">
      <Sidebar project={project?.metadata?.name} path={path} />
      <div className="main-panel">
        <header className="topbar">
          <div className="topbar-title"><span className="topbar-muted">Geass</span><span>/</span><strong>{projectName || title}</strong></div>
          {project && setEnvironment && (
            <Select value={environment || ""} onChange={(event) => setEnvironment(event.target.value)} aria-label="Environment">
              {(project.spec?.environments || []).map((env) => <option key={env}>{env}</option>)}
            </Select>
          )}
        </header>
        <main className="main-content">{children}</main>
      </div>
    </div>
  );
}

function projectResources(data, projectName, environment) {
  const match = (item) => item.spec?.project === projectName && (!environment || item.spec?.environment === environment);
  return {
    apps: list(data, "apps").filter(match),
    databases: list(data, "databases").filter(match),
    logical: list(data, "logicalDatabases").filter(match),
    caches: list(data, "caches").filter(match),
    stores: list(data, "objectStores").filter(match),
  };
}

function availableConnections(data, provider) {
  return list(data, "cloudConnections").filter((item) => item.spec?.provider === provider && item.status?.available && !item.spec?.project);
}

function Projects({ data }) {
  const navigate = useNavigate();
  const queryClient = useQueryClient();
  const [query, setQuery] = useState("");
  const create = useMutation({
    mutationFn: () => action("/projects/create"),
    onSuccess: (result) => { queryClient.invalidateQueries({ queryKey: ["dashboard", "bootstrap"] }); navigate({ to: result.project ? `/projects/${result.project}` : "/projects" }); },
    onError: (error) => alert(error.message),
  });
  const projects = list(data, "projects").filter((project) => {
    const haystack = `${project.spec?.displayName || ""} ${resourceName(project)}`.toLowerCase();
    return haystack.includes(query.toLowerCase());
  });
  return (
    <>
      <PageHeader title="Projects" actions={canMutate(data) ? <Button onClick={() => create.mutate()} disabled={create.isPending}><Plus size={16} /> New project</Button> : null} />
      <div className="projects-toolbar"><div className="search-wrap"><Search size={16} /><Input placeholder="Search projects" value={query} onChange={(event) => setQuery(event.target.value)} /></div></div>
      {projects.length ? (
        <div className="project-card-grid">
          {projects.map((project) => {
            const name = resourceName(project);
            const environments = project.spec?.environments || [];
            const resources = projectResources(data, name);
            const total = resources.apps.length + resources.databases.length + resources.stores.length + resources.caches.length;
            return (
              <AppLink key={name} href={`/projects/${name}`} className="project-card">
                <div className="project-card-header">
                  <div className="project-card-title-block">
                    <span className="project-card-kicker">Project</span>
                    <strong className="project-card-title">{project.spec?.displayName || name}</strong>
                    <span className="project-card-name">{name}</span>
                  </div>
                  <Status value={condition(project)} />
                </div>
                <div className="project-card-meta">
                  <div><span>Environments</span><strong>{environments.join(" · ") || "—"}</strong></div>
                  <div><span>Resources</span><strong>{total}</strong></div>
                </div>
              </AppLink>
            );
          })}
        </div>
      ) : <Empty icon={FolderKanban} title="Create your first project" description="Projects keep environments, services, databases, and buckets isolated." action={canMutate(data) ? <Button onClick={() => create.mutate()} disabled={create.isPending}><Plus size={15} /> Create project</Button> : null} />}
    </>
  );
}

function ResourceRow({ href, icon: Icon, item, kind }) {
  return (
    <AppLink className="resource-row" href={href}>
      <span className="resource-row-icon"><Icon size={16} /></span>
      <div><strong>{resourceName(item)}</strong><small>{kind} · {item.spec?.environment}</small></div>
      <Status value={resourceStatus(item)} />
      <ArrowRight size={15} />
    </AppLink>
  );
}

function Workspace({ project, data, environment }) {
  const name = resourceName(project);
  const resources = projectResources(data, name, environment);
  const total = resources.apps.length + resources.databases.length + resources.logical.length + resources.caches.length + resources.stores.length;
  return (
    <>
      <PageHeader
        eyebrow="Project"
        title={project.spec?.displayName || name}
        description={`${environment} environment. Add a service, database, or bucket, then monitor and change it from its page.`}
        actions={<><AppLink href={`/projects/${name}/settings`}><Button variant="outline"><Settings size={16} /> Project settings</Button></AppLink>{canMutate(data) && <Button onClick={openResourceDialog}><Plus size={16} /> Add resource</Button>}</>}
      />
      {total ? (
        <div className="stack-lg">
          <section className="resource-section">
            <div className="section-heading"><h2>Services</h2><p>GitHub repositories and Docker images.</p></div>
            {resources.apps.length ? resources.apps.map((item) => <ResourceRow key={resourceName(item)} item={item} kind={item.spec?.source?.git ? "GitHub" : "Docker image"} icon={item.spec?.source?.git ? GitBranch : Box} href={`/projects/${name}/apps/${resourceName(item)}?environment=${environment}`} />) : <p className="muted">No services in {environment}.</p>}
          </section>
          <section className="resource-section">
            <div className="section-heading"><h2>Databases</h2><p>In-cluster, PlanetScale, AWS, and logical databases.</p></div>
            {[...resources.databases.map((item) => [item, "databases", item.spec?.engine || "Database"]), ...resources.caches.map((item) => [item, "caches", "Redis"]), ...resources.logical.map((item) => [item, "logical-databases", "Logical"])].map(([item, kind, label]) => (
              <ResourceRow key={`${kind}-${resourceName(item)}`} item={item} kind={label} icon={Database} href={`/projects/${name}/${kind}/${resourceName(item)}?environment=${environment}`} />
            ))}
            {!resources.databases.length && !resources.caches.length && !resources.logical.length && <p className="muted">No databases in {environment}.</p>}
          </section>
          <section className="resource-section">
            <div className="section-heading"><h2>Buckets</h2><p>In-cluster object storage and AWS S3.</p></div>
            {resources.stores.length ? resources.stores.map((item) => <ResourceRow key={resourceName(item)} item={item} kind={item.spec?.engine || "Bucket"} icon={HardDrive} href={`/projects/${name}/object-stores/${resourceName(item)}?environment=${environment}`} />) : <p className="muted">No buckets in {environment}.</p>}
          </section>
        </div>
      ) : (
        <Empty icon={Box} title="Add the first resource" description="Create a service from GitHub or a Docker image, then add a database or bucket." action={canMutate(data) ? <Button onClick={openResourceDialog}>Add the first resource</Button> : null} />
      )}
      <ResourceDialog project={name} environment={environment} data={data} />
    </>
  );
}

function workloadKind(type, kind) {
  if (type === "service") return "service";
  if (kind === "logical") return "logical";
  if (kind === "planetscale" || kind === "awsdb" || kind === "s3") return "external";
  if (kind === "minio") return "bucket";
  return kind;
}

function defaultSizeForKind(type, kind) {
  if (kind === "sqlite" || type === "service") return { cpu: "100m", memory: "128Mi" };
  if (["postgres", "mysql", "redis", "minio"].includes(kind) || type === "database") return { cpu: "250m", memory: "512Mi" };
  return { cpu: "100m", memory: "128Mi" };
}

function cpuMillis(value) {
  const text = String(value || "").trim();
  if (!text) return 0;
  if (text.endsWith("m")) return parseInt(text, 10) || 0;
  const cores = parseFloat(text);
  return Number.isFinite(cores) ? Math.round(cores * 1000) : 0;
}

function memoryBytes(value) {
  const text = String(value || "").trim();
  if (text.endsWith("Gi")) return (parseInt(text, 10) || 0) * 1024 ** 3;
  if (text.endsWith("Mi")) return (parseInt(text, 10) || 0) * 1024 ** 2;
  if (text.endsWith("Ki")) return (parseInt(text, 10) || 0) * 1024;
  return parseInt(text, 10) || 0;
}

function sizeLabel(options, value, fallback) {
  return options.find((option) => option.value === value)?.label || fallback || value;
}

function resourceEstimate(data, type, kind, ha) {
  const estimates = data?.platform?.capacity?.estimates || {};
  const key = ha && (kind === "postgres" || kind === "mysql" || kind === "redis") ? `${kind}-ha` : workloadKind(type, kind);
  return estimates[key] || estimates[workloadKind(type, kind)];
}

function liveEstimate(data, type, kind, form) {
  const catalog = resourceEstimate(data, type, kind, form.highAvailability);
  if (!catalog || (!catalog.cpuMillis && !catalog.memoryBytes)) return catalog;
  const copies = type === "service"
    ? (form.autoscaling ? Math.max(Number(form.maxReplicas) || 3, Number(form.replicas) || 1) : Number(form.replicas) || 1)
    : (catalog.replicas || 1);
  const cpu = form.cpu || catalog.perCpu;
  const memory = form.memory || catalog.perMemory;
  return {
    ...catalog,
    cpu,
    memory,
    replicas: copies,
    cpuMillis: cpuMillis(cpu) * copies,
    memoryBytes: memoryBytes(memory) * copies,
    perCpuMillis: cpuMillis(cpu),
    perMemoryBytes: memoryBytes(memory),
  };
}

function capacityFits(capacity, estimate) {
  if (!estimate) return true;
  if (!estimate.cpuMillis && !estimate.memoryBytes) return true;
  if (!capacity?.known) return false;
  if (estimate.perCpuMillis > capacity.largestNodeCpuMillis || estimate.perMemoryBytes > capacity.largestNodeMemoryBytes) return false;
  return estimate.cpuMillis <= capacity.cpuAvailableMillis && estimate.memoryBytes <= capacity.memoryAvailableBytes;
}

function SizeFields({ cpu, memory, onChange, data }) {
  const cpuSizes = data?.platform?.capacity?.cpuSizes || [{ value: "100m", label: "0.1 CPU" }, { value: "250m", label: "0.25 CPU" }, { value: "500m", label: "0.5 CPU" }, { value: "1", label: "1 CPU" }, { value: "2", label: "2 CPU" }];
  const memorySizes = data?.platform?.capacity?.memorySizes || [{ value: "128Mi", label: "128 MB" }, { value: "256Mi", label: "256 MB" }, { value: "512Mi", label: "512 MB" }, { value: "1Gi", label: "1 GB" }, { value: "2Gi", label: "2 GB" }, { value: "4Gi", label: "4 GB" }];
  return (
    <div className="size-grid">
      <Field label="CPU"><Select value={cpu} onChange={(event) => onChange("cpu", event.target.value)}>{cpuSizes.map((option) => <option key={option.value} value={option.value}>{option.label}</option>)}</Select></Field>
      <Field label="Memory"><Select value={memory} onChange={(event) => onChange("memory", event.target.value)}>{memorySizes.map((option) => <option key={option.value} value={option.value}>{option.label}</option>)}</Select></Field>
    </div>
  );
}

function ResourceDialog({ project, environment, data }) {
  const queryClient = useQueryClient();
  const navigate = useNavigate();
  const platform = data?.platform || {};
  const currentProject = list(data, "projects").find((item) => resourceName(item) === project);
  const githubReady = Boolean(currentProject?.spec?.githubConnectionRef?.name) && platform.hasGitHubApp;
  const servers = list(data, "databases").filter((item) => item.spec?.project === project && item.spec?.environment === environment && (item.spec?.engine === "Postgres" || item.spec?.engine === "MySQL" || !item.spec?.engine));
  const awsConnections = availableConnections(data, "AWS");
  const planetConnections = availableConnections(data, "PlanetScale");
  const minioAvailable = Boolean(platform.minioAvailable);
  const [open, setOpen] = useState(false);
  const [step, setStep] = useState(1);
  const [type, setType] = useState("service");
  const [kind, setKind] = useState("app");
  const [form, setForm] = useState({ name: "", image: "nginx:alpine", repository: "", branch: "main", engine: "Postgres", placement: "InCluster", provider: "", mode: "Create", host: "", username: "", password: "", databaseName: "", server: "", bucket: "", connectionRef: "", highAvailability: false, createBucket: true, cpu: "100m", memory: "128Mi", replicas: 1, autoscaling: false, maxReplicas: 3, targetCPU: 50 });
  const set = (key, value) => setForm((current) => ({ ...current, [key]: value }));
  const emptyForm = { name: "", image: "nginx:alpine", repository: "", branch: "main", engine: "Postgres", placement: "InCluster", provider: "", mode: "Create", host: "", username: "", password: "", databaseName: "", server: "", bucket: "", connectionRef: "", highAvailability: false, createBucket: true, cpu: "100m", memory: "128Mi", replicas: 1, autoscaling: false, maxReplicas: 3, targetCPU: 50 };
  useEffect(() => {
    const openDialog = () => { setStep(1); setType("service"); setKind("app"); setForm(emptyForm); setOpen(true); };
    window.addEventListener("geass:open-resource", openDialog);
    return () => window.removeEventListener("geass:open-resource", openDialog);
  }, []);
  const types = [
    { value: "service", label: "Service", description: "GitHub repository or Docker image" },
    { value: "database", label: "Database", description: "Postgres, MySQL, SQLite, Redis, PlanetScale, AWS" },
    { value: "bucket", label: "Bucket", description: "In-cluster or AWS S3" },
  ];
  const options = {
    service: [
      { value: "app", label: "Docker image", description: "Pull and run a container image", enabled: true },
      { value: "github", label: "GitHub repository", description: githubReady ? "Build from GitHub, then deploy when you are ready" : "Connect GitHub in project settings first", enabled: githubReady },
    ],
    database: [
      { value: "postgres", label: "PostgreSQL", description: "In-cluster Postgres", enabled: true, engine: "Postgres", placement: "InCluster" },
      { value: "mysql", label: "MySQL", description: "In-cluster MySQL", enabled: true, engine: "MySQL", placement: "InCluster" },
      { value: "sqlite", label: "SQLite", description: "In-cluster SQLite", enabled: true, engine: "SQLite", placement: "InCluster" },
      { value: "redis", label: "Redis", description: "In-cluster Redis", enabled: true, engine: "Redis", placement: "InCluster" },
      { value: "planetscale", label: "PlanetScale", description: planetConnections.length ? "Create or connect a PlanetScale database" : "Add a PlanetScale connection in cluster settings", enabled: planetConnections.length > 0, engine: "MySQL", placement: "External", provider: "PlanetScale" },
      { value: "awsdb", label: "AWS", description: awsConnections.length ? "Connect an AWS database" : "Add an AWS connection in cluster settings", enabled: awsConnections.length > 0, engine: "Postgres", placement: "External", provider: "AWS" },
      { value: "logical", label: "Logical database", description: servers.length ? "Create a database inside an existing server" : "Create a database server first", enabled: servers.length > 0 },
    ],
    bucket: [
      { value: "minio", label: "In-cluster bucket", description: minioAvailable ? "Create a bucket on the cluster MinIO server" : "Set up the MinIO server in cluster settings first", enabled: minioAvailable, placement: "InCluster" },
      { value: "s3", label: "AWS S3", description: awsConnections.length ? "Create or connect an S3 bucket" : "Add an AWS connection in cluster settings", enabled: awsConnections.length > 0, placement: "External" },
    ],
  };
  const submit = (event) => {
    event.preventDefault();
    const values = { project, environment, name: form.name };
    let endpoint = "/apps/create";
    if (type === "service" && kind === "app") {
      endpoint = "/apps/create";
      Object.assign(values, { image: form.image, port: "80", name: form.name, cpu: form.cpu, memory: form.memory, replicas: String(form.replicas || 1), autoscaling: form.autoscaling ? "on" : "", maxReplicas: String(form.maxReplicas || 3), targetCPU: String(form.targetCPU || 50) });
    } else if (type === "service" && kind === "github") {
      endpoint = "/apps/create";
      Object.assign(values, { source: "git", repository: form.repository, branch: form.branch || "main", name: form.name, cpu: form.cpu, memory: form.memory, replicas: String(form.replicas || 1), autoscaling: form.autoscaling ? "on" : "", maxReplicas: String(form.maxReplicas || 3), targetCPU: String(form.targetCPU || 50) });
    } else if (kind === "logical") {
      endpoint = "/logical-databases/create";
      Object.assign(values, { server: form.server || (servers[0] && resourceName(servers[0])), database: form.databaseName || form.name });
    } else if (type === "database") {
      endpoint = "/databases/create";
      const option = options.database.find((item) => item.value === kind);
      Object.assign(values, {
        engine: form.engine || option?.engine || "Postgres",
        placement: option?.placement || "InCluster",
        provider: option?.provider || "",
        mode: form.mode,
        host: form.host,
        username: form.username,
        password: form.password,
        databaseName: form.databaseName,
        connectionRef: form.connectionRef || (option?.provider === "PlanetScale" ? planetConnections[0] && resourceName(planetConnections[0]) : option?.provider === "AWS" ? awsConnections[0] && resourceName(awsConnections[0]) : ""),
        highAvailability: form.highAvailability ? "on" : "",
        cpu: form.cpu,
        memory: form.memory,
      });
    } else {
      endpoint = "/object-stores/create";
      Object.assign(values, {
        engine: kind === "s3" ? "S3" : "MinIO",
        placement: kind === "s3" ? "External" : "InCluster",
        bucket: form.bucket || form.name,
        createBucket: form.createBucket ? "on" : "",
        connectionRef: kind === "s3" ? (form.connectionRef || (awsConnections[0] && resourceName(awsConnections[0]))) : "",
      });
    }
    action(endpoint, values).then(() => {
      queryClient.invalidateQueries({ queryKey: ["dashboard", "bootstrap"] });
      setOpen(false);
      if (type === "service" && values.name) {
        navigate({ to: `/projects/${project}/apps/${values.name}?environment=${environment}` });
        alert("Service created as a draft. Change settings or variables, then deploy.");
        return;
      }
      alert("Resource created");
    }).catch((error) => alert(error.message));
  };
  const haDisabled = !platform.haReady;
  const selectedKind = options[type]?.find((item) => item.value === kind);
  const estimate = liveEstimate(data, type, kind, form);
  const capacity = platform.capacity || {};
  const fits = capacityFits(capacity, estimate);
  return (
    <Dialog open={open} onOpenChange={(next) => { setOpen(next); if (next) setStep(1); }}>
      <DialogContent className="ui-dialog resource-dialog">
        <DialogHeader>
          <DialogTitle>{step === 1 ? "Add a resource" : step === 2 ? "Choose a source" : type === "service" ? "Create a draft service" : "Configure resource"}</DialogTitle>
          <DialogDescription>{type === "service" && step === 3 ? "Creating saves a skeleton. You can change settings and variables before you deploy." : `Resources are created in ${environment} and reconciled by the Geass controllers.`}</DialogDescription>
        </DialogHeader>
        <form className="stack" onSubmit={step < 3 ? (event) => { event.preventDefault(); setStep(step + 1); } : submit}>
          {step === 1 && types.map((option) => <button type="button" key={option.value} className={cn("choice", type === option.value && "choice-selected")} onClick={() => { setType(option.value); const nextKind = options[option.value].find((item) => item.enabled)?.value || options[option.value][0].value; setKind(nextKind); setForm((current) => ({ ...current, ...defaultSizeForKind(option.value, nextKind) })); }}><strong>{option.label}</strong><small>{option.description}</small></button>)}
          {step === 2 && options[type].map((option) => <button type="button" key={option.value} disabled={!option.enabled} className={cn("choice", kind === option.value && "choice-selected")} onClick={() => { if (option.enabled) { setKind(option.value); setForm((current) => ({ ...current, ...defaultSizeForKind(type, option.value), ...(option.engine ? { engine: option.engine } : {}), ...(option.placement ? { placement: option.placement } : {}), ...(option.provider ? { provider: option.provider } : {}) })); } }}><strong>{option.label}</strong><small>{option.description}</small></button>)}
          {step === 3 && (
            <div className="stack">
              <Field label="Name"><Input required value={form.name} onChange={(event) => set("name", event.target.value)} placeholder="api" /></Field>
              {kind === "app" && <Field label="Container image"><Input required value={form.image} onChange={(event) => set("image", event.target.value)} /></Field>}
              {kind === "github" && <>
                <Field label="Repository"><Input required value={form.repository} onChange={(event) => set("repository", event.target.value)} placeholder="org/app" /></Field>
                <Field label="Branch"><Input value={form.branch} onChange={(event) => set("branch", event.target.value)} /></Field>
              </>}
              {type === "database" && kind !== "logical" && kind !== "planetscale" && kind !== "awsdb" && kind !== "sqlite" && (
                <label className={cn("check-row", haDisabled && "is-disabled")}><input type="checkbox" disabled={haDisabled} checked={form.highAvailability} onChange={(event) => set("highAvailability", event.target.checked)} /> High availability {haDisabled && <small>(requires 3 healthy cluster nodes)</small>}</label>
              )}
              {(type === "service" || (type === "database" && kind !== "logical" && kind !== "planetscale" && kind !== "awsdb")) && (
                <>
                  <SizeFields cpu={form.cpu} memory={form.memory} onChange={set} data={data} />
                  {type === "service" && (
                    <>
                      <Field label="Replicas"><Input type="number" min="1" value={form.replicas} onChange={(event) => set("replicas", Number(event.target.value) || 1)} /></Field>
                      <label className="check-row"><input type="checkbox" checked={form.autoscaling} onChange={(event) => set("autoscaling", event.target.checked)} /> Autoscaling</label>
                      {form.autoscaling && <>
                        <Field label="Scale up to"><Input type="number" min={form.replicas || 1} value={form.maxReplicas} onChange={(event) => set("maxReplicas", Number(event.target.value) || 3)} /></Field>
                        <Field label="CPU target %"><Input type="number" min="1" max="100" value={form.targetCPU} onChange={(event) => set("targetCPU", Number(event.target.value) || 50)} /></Field>
                      </>}
                    </>
                  )}
                </>
              )}
              {kind === "planetscale" && <>
                <Field label="Mode"><Select value={form.mode} onChange={(event) => set("mode", event.target.value)}><option>Create</option><option>Connect</option></Select></Field>
                {form.mode === "Connect" && <>
                  <Field label="Host"><Input value={form.host} onChange={(event) => set("host", event.target.value)} /></Field>
                  <Field label="Username"><Input value={form.username} onChange={(event) => set("username", event.target.value)} /></Field>
                  <Field label="Password"><Input type="password" value={form.password} onChange={(event) => set("password", event.target.value)} /></Field>
                </>}
                <Field label="Database name"><Input value={form.databaseName} onChange={(event) => set("databaseName", event.target.value)} placeholder="app" /></Field>
                {planetConnections.length > 0 && <Field label="PlanetScale connection"><Select value={form.connectionRef || resourceName(planetConnections[0])} onChange={(event) => set("connectionRef", event.target.value)}>{planetConnections.map((item) => <option key={resourceName(item)}>{resourceName(item)}</option>)}</Select></Field>}
              </>}
              {kind === "awsdb" && <>
                <Field label="Engine"><Select value={form.engine} onChange={(event) => set("engine", event.target.value)}><option>Postgres</option><option>MySQL</option></Select></Field>
                <Field label="Host"><Input required value={form.host} onChange={(event) => set("host", event.target.value)} /></Field>
                <Field label="Username"><Input required value={form.username} onChange={(event) => set("username", event.target.value)} /></Field>
                <Field label="Password"><Input type="password" required value={form.password} onChange={(event) => set("password", event.target.value)} /></Field>
                <Field label="Database name"><Input value={form.databaseName} onChange={(event) => set("databaseName", event.target.value)} /></Field>
                {awsConnections.length > 0 && <Field label="AWS connection"><Select value={form.connectionRef || resourceName(awsConnections[0])} onChange={(event) => set("connectionRef", event.target.value)}>{awsConnections.map((item) => <option key={resourceName(item)}>{resourceName(item)}</option>)}</Select></Field>}
              </>}
              {kind === "logical" && <>
                <Field label="Server"><Select value={form.server || (servers[0] && resourceName(servers[0]))} onChange={(event) => set("server", event.target.value)}>{servers.map((item) => <option key={resourceName(item)}>{resourceName(item)}</option>)}</Select></Field>
                <Field label="Database name"><Input required value={form.databaseName} onChange={(event) => set("databaseName", event.target.value)} /></Field>
              </>}
              {kind === "minio" && <Field label="Bucket name"><Input value={form.bucket} onChange={(event) => set("bucket", event.target.value)} placeholder={form.name || "uploads"} /></Field>}
              {kind === "s3" && <>
                <Field label="Bucket name"><Input value={form.bucket} onChange={(event) => set("bucket", event.target.value)} placeholder={form.name || "uploads"} /></Field>
                {awsConnections.length > 0 && <Field label="AWS connection"><Select value={form.connectionRef || resourceName(awsConnections[0])} onChange={(event) => set("connectionRef", event.target.value)}>{awsConnections.map((item) => <option key={resourceName(item)}>{resourceName(item)}</option>)}</Select></Field>}
              </>}
              {estimate && (
                <p className={cn("form-help", !fits && "text-danger")}>
                  {estimate.cpuMillis || estimate.memoryBytes
                    ? `Each copy gets ${sizeLabel(data?.platform?.capacity?.cpuSizes || [], form.cpu, form.cpu)} and ${sizeLabel(data?.platform?.capacity?.memorySizes || [], form.memory, form.memory)}${estimate.replicas > 1 ? ` · ${estimate.replicas} copies` : ""}. Cluster has ${capacity.known ? `${capacity.cpuAvailable} CPU and ${capacity.memoryAvailable} memory` : "unknown capacity"} available.`
                    : "This resource does not consume in-cluster CPU or memory."}
                  {type === "service"
                    ? !fits && <> Scale up the cluster before deploying. <AppLink href="/cluster">View cluster capacity</AppLink></>
                    : !fits && <> Scale up the cluster before creating it. <AppLink href="/cluster">View cluster capacity</AppLink></>}
                </p>
              )}
            </div>
          )}
          <DialogFooter>
            {step > 1 && <Button type="button" variant="outline" onClick={() => setStep(step - 1)}><ArrowLeft size={15} /> Back</Button>}
            <Button type="submit" disabled={(step === 2 && selectedKind && !selectedKind.enabled) || (step === 3 && type !== "service" && !fits)}>{step < 3 ? <>Continue <ArrowRight size={15} /></> : type === "service" ? "Create service" : "Create resource"}</Button>
          </DialogFooter>
        </form>
      </DialogContent>
    </Dialog>
  );
}

function ResourceSettings({ item, kind, name, title, data, project, reload, navigate }) {
  const defaults = defaultSizeForKind(kind === "apps" ? "service" : "database", (item.spec?.engine || "postgres").toLowerCase());
  const [cpu, setCpu] = useState(item.spec?.resources?.requests?.cpu || defaults.cpu);
  const [memory, setMemory] = useState(item.spec?.resources?.requests?.memory || defaults.memory);
  const [replicas, setReplicas] = useState(item.spec?.replicas || 1);
  const [autoscaling, setAutoscaling] = useState(Boolean(item.spec?.autoscaling?.maxReplicas > 1));
  const [maxReplicas, setMaxReplicas] = useState(item.spec?.autoscaling?.maxReplicas || 3);
  const [targetCPU, setTargetCPU] = useState(item.spec?.autoscaling?.targetCPUUtilization || 50);
  const [version, setVersion] = useState(item.spec?.version || "");
  const [confirmDelete, setConfirmDelete] = useState("");
  const inCluster = kind === "apps" || (kind === "databases" && item.spec?.placement !== "External");
  if (!canMutate(data)) {
    return <Card><p className="muted">Your viewer role can inspect this resource but cannot change it.</p></Card>;
  }
  const save = (event) => {
    event.preventDefault();
    const values = { project: item.spec?.project, environment: item.spec?.environment };
    if (kind === "databases") values.version = version;
    if (kind === "apps") Object.assign(values, { cpu, memory, replicas: String(replicas), autoscaling: autoscaling ? "on" : "", maxReplicas: String(maxReplicas), targetCPU: String(targetCPU) });
    if (kind === "databases" && inCluster) Object.assign(values, { cpu, memory });
    action(`/${kind}/${name}/update`, values).then(() => { reload(); alert(kind === "apps" ? "Settings saved. Deploy to apply them." : "Settings saved"); }).catch((error) => alert(error.message));
  };
  return (
    <Card>
      <form className="form-grid" onSubmit={save}>
        <Field label="Project"><Input readOnly value={item.spec?.project || ""} /></Field>
        <Field label="Environment"><Input readOnly value={item.spec?.environment || ""} /></Field>
        {kind === "databases" && <Field label="Version"><Input value={version} onChange={(event) => setVersion(event.target.value)} /></Field>}
        {inCluster && <SizeFields cpu={cpu} memory={memory} onChange={(key, value) => { if (key === "cpu") setCpu(value); else setMemory(value); }} data={data} />}
        {kind === "apps" && (
          <>
            <Field label="Replicas"><Input type="number" min="1" value={replicas} onChange={(event) => setReplicas(Number(event.target.value) || 1)} /></Field>
            <label className="check-row"><input type="checkbox" checked={autoscaling} onChange={(event) => setAutoscaling(event.target.checked)} /> Autoscaling</label>
            {autoscaling && <>
              <Field label="Scale up to"><Input type="number" min={replicas || 1} value={maxReplicas} onChange={(event) => setMaxReplicas(Number(event.target.value) || 3)} /></Field>
              <Field label="CPU target %"><Input type="number" min="1" max="100" value={targetCPU} onChange={(event) => setTargetCPU(Number(event.target.value) || 50)} /></Field>
            </>}
          </>
        )}
        <div className="form-actions">
          <Button type="submit">Save changes</Button>
        </div>
      </form>
      <div className="danger-zone">
        <Field label={`Type ${title} to delete`}>
          <Input value={confirmDelete} onChange={(event) => setConfirmDelete(event.target.value)} autoComplete="off" placeholder={title} />
        </Field>
        <Button type="button" variant="danger" disabled={confirmDelete !== title} onClick={() => action(`/${kind}/${name}/delete`, { confirmName: confirmDelete }).then(() => navigate({ to: `/projects/${resourceName(project)}` })).catch((error) => alert(error.message))}><Trash2 size={15} /> Delete</Button>
      </div>
    </Card>
  );
}

function ResourceDetail({ project, data, kind, name, reload }) {
  const navigate = useNavigate();
  const search = useRouterState({ select: (state) => state.location.search || {} });
  const view = search.view || "overview";
  const key = { apps: "apps", databases: "databases", caches: "caches", "object-stores": "objectStores", "logical-databases": "logicalDatabases" }[kind];
  const item = list(data, key).find((entry) => resourceName(entry) === name);
  const [query, setQuery] = useState("SELECT 1");
  const [output, setOutput] = useState("");
  const [variable, setVariable] = useState({ key: "", value: "", secret: false });
  const runtime = useQuery({ queryKey: ["runtime", kind, name], queryFn: () => api(`/api/apps/${name}/runtime`), enabled: kind === "apps" && view === "console" });
  const logs = useQuery({ queryKey: ["logs", name], queryFn: () => api(`/api/apps/${name}/logs`), enabled: kind === "apps" && view === "logs" });
  if (!item) return <Empty title="Resource not found" description="It may still be reconciling." action={<Button onClick={() => navigate({ to: `/projects/${resourceName(project)}` })}>Back to workspace</Button>} />;
  const href = (next) => `/projects/${resourceName(project)}/${kind}/${name}?view=${next}`;
  const title = resourceName(item);
  const isApp = kind === "apps";
  const isDatabase = kind === "databases" || kind === "caches" || kind === "logical-databases";
  const tabs = ["overview", ...(isApp ? [...(canMutate(data) ? ["logs"] : []), "metrics", "deployments", "variables", "console"] : []), ...(isDatabase ? ["console", "monitor"] : []), "settings"];
  const sourceImage = typeof item.spec?.source?.image === "string" ? item.spec.source.image : item.spec?.source?.image?.image;
  const deployments = list(data, "deployments").filter((entry) => entry.spec?.app === name);
  const shared = (project.spec?.sharedVariables || []).filter((entry) => entry.environment === item.spec?.environment);
  const saveVariable = (event) => {
    event.preventDefault();
    const path = variable.secret ? `/apps/${name}/secrets/set` : `/apps/${name}/config/set`;
    action(path, variable).then(() => { reload(); alert("Variable saved. Deploy to apply it."); setVariable({ key: "", value: "", secret: false }); }).catch((error) => alert(error.message));
  };
  const attachShared = (event) => {
    event.preventDefault();
    const selected = Array.from(event.target.querySelectorAll("input[name=sharedVariable]:checked")).map((input) => input.value);
    action(`/apps/${name}/shared-variables/save`, { sharedVariable: selected }).then(() => { reload(); alert("Shared variables saved. Deploy to apply them."); }).catch((error) => alert(error.message));
  };
  return (
    <>
      <div className="breadcrumb"><AppLink href={`/projects/${resourceName(project)}`}><ArrowLeft size={15} /> {project.spec?.displayName || resourceName(project)}</AppLink><span>/</span><strong>{title}</strong></div>
      <PageHeader
        eyebrow={isApp ? "Service" : isDatabase ? "Database" : "Bucket"}
        title={title}
        description={`${item.spec?.engine || sourceImage || item.spec?.source?.git?.repository || "Managed resource"} in ${item.spec?.environment}.`}
        actions={<>
          <Status value={resourceStatus(item)} />
          {isApp && !pendingChangeCopy(item) && item.spec?.deploy?.enabled && canMutate(data) && <Button variant="outline" onClick={() => action(`/apps/${name}/deploy`).then(() => { reload(); alert("Redeploy requested"); }).catch((error) => alert(error.message))}>Redeploy</Button>}
        </>}
      />
      {isApp && <PendingChangesBanner item={item} name={name} reload={reload} data={data} />}
      <div className="tabs">{tabs.map((tab) => <AppLink key={tab} className={view === tab ? "tab-active" : ""} href={href(tab)}>{tab[0].toUpperCase() + tab.slice(1)}</AppLink>)}</div>
      {view === "overview" && (
        <div className="detail-grid">
          <Card>
            <div className="card-heading"><div><div className="eyebrow">Status</div><h2>Runtime overview</h2></div><Status value={resourceStatus(item)} /></div>
            <div className="detail-list">
              <div className="detail-row"><span>Project</span><strong>{item.spec?.project}</strong></div>
              <div className="detail-row"><span>Environment</span><strong>{item.spec?.environment}</strong></div>
              <div className="detail-row"><span>Source</span><strong>{item.spec?.source?.git?.repository || sourceImage || item.spec?.engine || item.spec?.placement || "—"}</strong></div>
              {isApp && <div className="detail-row"><span>Replicas</span><strong>{item.spec?.replicas ?? 1}{item.spec?.autoscaling?.maxReplicas > 1 ? ` · autoscale to ${item.spec.autoscaling.maxReplicas}` : ""}</strong></div>}
              {(isApp || (kind === "databases" && item.spec?.placement !== "External")) && <div className="detail-row"><span>Size</span><strong>{sizeLabel(data?.platform?.capacity?.cpuSizes || [], item.spec?.resources?.requests?.cpu, item.spec?.resources?.requests?.cpu || "0.1 CPU")} · {sizeLabel(data?.platform?.capacity?.memorySizes || [], item.spec?.resources?.requests?.memory, item.spec?.resources?.requests?.memory || "128 MB")}</strong></div>}
              <div className="detail-row"><span>Namespace</span><strong>{item.status?.targetNamespace || "Pending"}</strong></div>
            </div>
          </Card>
          <Card>
            <div className="card-heading"><div><div className="eyebrow">Connection</div><h2>Endpoint</h2></div></div>
            <div className="connection-box"><code>{item.status?.host || item.status?.endpoint || item.status?.url || "Appears when ready"}</code></div>
          </Card>
        </div>
      )}
      {view === "logs" && <Card className="log-card"><pre>{logs.data?.lines || "No logs yet."}</pre><Button variant="outline" onClick={() => logs.refetch()}><RefreshCw size={14} /> Refresh</Button></Card>}
      {view === "metrics" && <Card><p className="muted">Metrics refresh from Prometheus once the service is scraping.</p><div className="panel-metrics">{(data?.metrics || []).map((metric) => <div className="panel-metric" key={metric.title}><div className="eyebrow">{metric.title}</div><div className="stat-value">{metric.value}</div></div>)}</div></Card>}
      {view === "deployments" && <Card>{deployments.length ? <Table headers={["Change", "Source", "Image", "Phase"]}>{deployments.map((entry) => <tr key={resourceName(entry)}><td>{entry.spec?.changeTitle}</td><td>{entry.spec?.source}</td><td>{entry.spec?.image}</td><td>{entry.status?.phase}</td></tr>)}</Table> : <p className="muted">No deployments recorded yet.</p>}</Card>}
      {view === "variables" && (
        <div className="stack-lg">
          {canMutate(data) ? (
            <>
          <Card>
            <form className="panel-form" onSubmit={saveVariable}>
              <Field label="Name"><Input required value={variable.key} onChange={(event) => setVariable({ ...variable, key: event.target.value })} placeholder="DATABASE_URL" /></Field>
              <Field label="Value"><Input required type={variable.secret ? "password" : "text"} value={variable.value} onChange={(event) => setVariable({ ...variable, value: event.target.value })} /></Field>
              <label className="check-row"><input type="checkbox" checked={variable.secret} onChange={(event) => setVariable({ ...variable, secret: event.target.checked })} /> Store as secret</label>
              <Button type="submit">Add variable</Button>
            </form>
          </Card>
          <Card>
            <form className="panel-form" onSubmit={attachShared}>
              <div className="eyebrow">Project variables</div>
              {shared.length ? shared.map((entry) => <label className="check-row" key={entry.name}><input type="checkbox" name="sharedVariable" value={entry.name} defaultChecked={(item.spec?.sharedVariableRefs || []).includes(entry.name)} /> {entry.name} {entry.secretRef ? "(secret)" : ""}</label>) : <p className="muted">No project-level variables in this environment.</p>}
              {shared.length > 0 && <Button type="submit">Reference selected variables</Button>}
            </form>
          </Card>
            </>
          ) : <Card><p className="muted">Your viewer role cannot change variables.</p></Card>}
        </div>
      )}
      {view === "console" && (
        <Card>
          {!canMutate(data) ? <p className="muted">Your viewer role cannot open consoles or run queries.</p> : isApp ? (
            <form className="panel-form" onSubmit={(event) => { event.preventDefault(); const target = event.target.target.value; const command = event.target.command.value || "sh"; action(`/apps/${name}/console/create`, { target, command }).then(() => alert("Console session created")).catch((error) => alert(error.message)); }}>
              <Field label="Pod"><Select name="target">{(runtime.data?.pods || []).map((pod) => <option key={pod.name} value={`${pod.name}|${pod.container}`}>{pod.name}</option>)}</Select></Field>
              <Field label="Command"><Input name="command" defaultValue="sh" /></Field>
              <Button type="submit"><Terminal size={14} /> Open console</Button>
            </form>
          ) : (
            <form className="panel-form" onSubmit={(event) => { event.preventDefault(); action(`/databases/${name}/query`, { query }).then((result) => setOutput(result.output || "")).catch((error) => alert(error.message)); }}>
              <Field label="Query"><ShadcnTextarea rows={6} value={query} onChange={(event) => setQuery(event.target.value)} /></Field>
              <Button type="submit">Run query</Button>
              {output && <pre className="log-output">{output}</pre>}
            </form>
          )}
        </Card>
      )}
      {view === "monitor" && <Card><div className="panel-metrics">{(data?.metrics || []).map((metric) => <div className="panel-metric" key={metric.title}><div className="eyebrow">{metric.title}</div><div className="stat-value">{metric.value}</div><div className="stat-detail">{metric.state}</div></div>)}</div></Card>}
      {view === "settings" && <ResourceSettings item={item} kind={kind} name={name} title={title} data={data} project={project} reload={reload} navigate={navigate} />}
    </>
  );
}

function ProjectSettings({ project, data, reload, environment: workspaceEnvironment }) {
  const name = resourceName(project);
  const [displayName, setDisplayName] = useState(project.spec?.displayName || name);
  const [environment, setEnvironment] = useState("");
  const githubEnvironment = workspaceEnvironment || project.spec?.environments?.[0] || "";
  const [variable, setVariable] = useState({ environment: githubEnvironment, name: "", value: "", secret: false });
  return (
    <>
      <PageHeader eyebrow="Project" title="Project settings" description="Environments, shared variables, and GitHub access for this project." />
      <div className="stack-lg">
        <Card>
          <form className="panel-form" onSubmit={(event) => { event.preventDefault(); action(`/projects/${name}/settings/save`, { displayName, environments: project.spec?.environments || [] }).then(() => { reload(); alert("Project settings saved"); }).catch((error) => alert(error.message)); }}>
            <Field label="Project name"><Input value={displayName} onChange={(event) => setDisplayName(event.target.value)} /></Field>
            <Button type="submit" disabled={!canMutate(data)}>Save project settings</Button>
          </form>
        </Card>
        <Card>
          <div className="card-heading"><div><div className="eyebrow">Environments</div><h2>Isolated namespaces</h2></div></div>
          {(project.spec?.environments || []).map((env) => <div className="panel-list-row" key={env}><strong>{env}</strong></div>)}
          <form className="panel-form" onSubmit={(event) => { event.preventDefault(); action(`/projects/${name}/environments/create`, { environment }).then(() => { reload(); setEnvironment(""); alert("Environment created"); }).catch((error) => alert(error.message)); }}>
            <Field label="New environment"><Input value={environment} onChange={(event) => setEnvironment(event.target.value)} placeholder="staging" /></Field>
            <Button type="submit" disabled={!canMutate(data)}>Add environment</Button>
          </form>
        </Card>
        <Card>
          <div className="card-heading"><div><div className="eyebrow">Shared variables</div><h2>Project values</h2></div></div>
          {(project.spec?.sharedVariables || []).map((entry) => <div className="panel-list-row" key={`${entry.environment}-${entry.name}`}><div><strong className="panel-code">{entry.name}</strong><small>{entry.environment} · {entry.secretRef ? "Secret" : "Literal"}</small></div>{canMutate(data) && <Button type="button" variant="ghost" size="sm" onClick={() => action(`/projects/${name}/variables/delete`, { environment: entry.environment, name: entry.name }).then(() => { reload(); alert("Shared variable deleted"); })}>Delete</Button>}</div>)}
          <form className="panel-form" onSubmit={(event) => { event.preventDefault(); action(`/projects/${name}/variables/save`, { ...variable, secret: variable.secret ? "on" : "" }).then(() => { reload(); setVariable({ ...variable, name: "", value: "" }); alert("Shared variable saved"); }).catch((error) => alert(error.message)); }}>
            <Field label="Environment"><Select value={variable.environment} onChange={(event) => setVariable({ ...variable, environment: event.target.value })}>{(project.spec?.environments || []).map((env) => <option key={env}>{env}</option>)}</Select></Field>
            <Field label="Name"><Input required value={variable.name} onChange={(event) => setVariable({ ...variable, name: event.target.value })} placeholder="DATABASE_URL" /></Field>
            <Field label="Value"><Input required type={variable.secret ? "password" : "text"} value={variable.value} onChange={(event) => setVariable({ ...variable, value: event.target.value })} /></Field>
            <label className="check-row"><input type="checkbox" checked={variable.secret} onChange={(event) => setVariable({ ...variable, secret: event.target.checked })} /> Secret</label>
            <Button type="submit" disabled={!canMutate(data)}>Add variable</Button>
          </form>
        </Card>
        <Card>
          <div className="card-heading"><div><div className="eyebrow">GitHub</div><h2>Repository access</h2></div></div>
          <p className="muted">{project.spec?.githubConnectionRef?.name ? `Connected as ${project.spec.githubConnectionRef.name}` : githubEnvironment ? `Install the platform GitHub App for ${githubEnvironment} to deploy from repositories.` : "Install the platform GitHub App on this project to deploy from repositories."}</p>
          {canMutate(data) && <Button type="button" onClick={() => action(`/projects/${name}/github/install`, { environment: githubEnvironment }).then((result) => { if (result.url) window.location.assign(result.url); else reload(); }).catch((error) => alert(error.message))}>Connect GitHub</Button>}
        </Card>
      </div>
    </>
  );
}

function PlatformSettings({ data, page, reload }) {
  const config = data?.platformConfig?.items?.[0];
  const github = useQuery({ queryKey: ["github-settings"], queryFn: () => api("/api/settings/github"), enabled: page === "github" });
  if (page === "domain") {
    return <DomainSettings config={config} reload={reload} />;
  }
  if (page === "github") {
    const info = github.data || {};
    return (
      <>
        <PageHeader eyebrow="Platform / Settings" title="GitHub App" description="Connect Geass to GitHub for repository deploys." />
        {!info.hasDashboardURL ? (
          <Card><p className="muted">Configure your dashboard domain before setting up a GitHub App.</p><AppLink href="/settings/domain"><Button>Configure domain</Button></AppLink></Card>
        ) : (
          <div className="stack-lg">
            <Card>
              {info.manifestAction && (
                <form method="post" action={info.manifestAction}>
                  <input type="hidden" name="manifest" value={info.manifest || ""} />
                  <p className="muted">Register a GitHub App with the correct callback and webhook URLs, then return here.</p>
                  <Button type="submit">Create GitHub App on GitHub</Button>
                </form>
              )}
            </Card>
            <GitHubManualForm config={config} reload={reload} />
          </div>
        )}
      </>
    );
  }
  if (page === "ha") return <HAReadiness data={data} />;
  if (page === "cluster") return <ClusterCapacity data={data} />;
  if (page === "cloud") return <CloudConnections data={data} reload={reload} />;
  if (page === "object") return <ObjectStorageSettings data={data} reload={reload} />;
  return (
    <>
      <PageHeader eyebrow="Platform" title="General settings" description="Configure the Geass control plane and inspect cluster health." />
      <Card>
        <div className="card-heading"><div><div className="eyebrow">Cluster overview</div><h2>Control plane</h2></div></div>
        <div className="detail-list">
          <div className="detail-row"><span>Clusters</span><strong>{list(data, "clusters").length}</strong></div>
          <div className="detail-row"><span>Dashboard URL</span><strong>{config?.spec?.dashboardURL || "Not configured"}</strong></div>
          <div className="detail-row"><span>HA nodes</span><strong>{data?.platform?.healthyNodes ?? 0}</strong></div>
        </div>
      </Card>
      <Card>
        <div className="setting-list">
          <AppLink className="setting-row" href="/settings/domain"><div className="setting-icon"><Server size={17} /></div><div><strong>Domain</strong><small>Configure exposure and DNS verification.</small></div><ArrowRight size={16} /></AppLink>
          <AppLink className="setting-row" href="/settings/github"><div className="setting-icon"><GitBranch size={17} /></div><div><strong>GitHub</strong><small>Connect repositories for source-based deploys.</small></div><ArrowRight size={16} /></AppLink>
          <AppLink className="setting-row" href="/cluster"><div className="setting-icon"><Cpu size={17} /></div><div><strong>Cluster capacity</strong><small>Inspect CPU, memory, and node pressure before creating resources.</small></div><ArrowRight size={16} /></AppLink>
          <AppLink className="setting-row" href="/ha-readiness"><div className="setting-icon"><ShieldCheck size={17} /></div><div><strong>HA readiness</strong><small>Check storage, nodes, and add-ons.</small></div><ArrowRight size={16} /></AppLink>
          <AppLink className="setting-row" href="/cloud-connections"><div className="setting-icon"><Cloud size={17} /></div><div><strong>Cloud connections</strong><small>Connect AWS and PlanetScale.</small></div><ArrowRight size={16} /></AppLink>
          <AppLink className="setting-row" href="/object-storage"><div className="setting-icon"><HardDrive size={17} /></div><div><strong>Object storage</strong><small>Set up the cluster MinIO server.</small></div><ArrowRight size={16} /></AppLink>
        </div>
      </Card>
    </>
  );
}

function DomainSettings({ config, reload }) {
  const { data } = useBootstrap();
  const [domain, setDomain] = useState(config?.spec?.rootDomain || "");
  const [exposure, setExposure] = useState(config?.spec?.dashboardExposure || "ingress");
  return (
    <>
      <PageHeader eyebrow="Platform / Settings" title="Domain" description="Choose how Geass is exposed and verify the public dashboard endpoint." />
      <Card>
        <form className="form-grid" onSubmit={(event) => { event.preventDefault(); action("/settings/domain/save", { domain, exposure, tunnelCNAMETarget: config?.spec?.tunnelCNAMETarget || "" }).then(() => { reload(); alert("Domain settings saved"); }); }}>
          <Field label="Your domain"><Input required value={domain} onChange={(event) => setDomain(event.target.value)} placeholder="example.com" /></Field>
          <Field label="Exposure"><Select value={exposure} onChange={(event) => setExposure(event.target.value)}><option value="ingress">Server (A record)</option><option value="cloudflare-tunnel">Local + Cloudflare Tunnel</option></Select></Field>
          <div className="form-actions"><Button type="submit" disabled={!canMutate(data)}>Save domain</Button></div>
        </form>
      </Card>
    </>
  );
}

function GitHubManualForm({ reload }) {
  const { data } = useBootstrap();
  const [values, setValues] = useState({ appID: "", clientID: "", slug: "", clientSecret: "", webhookSecret: "", privateKey: "" });
  return (
    <Card>
      <form className="form-grid" onSubmit={(event) => { event.preventDefault(); action("/settings/github/save", values).then(() => { reload(); alert("GitHub App credentials saved"); }).catch((error) => alert(error.message)); }}>
        <Field label="App ID"><Input required value={values.appID} onChange={(event) => setValues({ ...values, appID: event.target.value })} /></Field>
        <Field label="Client ID"><Input required value={values.clientID} onChange={(event) => setValues({ ...values, clientID: event.target.value })} /></Field>
        <Field label="App slug"><Input required value={values.slug} onChange={(event) => setValues({ ...values, slug: event.target.value })} /></Field>
        <Field label="Client secret"><Input type="password" value={values.clientSecret} onChange={(event) => setValues({ ...values, clientSecret: event.target.value })} /></Field>
        <Field label="Webhook secret"><Input type="password" value={values.webhookSecret} onChange={(event) => setValues({ ...values, webhookSecret: event.target.value })} /></Field>
        <Field label="Private key (PEM)"><ShadcnTextarea rows={6} value={values.privateKey} onChange={(event) => setValues({ ...values, privateKey: event.target.value })} /></Field>
        <Button type="submit" disabled={!canMutate(data)}>Save GitHub App</Button>
      </form>
    </Card>
  );
}

function ClusterCapacity({ data }) {
  const capacity = data?.platform?.capacity || {};
  const cpuUsed = capacity.cpuAllocatableMillis ? Math.min(100, Math.round((capacity.cpuRequestedMillis / capacity.cpuAllocatableMillis) * 100)) : 0;
  const memUsed = capacity.memoryAllocatableBytes ? Math.min(100, Math.round((capacity.memoryRequestedBytes / capacity.memoryAllocatableBytes) * 100)) : 0;
  const barClass = (used) => cn("capacity-bar", used >= 90 && "is-full", used >= 70 && used < 90 && "is-tight");
  return (
    <>
      <PageHeader eyebrow="Platform / Settings" title="Cluster" description="Compare allocatable CPU and memory to current requests before creating services or databases. If a resource will not fit, scale up instead of creating it." />
      {!capacity.known ? (
        <Card><p className="muted">{capacity.message || "Node capacity is unavailable until the cluster reports schedulable nodes."}</p></Card>
      ) : (
        <div className="stat-grid">
          <Card>
            <div className="stat-label">CPU available</div>
            <div className="stat-value">{capacity.cpuAvailable}</div>
            <div className="stat-detail">{capacity.cpuRequested} requested of {capacity.cpuAllocatable}</div>
            <div className={barClass(cpuUsed)}><span style={{ width: `${cpuUsed}%` }} /></div>
          </Card>
          <Card>
            <div className="stat-label">Memory available</div>
            <div className="stat-value">{capacity.memoryAvailable}</div>
            <div className="stat-detail">{capacity.memoryRequested} requested of {capacity.memoryAllocatable}</div>
            <div className={barClass(memUsed)}><span style={{ width: `${memUsed}%` }} /></div>
          </Card>
          <Card>
            <div className="stat-label">Healthy nodes</div>
            <div className="stat-value">{capacity.healthyNodes ?? 0}</div>
            <div className="stat-detail">{capacity.schedulableNodes ?? 0} schedulable</div>
          </Card>
        </div>
      )}
      {capacity.issues?.length > 0 && (
        <Card>
          <div className="card-heading"><div><div className="eyebrow">Issues</div><h2>Capacity and node pressure</h2></div></div>
          {capacity.issues.map((issue, index) => <div className="signal-row" key={`${issue.message}-${index}`}><span className={cn("signal-icon", issue.severity === "danger" ? "signal-danger" : "signal-warning")}><Cpu size={15} /></span><span>{issue.message}</span></div>)}
        </Card>
      )}
      <Card>
        <div className="card-heading"><div><div className="eyebrow">Nodes</div><h2>Schedulable capacity</h2></div></div>
        {capacity.nodes?.length ? capacity.nodes.map((node) => (
          <div className="setting-row" key={node.name}>
            <div className="setting-icon"><Server size={17} /></div>
            <div><strong>{node.name}</strong><small>{node.role} · {node.cpuAllocatable} CPU · {node.memoryAllocatable} memory{node.pressure?.length ? ` · ${node.pressure.join(", ")}` : ""}</small></div>
            <Badge tone={!node.ready ? "danger" : !node.schedulable ? "warning" : "success"}>{!node.ready ? "Not ready" : !node.schedulable ? "Unschedulable" : "Ready"}</Badge>
          </div>
        )) : <p className="muted">No Kubernetes nodes are visible yet.</p>}
      </Card>
    </>
  );
}

function HAReadiness({ data }) {
  const queryClient = useQueryClient();
  const report = list(data, "haReadiness")[0];
  return (
    <>
      <PageHeader eyebrow="Platform / Settings" title="HA readiness" description="High availability databases need at least three healthy nodes." />
      <Card>
        <div className="detail-list">
          <div className="detail-row"><span>Healthy nodes</span><strong>{report?.status?.healthyNodes ?? data?.platform?.healthyNodes ?? 0}</strong></div>
          <div className="detail-row"><span>Ready</span><strong>{data?.platform?.haReady ? "Yes" : "No"}</strong></div>
        </div>
        <Button disabled={!canMutate(data)} onClick={() => action("/ha-readiness/check").then(() => queryClient.invalidateQueries({ queryKey: ["dashboard", "bootstrap"] }))}><RefreshCw size={15} /> Run readiness check</Button>
      </Card>
    </>
  );
}

function ObjectStorageSettings({ data, reload }) {
  const items = list(data, "objectStores").filter((item) => !item.spec?.project && item.spec?.engine !== "S3" && item.spec?.placement !== "External");
  const server = items[0];
  const ready = condition(server) === "True";
  const capacity = data?.platform?.capacity || {};
  const [cpu, setCpu] = useState("250m");
  const [memory, setMemory] = useState("512Mi");
  const [confirmDelete, setConfirmDelete] = useState("");
  const live = { cpu, memory, cpuMillis: cpuMillis(cpu), memoryBytes: memoryBytes(memory), perCpuMillis: cpuMillis(cpu), perMemoryBytes: memoryBytes(memory), replicas: 1 };
  const fits = capacityFits(capacity, live);
  const projectBuckets = list(data, "objectStores").filter((item) => item.spec?.project && item.spec?.engine !== "S3" && item.spec?.placement !== "External");
  const serverName = resourceName(server);
  return (
    <>
      <PageHeader eyebrow="Platform / Settings" title="Object storage" description="Set up one MinIO server for the cluster. Project buckets are created on this server." />
      <Card>
        {server ? (
          <>
          <div className="setting-row">
            <div className="setting-icon"><HardDrive size={17} /></div>
            <div><strong>{serverName}</strong><small>{server.status?.endpoint || "Cluster MinIO server"}</small></div>
            <Badge tone={ready ? "success" : "warning"}>{ready ? "Ready" : "Pending"}</Badge>
          </div>
          {canMutate(data) && (
            projectBuckets.length
              ? <p className="form-help">Delete project buckets before removing the MinIO server.</p>
              : (
                <div className="panel-form">
                  <Field label={`Type ${serverName} to remove the MinIO server`}><Input value={confirmDelete} onChange={(event) => setConfirmDelete(event.target.value)} autoComplete="off" placeholder={serverName} /></Field>
                  <Button type="button" variant="danger" disabled={confirmDelete !== serverName} onClick={() => action(`/object-stores/${serverName}/delete`, { confirmName: confirmDelete }).then(() => { reload(); alert("MinIO server removed"); }).catch((error) => alert(error.message))}><Trash2 size={15} /> Remove MinIO server</Button>
                </div>
              )
          )}
          </>
        ) : (
          <>
            <p className="muted">In-cluster buckets stay disabled until this server exists. Choose how much CPU and memory to assign to MinIO.</p>
            <SizeFields cpu={cpu} memory={memory} onChange={(key, value) => { if (key === "cpu") setCpu(value); else setMemory(value); }} data={data} />
            {!fits && <p className="form-help text-danger">The cluster does not have enough capacity. <AppLink href="/cluster">Scale up from cluster capacity</AppLink> before creating MinIO.</p>}
            <Button disabled={!canMutate(data) || !fits} onClick={() => action("/object-stores/create", { cluster: "on", engine: "MinIO", placement: "InCluster", cpu, memory }).then(() => { reload(); alert("MinIO server created"); }).catch((error) => alert(error.message))}>Set up MinIO server</Button>
          </>
        )}
      </Card>
    </>
  );
}

function CloudConnections({ data, reload }) {
  const items = list(data, "cloudConnections").filter((item) => !item.spec?.project);
  const [provider, setProvider] = useState("AWS");
  const [form, setForm] = useState({ name: "", accessKeyId: "", secretAccessKey: "", region: "us-east-1", token: "", organization: "" });
  return (
    <>
      <PageHeader eyebrow="Platform / Settings" title="Cloud connections" description="Connect AWS and PlanetScale once for the cluster. External databases and buckets stay disabled until a connection is ready." />
      <Card>
        {items.length ? items.map((item) => (
          <div className="setting-row" key={resourceName(item)}>
            <div className="setting-icon"><Cloud size={17} /></div>
            <div><strong>{resourceName(item)}</strong><small>{item.spec?.provider}</small></div>
            <Badge tone={item.status?.available ? "success" : "warning"}>{item.status?.available ? "Ready" : "Pending"}</Badge>
          </div>
        )) : <p className="muted">No cloud connections yet.</p>}
      </Card>
      <Card>
        <form className="form-grid" onSubmit={(event) => { event.preventDefault(); action("/cloud-connections/create", { ...form, provider }).then(() => { reload(); alert("Connection saved"); setForm({ name: "", accessKeyId: "", secretAccessKey: "", region: "us-east-1", token: "", organization: "" }); }).catch((error) => alert(error.message)); }}>
          <Field label="Name"><Input required value={form.name} onChange={(event) => setForm({ ...form, name: event.target.value })} placeholder="prod-aws" /></Field>
          <Field label="Provider"><Select value={provider} onChange={(event) => setProvider(event.target.value)}><option>AWS</option><option>PlanetScale</option></Select></Field>
          {provider === "AWS" ? <>
            <Field label="Access key ID"><Input required value={form.accessKeyId} onChange={(event) => setForm({ ...form, accessKeyId: event.target.value })} /></Field>
            <Field label="Secret access key"><Input required type="password" value={form.secretAccessKey} onChange={(event) => setForm({ ...form, secretAccessKey: event.target.value })} /></Field>
            <Field label="Region"><Input value={form.region} onChange={(event) => setForm({ ...form, region: event.target.value })} /></Field>
          </> : <>
            <Field label="Organization"><Input required value={form.organization} onChange={(event) => setForm({ ...form, organization: event.target.value })} /></Field>
            <Field label="Service token"><Input required type="password" value={form.token} onChange={(event) => setForm({ ...form, token: event.target.value })} /></Field>
          </>}
          <Button type="submit" disabled={!canMutate(data)}>Save connection</Button>
        </form>
      </Card>
    </>
  );
}

function LoginScreen({ onSuccess }) {
  const [username, setUsername] = useState("admin");
  const [password, setPassword] = useState("");
  const [pending, setPending] = useState(false);
  const submit = (event) => {
    event.preventDefault();
    setPending(true);
    action("/login", { username, password }).then(() => onSuccess()).catch((error) => alert(error.message)).finally(() => setPending(false));
  };
  return (
    <div className="error-screen">
      <div className="brand-mark" aria-hidden="true">G</div>
      <h1>Sign in to Geass</h1>
      <p>Enter your username and password. Admins can change the cluster; viewers can only inspect it.</p>
      <form className="form-grid" style={{ width: "min(320px, 100%)" }} onSubmit={submit}>
        <Field label="Username"><Input required autoFocus value={username} onChange={(event) => setUsername(event.target.value)} autoComplete="username" /></Field>
        <Field label="Password"><Input type="password" required value={password} onChange={(event) => setPassword(event.target.value)} autoComplete="current-password" /></Field>
        <Button type="submit" disabled={pending}>{pending ? "Signing in…" : "Continue"}</Button>
      </form>
    </div>
  );
}

function DashboardScreen({ mode }) {
  const { data, error, isPending, refetch } = useBootstrap();
  const navigate = useNavigate();
  const routeState = useRouterState({ select: (state) => ({ location: state.location, params: state.matches.at(-1)?.params || {} }) });
  const search = routeState.location.search || {};
  const params = routeState.params;
  if (error?.status === 401) return <LoginScreen onSuccess={() => refetch()} />;
  if (error) return <div className="error-screen"><h1>Dashboard unavailable</h1><p>{error.message}</p><Button onClick={() => refetch()}>Try again</Button></div>;
  if (isPending || !data) return <div className="loading-screen"><div className="spinner" />Loading Geass…</div>;
  const projects = list(data, "projects");
  const project = projects.find((item) => resourceName(item) === params.projectName);
  let title = "Dashboard";
  let content;
  let environment = search.environment || project?.spec?.environments?.[0] || "";
  const setEnvironment = (next) => navigate({ search: (previous) => ({ ...previous, environment: next || undefined }) });
  if (mode === "projects") { title = "Projects"; content = <Projects data={data} />; }
  else if (mode === "workspace" && project) { title = project.spec?.displayName || resourceName(project); content = <Workspace project={project} data={data} environment={environment} />; }
  else if (mode === "project-settings" && project) { title = "Project settings"; content = <ProjectSettings project={project} data={data} reload={refetch} environment={environment} />; }
  else if (mode.startsWith("resource:") && project) {
    const kind = mode.slice("resource:".length);
    title = params.name;
    content = <ResourceDetail project={project} data={data} kind={kind} name={params.name} reload={refetch} />;
  } else if (mode === "settings" || mode.startsWith("settings:")) {
    const page = mode.split(":")[1] || "general";
    title = page === "general" ? "Settings" : page === "github" ? "GitHub App" : page === "ha" ? "HA readiness" : page === "cluster" ? "Cluster" : page === "cloud" ? "Cloud connections" : page === "object" ? "Object storage" : "Domain";
    content = <PlatformSettings data={data} page={page} reload={refetch} />;
  } else {
    content = <Empty title="Page not found" description="The requested dashboard page does not exist." action={<Button onClick={() => navigate({ to: "/projects" })}>Open projects</Button>} />;
  }
  return <Layout title={title} project={project} environment={environment} setEnvironment={project ? setEnvironment : undefined}>{content}</Layout>;
}

const projectSearch = (search) => {
  const value = (key) => typeof search[key] === "string" && search[key] ? search[key] : undefined;
  return { environment: value("environment"), view: value("view") };
};
const rootRoute = createRootRoute({ component: () => <Outlet /> });
const route = (path, mode, validateSearch) => createRoute({ getParentRoute: () => rootRoute, path, validateSearch, component: () => <DashboardScreen mode={mode} /> });
const routeTree = rootRoute.addChildren([
  createRoute({ getParentRoute: () => rootRoute, path: "/", component: () => <DashboardScreen mode="projects" /> }),
  route("/projects", "projects"),
  route("/projects/$projectName/settings", "project-settings", projectSearch),
  route("/projects/$projectName/apps/$name", "resource:apps", projectSearch),
  route("/projects/$projectName/databases/$name", "resource:databases", projectSearch),
  route("/projects/$projectName/caches/$name", "resource:caches", projectSearch),
  route("/projects/$projectName/logical-databases/$name", "resource:logical-databases", projectSearch),
  route("/projects/$projectName/object-stores/$name", "resource:object-stores", projectSearch),
  route("/projects/$projectName", "workspace", projectSearch),
  route("/settings", "settings"),
  route("/settings/domain", "settings:domain"),
  route("/settings/github", "settings:github"),
  route("/ha-readiness", "settings:ha"),
  route("/cluster", "settings:cluster"),
  route("/cloud-connections", "settings:cloud"),
  route("/cloud-connections/new", "settings:cloud"),
  route("/object-storage", "settings:object"),
]);
const router = createRouter({ routeTree, defaultPreload: "intent" });
const queryClient = new QueryClient();

createRoot(document.getElementById("root")).render(
  <QueryClientProvider client={queryClient}>
    <RouterProvider router={router} />
    <NoticeHost />
  </QueryClientProvider>
);
