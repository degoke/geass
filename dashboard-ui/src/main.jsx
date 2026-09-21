import React, { useEffect, useMemo, useState } from "react";
import { createRoot } from "react-dom/client";
import "@fontsource-variable/geist";
import { QueryClient, QueryClientProvider, useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { createRootRoute, createRoute, createRouter, Link, Outlet, RouterProvider, useNavigate, useRouterState } from "@tanstack/react-router";
import { cn } from "@/lib/utils";
import { Button as ShadcnButton } from "@/components/ui/button";
import { Badge as ShadcnBadge } from "@/components/ui/badge";
import { Card as ShadcnCard } from "@/components/ui/card";
import { Input as ShadcnInput } from "@/components/ui/input";
import { Textarea as ShadcnTextarea } from "@/components/ui/textarea";
import { Label as ShadcnLabel } from "@/components/ui/label";
import {
  Select as ShadcnSelect, SelectContent, SelectItem, SelectTrigger, SelectValue,
} from "@/components/ui/select";
import { Separator as ShadcnSeparator } from "@/components/ui/separator";
import {
  Table as ShadcnTable, TableBody, TableHead, TableHeader, TableRow, TableCell,
} from "@/components/ui/table";
import {
  Dialog, DialogContent, DialogDescription, DialogFooter, DialogHeader,
  DialogTitle,
} from "@/components/ui/dialog";
import {
  Activity, AlertTriangle, ArrowLeft, ArrowRight, Box, Check, ChevronDown, ChevronRight,
  CircleHelp, Cloud, Database, ExternalLink, FolderKanban, Gauge, GitBranch,
  HardDrive, LayoutDashboard, Menu, Moon, MoreHorizontal, Network, Plus,
  RefreshCw, Search, Server, Settings, ShieldCheck, Sparkles, Sun, Trash2, X,
} from "lucide-react";
import "./styles.css";

function Button({ variant = "default", ...props }) { return <ShadcnButton variant={variant === "danger" ? "destructive" : variant} {...props} />; }
function Card({ className = "", ...props }) { return <ShadcnCard className={cn("ui-card", className)} {...props} />; }
function Badge({ children, tone = "neutral", className = "" }) { return <ShadcnBadge variant={tone === "danger" ? "destructive" : "outline"} className={cn("ui-badge", `ui-badge-${tone}`, className)}>{children}</ShadcnBadge>; }
function Input({ className = "", ...props }) { return <ShadcnInput className={cn("ui-input", className)} {...props} />; }
function openResourceDialog() { window.dispatchEvent(new Event("geass:open-resource")); }
function notify(message, tone = "neutral") { window.dispatchEvent(new CustomEvent("geass:notice", { detail: { message: String(message), tone } })); }
function alert(message) { const text = String(message); notify(text, /error|fail|unavailable|request failed|not found/i.test(text) ? "error" : "success"); }
function Select({ className = "", children, value, onChange, ...props }) {
  const options = React.Children.toArray(children).filter(Boolean).map((child) => ({ value: child.props.value ?? child.props.children, label: child.props.children, disabled: child.props.disabled }));
  const selected = options.find((option) => String(option.value) === String(value));
  return <ShadcnSelect value={value} onValueChange={(next) => onChange?.({ target: { value: next } })} {...props}><SelectTrigger className={cn("ui-input ui-select", className)}><SelectValue>{selected?.label ?? value}</SelectValue></SelectTrigger><SelectContent>{options.map((option) => <SelectItem key={option.value} value={String(option.value)} disabled={option.disabled}>{option.label}</SelectItem>)}</SelectContent></ShadcnSelect>;
}
function ResourceChoice({ option, selected, onSelect }) { const Icon = option.icon; return <button type="button" role="radio" aria-checked={selected} disabled={option.disabled} className={cn("resource-choice", selected && "resource-choice-selected", option.disabled && "resource-choice-disabled")} onClick={() => onSelect(option.value)}><span className="resource-choice-icon"><Icon size={16} /></span><span className="resource-choice-copy"><strong>{option.label}</strong><small>{option.description}</small></span>{option.note && <span className="resource-choice-note">{option.note}</span>}</button>; }
function Separator() { return <ShadcnSeparator className="ui-separator" />; }
function Empty({ icon: Icon = Box, title, description, action }) { return <Card className="empty-state"><Icon size={26} /><h3>{title}</h3><p>{description}</p>{action}</Card>; }
function PageHeader({ eyebrow, title, description, actions }) { return <div className="page-header"><div>{eyebrow && <div className="eyebrow">{eyebrow}</div>}<h1>{title}</h1>{description && <p>{description}</p>}</div><div className="page-actions">{actions}</div></div>; }
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
  return <div className="notice-stack" aria-live="polite" aria-atomic="false">{notices.map((notice) => <div className={cn("notice", `notice-${notice.tone || "neutral"}`)} key={notice.id} role="status"><span>{notice.message}</span><button type="button" aria-label="Dismiss notification" onClick={() => setNotices((current) => current.filter((item) => item.id !== notice.id))}><X size={14} /></button></div>)}</div>;
}
function Status({ value }) { const ready = value === "True" || value === "Ready" || value === "healthy" || value === true; return <Badge tone={ready ? "success" : value === "False" || value === "Failed" ? "danger" : "warning"}>{ready ? <Check size={13} /> : <Activity size={13} />}{String(value || "Pending")}</Badge>; }
function ProjectStatus({ value }) { const ready = value === "True" || value === "Ready" || value === "healthy" || value === true; const failed = value === "False" || value === "Failed"; const label = ready ? "Healthy" : failed ? "Attention" : "Provisioning"; return <span className={cn("project-status", ready ? "project-status-healthy" : failed ? "project-status-attention" : "project-status-provisioning")}><span className="project-status-dot" aria-hidden="true" />{label}</span>; }

async function api(path, options = {}) {
  const response = await fetch(path, { credentials: "same-origin", ...options });
  const type = response.headers.get("content-type") || "";
  const payload = type.includes("json") ? await response.json() : await response.text();
  if (!response.ok) throw new Error(payload?.error || payload || `Request failed (${response.status})`);
  return payload;
}
async function action(path, values = {}, options = {}) {
  const body = new URLSearchParams();
  Object.entries(values).forEach(([key, value]) => (Array.isArray(value) ? value.forEach((item) => body.append(key, item)) : body.set(key, value ?? "")));
  return api(`/api${path.startsWith("/api/") ? path.slice(4) : path}`, { method: "POST", body, headers: { Accept: "application/json" }, ...options });
}

const list = (data, key) => data?.[key]?.items || [];
const resourceName = (item) => item?.metadata?.name || "Unnamed";
const condition = (item) => item?.status?.conditions?.find((entry) => entry.type === "Ready")?.status || item?.status?.phase || "Pending";

function useBootstrap() { return useQuery({ queryKey: ["dashboard", "bootstrap"], queryFn: () => api("/api/bootstrap"), staleTime: 5000 }); }

function AppLink({ href, ...props }) { return <Link to={href} {...props} />; }

function Sidebar({ project, path }) {
  const [open, setOpen] = useState(false); const [dark, setDark] = useState(localStorage.getItem("geass.theme") !== "light");
  useEffect(() => { document.documentElement.dataset.theme = dark ? "dark" : "light"; localStorage.setItem("geass.theme", dark ? "dark" : "light"); }, [dark]);
  const global = [
    ["/projects", "Projects", FolderKanban],
  ];
  const projectItems = project ? [[`/projects/${project}`, "Workspace", LayoutDashboard], [`/projects/${project}/logs`, "Logs", Activity], [`/projects/${project}/observability`, "Observability", Gauge], [`/projects/${project}/settings`, "Project settings", Settings]] : [];
  const settings = [["/settings", "Settings", Settings], ["/ha-readiness", "HA readiness", ShieldCheck], ["/cloud-connections", "Cloud connections", Cloud]];
  const links = project ? projectItems : global;
  const item = ([href, label, Icon]) => { const baseHref = href.split("?")[0]; return <AppLink key={href} href={href} onClick={() => setOpen(false)} className={cn("nav-link", (path === baseHref || (baseHref !== "/projects" && path.startsWith(baseHref))) && "nav-link-active")}><Icon size={17} /><span>{label}</span></AppLink>; };
  return <><aside className={cn("app-sidebar", open && "app-sidebar-open")}><div className="brand"><AppLink href="/projects" onClick={() => setOpen(false)}><span className="brand-mark">G</span><span>Geass</span></AppLink></div><nav>{links.map(item)}</nav><div className="sidebar-spacer" /><nav>{settings.map(item)}</nav><Separator /><Button variant="ghost" className="theme-button" onClick={() => setDark(!dark)}>{dark ? <Sun size={17} /> : <Moon size={17} />}<span>{dark ? "Light mode" : "Dark mode"}</span></Button><div className="sidebar-user"><div className="avatar">G</div><div><strong>Geass operator</strong><small>Control plane</small></div><MoreHorizontal size={16} /></div></aside><Button variant="outline" size="icon" className="mobile-nav" onClick={() => setOpen(!open)}>{open ? <X size={18} /> : <Menu size={18} />}</Button></>;
}

function Topbar({ title, project, environment, setEnvironment }) {
  const projectName = project?.spec?.displayName || project?.metadata?.name;
  return <header className="topbar"><div className="topbar-title"><span className="topbar-muted">Geass</span><span>/</span><strong>{projectName || title}</strong></div>{project && <div className="topbar-actions"><Select value={environment || ""} onChange={(e) => setEnvironment(e.target.value)} aria-label="Environment"><option value="">All environments</option>{(project.spec?.environments || []).map((env) => <option key={env}>{env}</option>)}</Select></div>}</header>;
}

function Layout({ children, title = "Dashboard", project, environment, setEnvironment, fullHeight = false }) {
  const path = useRouterState({ select: (state) => state.location.pathname });
  return <div className="app-shell"><Sidebar project={project?.metadata?.name} path={path} /><div className="main-panel"><Topbar title={title} project={project} environment={environment} setEnvironment={setEnvironment} /><main className={cn("main-content", project && fullHeight && "project-main-content")}>{children}</main></div></div>;
}

// The dashboard opens directly to Projects; there is no separate overview surface.
function Signal({ icon: Icon, label, value, tone = "neutral" }) { return <div className="signal-row"><div className={cn("signal-icon", `signal-${tone}`)}><Icon size={16} /></div><span>{label}</span><strong>{value}</strong></div>; }

function Projects({ data }) { const navigate = useNavigate(); const queryClient = useQueryClient(); const create = useMutation({ mutationFn: () => action("/projects/create"), onSuccess: (result) => { queryClient.invalidateQueries({ queryKey: ["dashboard", "bootstrap"] }); navigate({ to: result.project ? `/projects/${result.project}` : "/projects" }); }, onError: (error) => alert(error.message) }); const projects = list(data, "projects"); return <><PageHeader title="Projects" actions={<Button onClick={() => create.mutate()} disabled={create.isPending}><Plus size={16} /> New project</Button>} /><div className="projects-shell"><div className="projects-toolbar"><div className="search-wrap"><Search size={16} /><Input placeholder="Search projects" /></div></div>{projects.length ? <div className="project-card-grid">{projects.map((p) => { const name = resourceName(p); const environments = p.spec?.environments || []; return <AppLink key={name} href={`/projects/${name}`} className="project-card"><div className="project-card-header"><div className="project-card-title-block"><span className="project-card-kicker">Project</span><strong className="project-card-title">{p.spec?.displayName || name}</strong><span className="project-card-name">{name}</span></div><ProjectStatus value={condition(p)} /></div><div className="project-card-meta"><div><span>Environments</span><strong>{environments.length ? environments.join(" · ") : "—"}</strong></div><div><span>Cluster</span><strong>{p.spec?.clusterRef?.name || "—"}</strong></div></div></AppLink>; })}</div> : <Empty icon={FolderKanban} title="Create your first project" description="Projects keep environments and their resources isolated." action={<Button onClick={() => create.mutate()} disabled={create.isPending}><Plus size={15} /> Create project</Button>} />}</div></>; }
function Table({ headers, children }) {
  return <ShadcnTable className="table-scroll"><TableHeader><TableRow>{headers.map((header) => <TableHead key={header}>{header}</TableHead>)}</TableRow></TableHeader><TableBody>{React.Children.map(children, (row) => <TableRow>{React.Children.map(row?.props?.children, (cell) => <TableCell>{cell?.props?.children}</TableCell>)}</TableRow>)}</TableBody></ShadcnTable>;
}

function ProjectPanel({ project, data, panel, environment, reload }) {
  const name = resourceName(project);
  const display = project.spec?.displayName || name;
  const panelTitles = { settings: "Project settings", environments: "Environments", variables: "Shared variables", usage: "Usage", "usage-details": "Usage details", logs: "Project logs", observability: "Project observability" };
  const [displayName, setDisplayName] = useState(display);
  const [environments, setEnvironments] = useState(project.spec?.environments || []);
  const [newEnvironment, setNewEnvironment] = useState("");
  const [variableEnvironment, setVariableEnvironment] = useState(environment || environments[0] || "");
  const [variableName, setVariableName] = useState("");
  const [variableValue, setVariableValue] = useState("");
  const [variableKind, setVariableKind] = useState("literal");
  const href = (next) => `/projects/${encodeURIComponent(name)}?${new URLSearchParams({ ...(environment ? { environment } : {}), panel: next }).toString()}`;
  const saveSettings = (event) => { event.preventDefault(); action(`/projects/${encodeURIComponent(name)}/settings/save`, { displayName, environments }).then(() => { reload(); alert("Project settings saved"); }).catch((error) => alert(error.message)); };
  const createEnvironment = (event) => { event.preventDefault(); if (!newEnvironment.trim()) return; action(`/projects/${encodeURIComponent(name)}/environments/create`, { environment: newEnvironment.trim() }).then(() => { reload(); setNewEnvironment(""); alert("Environment created"); }).catch((error) => alert(error.message)); };
  const saveVariable = (event) => { event.preventDefault(); action(`/projects/${encodeURIComponent(name)}/variables/save`, { environment: variableEnvironment, name: variableName, value: variableValue, ...(variableKind === "secret" ? { secret: "on" } : {}) }).then(() => { reload(); setVariableName(""); setVariableValue(""); alert("Shared variable saved"); }).catch((error) => alert(error.message)); };
  return <section className="project-panel" aria-label={panelTitles[panel] || "Project panel"}><div className="project-panel-header"><div><div className="eyebrow">Project</div><h2>{panelTitles[panel] || "Project"}</h2></div></div><nav className="project-panel-tabs" aria-label="Project panels">{Object.entries({ settings: "General", environments: "Environments", variables: "Shared variables", usage: "Usage", logs: "Logs", observability: "Observability" }).map(([key, label]) => <AppLink key={key} className={panel === key ? "project-panel-tab-active" : ""} href={href(key)}>{label}</AppLink>)}</nav><div className="project-panel-body">{panel === "settings" && <Card><form className="panel-form" onSubmit={saveSettings}><ShadcnLabel className="field-label">Project name<Input value={displayName} onChange={(event) => setDisplayName(event.target.value)} /></ShadcnLabel><div className="field-label">Environments<div className="panel-list">{environments.map((env) => <label className="panel-list-row" key={env}><input type="checkbox" checked readOnly /><span>{env}</span></label>)}</div></div><Button type="submit">Save project settings</Button></form></Card>}{panel === "environments" && <><Card><div className="card-heading"><div><div className="eyebrow">Project environments</div><h2>Isolated environments</h2></div></div><div className="panel-list">{environments.map((env) => <div className="panel-list-row" key={env}><div><strong>{env}</strong><small>Isolated project namespace</small></div>{environments.length > 1 && <Button type="button" variant="ghost" size="sm" onClick={() => { if (confirm(`Archive ${env}?`)) action(`/projects/${encodeURIComponent(name)}/environments/archive`, { environment: env, confirmName: env }).then(() => { reload(); alert("Environment archived"); }).catch((error) => alert(error.message)); }}>Archive</Button>}</div>)}</div></Card><Card><form className="panel-form" onSubmit={createEnvironment}><ShadcnLabel className="field-label">New environment<Input value={newEnvironment} onChange={(event) => setNewEnvironment(event.target.value)} placeholder="staging" /></ShadcnLabel><Button type="submit">Add environment</Button></form></Card></>}{panel === "variables" && <><Card><div className="card-heading"><div><div className="eyebrow">Shared variables</div><h2>Project values</h2></div></div><div className="panel-list">{(project.spec?.sharedVariables || []).filter((variable) => !environment || variable.environment === environment).map((variable) => <div className="panel-list-row" key={`${variable.environment}-${variable.name}`}><div><strong className="panel-code">{variable.name}</strong><small>{variable.environment} · {variable.secretRef ? "Secret" : variable.value || "Literal"}</small></div><Button type="button" variant="ghost" size="sm" onClick={() => action(`/projects/${encodeURIComponent(name)}/variables/delete`, { environment: variable.environment, name: variable.name }).then(() => { reload(); alert("Shared variable deleted"); }).catch((error) => alert(error.message))}>Delete</Button></div>)}</div>{!(project.spec?.sharedVariables || []).length && <div className="panel-empty"><p>No shared variables in this environment yet.</p></div>}</Card><Card><form className="panel-form" onSubmit={saveVariable}><ShadcnLabel className="field-label">Environment<Select value={variableEnvironment} onChange={(event) => setVariableEnvironment(event.target.value)}>{environments.map((env) => <option key={env}>{env}</option>)}</Select></ShadcnLabel><ShadcnLabel className="field-label">Variable name<Input required value={variableName} onChange={(event) => setVariableName(event.target.value)} placeholder="DATABASE_URL" /></ShadcnLabel><ShadcnLabel className="field-label">Value<Input required type={variableKind === "secret" ? "password" : "text"} value={variableValue} onChange={(event) => setVariableValue(event.target.value)} placeholder="Value" /></ShadcnLabel><ShadcnLabel className="field-label">Storage<Select value={variableKind} onChange={(event) => setVariableKind(event.target.value)}><option value="literal">Literal</option><option value="secret">Secret</option></Select></ShadcnLabel><Button type="submit">Add variable</Button></form></Card></>}{panel === "usage" && <><PageHeader eyebrow="Project" title="Usage" description="Current and estimated resource usage for this project." /><div className="panel-metrics">{(data?.metrics || []).map((metric) => <div className="panel-metric" key={metric.title}><div className="eyebrow">{metric.title}</div><div className="stat-value">{metric.value}</div><div className="stat-detail">{metric.state}</div></div>)}</div><Card><div className="callout"><Activity size={18} /><div><strong>Usage summary</strong><p>Measurements refresh from the configured metrics service.</p></div></div><AppLink className="text-link" href={href("usage-details")}>View details <ArrowRight size={14} /></AppLink></Card></>}{panel === "usage-details" && <><PageHeader eyebrow="Project" title="Usage details" description="Inspect the measurements behind the project usage summary." /><Card><Table headers={["Metric", "Quantity", "Unit rate", "Total"]}>{(data?.metrics || []).map((metric) => <tr key={metric.title}><td>{metric.title}</td><td>{metric.value}</td><td>Not configured</td><td>Unavailable</td></tr>)}</Table></Card></>}{panel === "logs" && <><PageHeader eyebrow="Project" title="Project logs" description="Logs from resources in this project are available on each resource page." /><Card><div className="panel-empty"><Activity size={24} /><h3>Resource logs</h3><p>Select a service and open its Logs tab to inspect live output.</p></div></Card></>}{panel === "observability" && <><PageHeader eyebrow="Project" title="Project observability" description="Health signals for resources in this project." /><div className="panel-metrics">{(data?.metrics || []).map((metric) => <div className="panel-metric" key={metric.title}><div className="eyebrow">{metric.title}</div><div className="stat-value">{metric.value}</div><div className="stat-detail">{metric.state}</div></div>)}</div></>}</div></section>;
}

function ProjectCanvas({ project, data, environment, setEnvironment, search = {}, reload }) {
  const name = resourceName(project); const selected = search.resource || ""; const panel = search.panel || ""; const [zoom, setZoom] = useState(1);
  const inEnvironment = (item) => item.spec?.project === name && (!environment || item.spec?.environment === environment);
  const resources = [...list(data, "apps").filter(inEnvironment).map((item) => [item, "Service", Box, "apps"]), ...list(data, "databases").filter(inEnvironment).map((item) => [item, "PostgreSQL database", Database, "databases"]), ...list(data, "caches").filter(inEnvironment).map((item) => [item, "Redis cache", Network, "caches"]), ...list(data, "objectStores").filter(inEnvironment).map((item) => [item, "Object storage", HardDrive, "object-stores"])]
  const selectedResource = selected.split("/");
  return <div className="workspace-layout"><div className="workspace-topology"><div className="topology-toolbar"><div className="topology-toolbar-controls"><Button variant="ghost" size="sm" onClick={() => setZoom(1)}>Fit</Button><Button variant="ghost" size="sm" onClick={() => setZoom((value) => Math.min(1.5, value + .1))} aria-label="Zoom in">+</Button><Button variant="ghost" size="sm" onClick={() => setZoom((value) => Math.max(.7, value - .1))} aria-label="Zoom out">−</Button></div><Button size="sm" onClick={openResourceDialog}><Plus size={15} /> Add resource</Button></div><div className="topology-preview" aria-label="Workspace topology canvas"><div className="topology-canvas" style={{ transform: `scale(${zoom})` }}>{resources.length ? resources.flatMap(([item, label, Icon, kind], index) => [index > 0 && <span className="topology-line" aria-hidden="true" key={`line-${kind}-${resourceName(item)}`}>──</span>, <ShadcnCard className={cn("topology-node", selected === `${kind}/${resourceName(item)}` && "topology-node-selected")} key={`${kind}-${resourceName(item)}`}><AppLink className="topology-node-link" href={`/projects/${encodeURIComponent(name)}?environment=${encodeURIComponent(environment)}&resource=${kind}/${encodeURIComponent(resourceName(item))}&view=overview`}><span className="topology-node-icon"><Icon size={18} /></span><span>{resourceName(item)}</span><small>{label}</small></AppLink></ShadcnCard>]) : <div className="topology-empty"><span className="topology-node-icon">·</span><p>No resources in this environment yet.</p><Button size="sm" onClick={openResourceDialog}>Add the first resource</Button></div>}</div></div></div><ResourceDialog project={name} environment={environment} />{panel && <ProjectPanel project={project} data={data} panel={panel} environment={environment} reload={reload} />}{selected && selectedResource.length === 2 && <div className="service-drawer"><ResourceDetail project={project} data={data} kind={selectedResource[0]} name={selectedResource[1]} view={search.view || "overview"} reload={reload} /></div>}</div>;
}

function ProjectWorkspace({ project, data, environment, setEnvironment }) { const navigate = useNavigate(); const name = resourceName(project); const apps = list(data, "apps").filter((a) => a.spec?.project === name && (!environment || a.spec?.environment === environment)); const dbs = list(data, "databases").filter((x) => x.spec?.project === name && (!environment || x.spec?.environment === environment)); const caches = list(data, "caches").filter((x) => x.spec?.project === name && (!environment || x.spec?.environment === environment)); const stores = list(data, "objectStores").filter((x) => x.spec?.project === name && (!environment || x.spec?.environment === environment)); const total = apps.length + dbs.length + caches.length + stores.length; return <><PageHeader eyebrow="Project workspace" title={project.spec?.displayName || name} description="Compose your application stack and monitor every environment." actions={<Button variant="outline" onClick={() => navigate({ to: `/projects/${name}?panel=settings` })}><Settings size={16} /> Project settings</Button>} /><div className="workspace-toolbar"><div><span className="eyebrow">Environment</span><Select value={environment} onChange={(e) => setEnvironment(e.target.value)}>{(project.spec?.environments || []).map((env) => <option key={env}>{env}</option>)}</Select></div><div className="workspace-actions"><Button onClick={openResourceDialog}><Plus size={16} /> Add resource</Button></div></div><div className="stat-grid stat-grid-compact"><Card><div className="stat-label">Services</div><div className="stat-value">{apps.length}</div><div className="stat-detail">Deployments and apps</div></Card><Card><div className="stat-label">Data services</div><div className="stat-value">{dbs.length + caches.length}</div><div className="stat-detail">Databases and caches</div></Card><Card><div className="stat-label">Storage</div><div className="stat-value">{stores.length}</div><div className="stat-detail">Object storage</div></Card></div><Card className="workspace-canvas"><div className="card-heading"><div><div className="eyebrow">Topology</div><h2>{total ? `${total} resources in ${environment || "all environments"}` : "Start building"}</h2></div><Badge tone={total ? "success" : "neutral"}>{total ? "Active" : "Empty"}</Badge></div>{total ? <div className="resource-grid">{[...apps.map((x) => [x, "Service", Box, "apps"]), ...dbs.map((x) => [x, "Database", Database, "databases"]), ...caches.map((x) => [x, "Cache", Network, "caches"]), ...stores.map((x) => [x, "Object storage", HardDrive, "object-stores"])].map(([item, label, Icon, kind]) => <AppLink href={`/projects/${name}?resource=${kind}/${resourceName(item)}&view=overview`} className="resource-tile" key={`${kind}-${resourceName(item)}`}><div className="resource-tile-icon"><Icon size={19} /></div><div><strong>{resourceName(item)}</strong><small>{label} · {item.spec?.environment}</small></div><Status value={condition(item)} /><ArrowRight size={15} /></AppLink>)}</div> : <div className="workspace-empty"><div className="empty-orbit"><Plus size={24} /></div><h3>Add your first resource</h3><p>Choose a service, database, cache, or object store to continue.</p><Button onClick={openResourceDialog}><Plus size={16} /> Add resource</Button></div>}</Card><ResourceDialog project={name} environment={environment} /></>; }
function LegacyResourceDialog({ project, environment }) {
  const queryClient = useQueryClient();
  const [open, setOpen] = useState(false);
  const [step, setStep] = useState(1);
  const [resourceType, setResourceType] = useState("service");
  const [kind, setKind] = useState("app");
  const [name, setName] = useState("");
  const [image, setImage] = useState("nginx:alpine");
  const resourceTypes = [
    { value: "service", label: "Service", description: "Run an application", icon: Box },
    { value: "database", label: "Database", description: "Store application data", icon: Database },
    { value: "bucket", label: "Bucket", description: "Keep files and assets", icon: HardDrive },
  ];
  const resourceOptions = {
    service: [{ value: "app", label: "Docker image", description: "Deploy from a container", icon: Box }, { value: "github", label: "GitHub", description: "Deploy from a repository", note: "Coming soon", icon: GitBranch, disabled: true }],
    database: [{ value: "database", label: "PostgreSQL", description: "Managed relational database", icon: Database }, { value: "cache", label: "Redis", description: "Managed in-memory store", icon: Network }],
    bucket: [{ value: "store", label: "Object storage", description: "S3-compatible bucket", icon: HardDrive }],
  };
  useEffect(() => { const openDialog = () => { setStep(1); setOpen(true); }; window.addEventListener("geass:open-resource", openDialog); return () => window.removeEventListener("geass:open-resource", openDialog); }, []);
  const selectResourceType = (nextType) => { setResourceType(nextType); setKind(resourceOptions[nextType].find((option) => !option.disabled).value); };
  const submit = (event) => { event.preventDefault(); const endpoint = { app: "/apps/create", database: "/databases/create", cache: "/caches/create", store: "/object-stores/create" }[kind]; const values = kind === "app" ? { project, environment, image, port: "80" } : { name, project, environment }; action(endpoint, values).then(() => { queryClient.invalidateQueries({ queryKey: ["dashboard", "bootstrap"] }); setOpen(false); setStep(1); setName(""); setImage("nginx:alpine"); }).catch((e) => alert(e.message)); };
  const selectedType = resourceTypes.find((option) => option.value === resourceType);
  const TypeIcon = selectedType.icon;
  const configure = (event) => { event.preventDefault(); setStep((current) => current + 1); };
  return <Dialog open={open} onOpenChange={(next) => { setOpen(next); if (next) setStep(1); }}><DialogContent className="ui-dialog resource-dialog"><DialogHeader className="resource-dialog-header"><div className="resource-dialog-topline"><div className="eyebrow">New resource</div><span className="resource-step-count">Step {step} of 3</span></div><DialogTitle className="resource-dialog-title">{step === 1 ? "Add a resource" : step === 2 ? "Choose a source" : `Configure ${selectedType?.label.toLowerCase()}`}</DialogTitle><DialogDescription className="resource-dialog-description">{step === 1 ? `Choose what you want to add to ${environment}.` : step === 2 ? `Choose how this ${selectedType?.label.toLowerCase()} will be provided.` : `Set up the ${selectedType?.label.toLowerCase()} you want to run in ${environment}.`}</DialogDescription><div className="resource-progress" aria-label={`Step ${step} of 3`}><span className="resource-progress-step resource-progress-step-active">1 <small>Type</small></span><span className={cn("resource-progress-line", step >= 2 && "resource-progress-line-active")} /><span className={cn("resource-progress-step", step >= 2 && "resource-progress-step-active")}>2 <small>Source</small></span><span className={cn("resource-progress-line", step === 3 && "resource-progress-line-active")} /><span className={cn("resource-progress-step", step === 3 && "resource-progress-step-active")}>3 <small>Details</small></span></div></DialogHeader><form onSubmit={step < 3 ? configure : submit}>{step === 1 ? <div className="resource-step"><div className="resource-choice-label">Resource type</div><div className="resource-choice-grid" role="radiogroup" aria-label="Resource type">{resourceTypes.map((option) => <ResourceChoice key={option.value} option={option} selected={resourceType === option.value} onSelect={selectResourceType} />)}</div></div> : step === 2 ? <div className="resource-step"><div className="resource-selection-summary"><span className="resource-choice-icon"><TypeIcon size={16} /></span><div><strong>{selectedType.label}</strong><small>{selectedType.description}</small></div><button type="button" onClick={() => setStep(1)}>Change</button></div><div className="resource-choice-section"><div className="resource-choice-label">{resourceType === "service" ? "Service source" : resourceType === "database" ? "Database engine" : "Bucket provider"}</div><div className="resource-choice-grid resource-choice-grid-options" role="radiogroup" aria-label={resourceType === "service" ? "Service source" : resourceType === "database" ? "Database engine" : "Bucket provider"}>{resourceOptions[resourceType].map((option) => <ResourceChoice key={option.value} option={option} selected={kind === option.value} onSelect={setKind} />)}</div></div></div> : <div className="resource-step"><div className="resource-selection-summary"><span className="resource-choice-icon"><TypeIcon size={16} /></span><div><strong>{selectedType.label}</strong><small>{selectedType.description}</small></div><button type="button" onClick={() => setStep(1)}>Change</button></div>{kind !== "app" && <ShadcnLabel className="field-label">Name<Input required value={name} onChange={(e) => setName(e.target.value)} placeholder={resourceType === "database" ? "database" : "assets"} /></ShadcnLabel>}{kind === "app" && <ShadcnLabel className="field-label">Container image<Input required value={image} onChange={(e) => setImage(e.target.value)} placeholder="ghcr.io/example/api:latest" /></ShadcnLabel>}<div className="resource-config-note">You can adjust this resource later from its settings page.</div></div>}<DialogFooter className="resource-dialog-footer">{step === 1 ? <><Button type="button" variant="outline" onClick={() => setOpen(false)}>Cancel</Button><Button type="submit">Continue <ArrowRight size={15} /></Button></> : step === 2 ? <><Button type="button" variant="outline" onClick={() => setStep(1)}><ArrowLeft size={15} /> Back</Button><Button type="submit">Continue <ArrowRight size={15} /></Button></> : <><Button type="button" variant="outline" onClick={() => setStep(2)}><ArrowLeft size={15} /> Back</Button><Button type="submit">Create resource <ArrowRight size={15} /></Button></>}</DialogFooter></form></DialogContent></Dialog>;
}

function ResourceCommandItem({ option, selected, onSelect }) {
  const Icon = option.icon;
  return <button type="button" role="option" aria-selected={selected} disabled={option.disabled} className={cn("resource-command-item", selected && "resource-command-item-selected", option.disabled && "resource-command-item-disabled")} onClick={() => onSelect(option.value)}><span className="resource-command-item-icon"><Icon size={18} /></span><span className="resource-command-item-copy"><strong>{option.label}</strong><small>{option.description}</small></span>{option.note && <span className="resource-command-item-note">{option.note}</span>}<ChevronRight className="resource-command-item-arrow" size={16} /></button>;
}

function ResourceDialog({ project, environment }) {
  const queryClient = useQueryClient();
  const [open, setOpen] = useState(false);
  const [step, setStep] = useState(1);
  const [resourceType, setResourceType] = useState("service");
  const [kind, setKind] = useState("app");
  const [name, setName] = useState("");
  const [image, setImage] = useState("nginx:alpine");
  const resourceTypes = [
    { value: "service", label: "Service", description: "Run an application from a Docker image", icon: Box },
    { value: "database", label: "Database", description: "PostgreSQL, Redis, and more", icon: Database },
    { value: "bucket", label: "Bucket", description: "Keep files and assets", icon: HardDrive },
  ];
  const resourceOptions = {
    service: [
      { value: "app", label: "Docker image", description: "Deploy from a container registry", icon: Box },
      { value: "github", label: "GitHub repository", description: "Deploy from a repository", note: "Coming soon", icon: GitBranch, disabled: true },
    ],
    database: [
      { value: "database", label: "PostgreSQL", description: "Managed relational database", icon: Database },
      { value: "cache", label: "Redis", description: "Managed in-memory store", icon: Network },
    ],
    bucket: [{ value: "store", label: "Object storage", description: "S3-compatible bucket", icon: HardDrive }],
  };
  useEffect(() => { const openDialog = () => { setStep(1); setOpen(true); }; window.addEventListener("geass:open-resource", openDialog); return () => window.removeEventListener("geass:open-resource", openDialog); }, []);
  const selectedType = resourceTypes.find((option) => option.value === resourceType);
  const selectedOptions = resourceOptions[resourceType];
  const selectResourceType = (nextType) => { setResourceType(nextType); setKind(resourceOptions[nextType].find((option) => !option.disabled).value); setStep(2); };
  const selectKind = (nextKind) => { setKind(nextKind); setStep(3); };
  const submit = (event) => { event.preventDefault(); const endpoint = { app: "/apps/create", database: "/databases/create", cache: "/caches/create", store: "/object-stores/create" }[kind]; const values = kind === "app" ? { project, environment, image, port: "80" } : { name, project, environment }; action(endpoint, values).then(() => { queryClient.invalidateQueries({ queryKey: ["dashboard", "bootstrap"] }); setOpen(false); setStep(1); setName(""); setImage("nginx:alpine"); }).catch((error) => alert(error.message)); };
  const back = () => setStep((current) => Math.max(1, current - 1));
  const sourcePlaceholder = resourceType === "database" ? "Choose a database..." : resourceType === "bucket" ? "Choose a storage provider..." : "Choose a service source...";
  return <Dialog open={open} onOpenChange={(next) => { setOpen(next); if (next) setStep(1); }}><DialogContent className="ui-dialog resource-command-dialog" showCloseButton={false}><div className="resource-command-shell">{step === 1 && <><div className="resource-command-search"><Sparkles size={17} /><input aria-label="Resource search" placeholder="Describe your project or paste a repo link" /></div><div className="resource-command-list" role="listbox" aria-label="Resource type">{resourceTypes.map((option, index) => <ResourceCommandItem key={option.value} option={option} selected={index === 0} onSelect={selectResourceType} />)}</div></>}{step === 2 && <><div className="resource-command-search"><button type="button" className="resource-command-back" onClick={back} aria-label="Back"><ArrowLeft size={17} /></button><input aria-label="Resource source" placeholder={sourcePlaceholder} readOnly /></div><div className="resource-command-list" role="listbox" aria-label={sourcePlaceholder.replace("Choose a ", "").replace("...", "")}>{selectedOptions.map((option, index) => <ResourceCommandItem key={option.value} option={option} selected={index === 0} onSelect={selectKind} />)}</div></>}{step === 3 && <form className="resource-command-form" onSubmit={submit}><div className="resource-command-search"><button type="button" className="resource-command-back" onClick={back} aria-label="Back"><ArrowLeft size={17} /></button><Input autoFocus aria-label={kind === "app" ? "Container image" : "Resource name"} value={kind === "app" ? image : name} onChange={(event) => kind === "app" ? setImage(event.target.value) : setName(event.target.value)} placeholder={kind === "app" ? "ghcr.io/example/api:latest" : resourceType === "database" ? "Database name" : "Bucket name"} required /></div>{kind === "app" ? <><div className="resource-command-callout"><span>i</span><span>Enter a Docker image from a supported registry</span></div><div className="resource-command-examples"><strong>Examples</strong><ul><li>hello-world</li><li>ghcr.io/username/repo:latest</li><li>quay.io/username/repo:tag</li><li>registry.gitlab.com/username/repo:tag</li><li>mcr.microsoft.com/username/repo:tag</li></ul></div></> : <p className="resource-command-note">{selectedType.description}. You can adjust this resource later from its settings page.</p>}<button className="resource-command-submit" type="submit">Create resource</button></form>}</div></DialogContent></Dialog>;
}

function ResourceList({ kind, data }) { const navigate = useNavigate(); const config = { apps: ["Services", "Run production workloads", "apps", Box], databases: ["Databases", "Managed PostgreSQL resources", "databases", Database], "logical-databases": ["Logical databases", "Databases provisioned inside managed PostgreSQL servers", "logicalDatabases", Database], caches: ["Caches", "Managed Redis resources", "caches", Network], "object-stores": ["Object storage", "S3-compatible storage resources", "objectStores", HardDrive] }[kind]; const items = list(data, config[2]); return <><PageHeader eyebrow="Resources" title={config[0]} description={config[1]} actions={<Button onClick={() => navigate({ to: "/projects" })}><Plus size={16} /> Add resource</Button>} /><Card className="table-card">{items.length ? <Table headers={["Name", "Project", "Environment", "Engine", "Status", ""]}>{items.map((item) => <tr key={resourceName(item)}><td><AppLink className="table-primary" href={`/projects/${item.spec?.project}?resource=${kind}/${resourceName(item)}&view=overview`}>{resourceName(item)}</AppLink></td><td>{item.spec?.project || "—"}</td><td><Badge>{item.spec?.environment || "—"}</Badge></td><td>{item.spec?.engine || item.spec?.databaseName || "—"}</td><td><Status value={condition(item)} /></td><td><ArrowRight size={16} /></td></tr>)}</Table> : <Empty icon={config[3]} title={`No ${config[0].toLowerCase()} yet`} description="Create a resource from a project workspace." action={<Button onClick={() => navigate({ to: "/projects" })}>Open projects</Button>} />}</Card></>; }

function ResourceDetail({ project, data, kind, name, view = "overview", reload }) { const navigate = useNavigate(); const key = { apps: "apps", databases: "databases", caches: "caches", "object-stores": "objectStores" }[kind]; const item = list(data, key).find((x) => resourceName(x) === name); if (!item) return <Empty title="Resource not found" description="The resource may have been deleted or is still being reconciled." action={<Button onClick={() => navigate({ to: `/projects/${resourceName(project)}` })}>Back to workspace</Button>} />; const title = resourceName(item); const isApp = kind === "apps"; return <><div className="breadcrumb"><AppLink href={`/projects/${resourceName(project)}`}><ArrowLeft size={15} /> {project.spec?.displayName || resourceName(project)}</AppLink><span>/</span><strong>{title}</strong></div><PageHeader eyebrow={`${kind === "apps" ? "Service" : kind.replace("-", " ")}`} title={title} description={`Managed ${kind === "apps" ? "application" : "resource"} in ${item.spec?.environment}.`} actions={<><Status value={condition(item)} /><Button variant="outline" onClick={() => navigate({ to: `/projects/${resourceName(project)}?resource=${kind}/${title}&view=settings` })}><Settings size={16} /> Settings</Button></>} /><div className="tabs"><AppLink className={view === "overview" ? "tab-active" : ""} href={`/projects/${resourceName(project)}?resource=${kind}/${title}&view=overview`}>Overview</AppLink>{isApp && <><AppLink className={view === "logs" ? "tab-active" : ""} href={`/projects/${resourceName(project)}?resource=${kind}/${title}&view=logs`}>Logs</AppLink><AppLink className={view === "metrics" ? "tab-active" : ""} href={`/projects/${resourceName(project)}?resource=${kind}/${title}&view=metrics`}>Metrics</AppLink><AppLink className={view === "deployments" ? "tab-active" : ""} href={`/projects/${resourceName(project)}?resource=${kind}/${title}&view=deployments`}>Deployments</AppLink><AppLink className={view === "variables" ? "tab-active" : ""} href={`/projects/${resourceName(project)}?resource=${kind}/${title}&view=variables`}>Variables</AppLink></>}<AppLink className={view === "settings" ? "tab-active" : ""} href={`/projects/${resourceName(project)}?resource=${kind}/${title}&view=settings`}>Settings</AppLink></div>{view === "settings" ? <ResourceSettings item={item} kind={kind} reload={reload} /> : <ResourceOverview item={item} kind={kind} view={view} />}</>; }
function ResourceOverview({ item, kind, view }) { const queryClient = useQueryClient(); if (["logs", "metrics", "deployments", "variables"].includes(view)) return <Card className="empty-state"><Activity size={25} /><h3>{view[0].toUpperCase() + view.slice(1)} stream</h3><p>Live {view} are available through the operator and will appear here as the resource reports activity.</p><Button variant="outline" onClick={() => queryClient.invalidateQueries({ queryKey: ["dashboard", "bootstrap"] })}><RefreshCw size={15} /> Refresh</Button></Card>; const sourceImage = typeof item.spec?.source?.image === "string" ? item.spec.source.image : item.spec?.source?.image?.image; return <div className="detail-grid"><Card><div className="card-heading"><div><div className="eyebrow">Resource status</div><h2>Runtime overview</h2></div><Status value={condition(item)} /></div><div className="detail-list"><Detail label="Project" value={item.spec?.project} /><Detail label="Environment" value={item.spec?.environment} /><Detail label="Engine" value={item.spec?.engine || sourceImage || "—"} /><Detail label="Target namespace" value={item.status?.targetNamespace || "Pending reconciliation"} /></div></Card><Card><div className="card-heading"><div><div className="eyebrow">Connection</div><h2>Managed endpoint</h2></div><Network size={18} /></div><div className="connection-box"><code>{item.status?.host || item.status?.endpoint || "Endpoint will appear when ready"}</code><Button variant="outline" size="sm" disabled={!item.status?.host && !item.status?.endpoint}><ExternalLink size={14} /> Copy</Button></div></Card></div>; }
function Detail({ label, value }) { return <div className="detail-row"><span>{label}</span><strong>{value || "—"}</strong></div>; }
function ResourceSettings({ item, kind, reload }) { const navigate = useNavigate(); const [environment, setEnvironment] = useState(item.spec?.environment || ""); const endpoint = `/${kind}/${resourceName(item)}/update`; const submit = (e) => { e.preventDefault(); action(endpoint, { project: item.spec?.project || "", environment }).then(() => { reload(); alert("Settings saved"); }).catch((err) => alert(err.message)); }; return <Card><div className="card-heading"><div><div className="eyebrow">Configuration</div><h2>Resource settings</h2></div></div><form className="form-grid" onSubmit={submit}><ShadcnLabel className="field-label">Project<Input value={item.spec?.project || ""} readOnly /></ShadcnLabel><ShadcnLabel className="field-label">Environment<Select value={environment} onChange={(e) => setEnvironment(e.target.value)}><option>{environment}</option></Select></ShadcnLabel>{kind === "databases" && <ShadcnLabel className="field-label">PostgreSQL version<Input defaultValue={item.spec?.version || "16"} name="version" /></ShadcnLabel>}<div className="form-actions"><Button type="submit">Save changes</Button><Button type="button" variant="danger" onClick={() => { if (confirm(`Delete ${resourceName(item)}?`)) action(`/${kind}/${resourceName(item)}/delete`).then(() => navigate({ to: "/projects" })); }}> <Trash2 size={15} /> Delete resource</Button></div></form></Card>; }

function PlatformSettings({ data, page = "general", reload }) { const config = data?.platformConfig?.items?.[0]; if (page === "domain") return <DomainSettings config={config} reload={reload} />; if (page === "github") return <GitHubSettings config={config} />; if (page === "ha") return <Readiness data={data} />; if (page === "cloud") return <CloudConnections data={data} />; return <><PageHeader eyebrow="Platform" title="General settings" description="Configure the Geass control plane and inspect cluster health." /><Card><div className="card-heading"><div><div className="eyebrow">Cluster overview</div><h2>Control plane</h2></div><Button variant="outline" onClick={reload}><RefreshCw size={15} /> Refresh</Button></div><div className="detail-grid"><Detail label="Clusters" value={list(data, "clusters").length} /><Detail label="Dashboard URL" value={config?.items?.[0]?.spec?.dashboardURL || config?.spec?.dashboardURL || "Not configured"} /><Detail label="Prometheus" value={config?.spec?.prometheusURL || "In-cluster default"} /></div></Card><Card><div className="setting-list"><SettingLink href="/settings/domain" icon={Network} title="Domain" description="Configure exposure and DNS verification." /><SettingLink href="/settings/github" icon={GitBranch} title="GitHub App" description="Connect repositories for source-based deploys." /><SettingLink href="/ha-readiness" icon={ShieldCheck} title="HA readiness" description="Check storage, nodes, and add-ons." /><SettingLink href="/cloud-connections" icon={Cloud} title="Cloud connections" description="Manage external cloud provider adapters." /></div></Card></>; }
function SettingLink({ href, icon: Icon, title, description }) { return <AppLink className="setting-row" href={href}><div className="setting-icon"><Icon size={17} /></div><div><strong>{title}</strong><small>{description}</small></div><ArrowRight size={16} /></AppLink>; }
function DomainSettings({ config, reload }) { const [domain, setDomain] = useState(config?.spec?.rootDomain || ""); const [exposure, setExposure] = useState(config?.spec?.dashboardExposure || "ingress"); return <><PageHeader eyebrow="Platform / Settings" title="Domain" description="Choose how Geass is exposed and verify the public dashboard endpoint." /><Card><form className="form-grid" onSubmit={(e) => { e.preventDefault(); action("/settings/domain/save", { domain, exposure, tunnelCNAMETarget: config?.spec?.tunnelCNAMETarget || "" }).then(() => { reload(); alert("Domain settings saved"); }); }}><ShadcnLabel className="field-label">Your domain<Input required value={domain} onChange={(e) => setDomain(e.target.value)} placeholder="example.com" /></ShadcnLabel><ShadcnLabel className="field-label">Exposure<Select value={exposure} onChange={(e) => setExposure(e.target.value)}><option value="ingress">Server (A record)</option><option value="cloudflare-tunnel">Local + Cloudflare Tunnel</option></Select></ShadcnLabel><p className="form-help">Geass serves the dashboard at <code>geass.{domain || "example.com"}</code> after DNS verification.</p><div className="form-actions"><Button type="submit">Save domain</Button><Button type="button" variant="outline" onClick={() => action("/settings/domain/verify").then(() => { reload(); alert("Verification requested"); })}><ShieldCheck size={15} /> Verify DNS</Button></div></form></Card></>; }
function GitHubSettings({ config, reload }) { const queryClient = useQueryClient(); const [values, setValues] = useState({ appID: "", clientID: "", slug: "", clientSecret: "", webhookSecret: "", privateKey: "" }); const save = (event) => { event.preventDefault(); action("/settings/github/save", values).then(() => { reload(); alert("GitHub App credentials saved"); }).catch((error) => alert(error.message)); }; return <><PageHeader eyebrow="Platform / Settings" title="GitHub App" description="Connect Geass to GitHub for repository deploys." /><Card><div className="callout"><GitBranch size={20} /><div><strong>{config?.spec?.githubAppRef ? "GitHub App configured" : "GitHub App not configured"}</strong><p>Credentials are stored in Kubernetes Secrets. Existing secret values remain masked; leave a field blank to keep it unchanged.</p></div></div><form className="form-grid" onSubmit={save}><ShadcnLabel className="field-label">App ID<Input required value={values.appID} onChange={(e) => setValues({ ...values, appID: e.target.value })} placeholder="123456" /></ShadcnLabel><ShadcnLabel className="field-label">Client ID<Input required value={values.clientID} onChange={(e) => setValues({ ...values, clientID: e.target.value })} placeholder="Iv1.abcdef" /></ShadcnLabel><ShadcnLabel className="field-label">App slug<Input required value={values.slug} onChange={(e) => setValues({ ...values, slug: e.target.value })} placeholder="geass" /></ShadcnLabel><ShadcnLabel className="field-label">Client secret<Input type="password" value={values.clientSecret} onChange={(e) => setValues({ ...values, clientSecret: e.target.value })} placeholder="Leave blank to keep current" /></ShadcnLabel><ShadcnLabel className="field-label">Webhook secret<Input type="password" value={values.webhookSecret} onChange={(e) => setValues({ ...values, webhookSecret: e.target.value })} placeholder="Leave blank to keep current" /></ShadcnLabel><ShadcnLabel className="field-label">Private key (PEM)<ShadcnTextarea rows="7" value={values.privateKey} onChange={(e) => setValues({ ...values, privateKey: e.target.value })} placeholder="Leave blank to keep current private key" /></ShadcnLabel><div className="form-actions"><Button type="submit">Save GitHub App</Button><Button type="button" variant="outline" onClick={() => queryClient.invalidateQueries({ queryKey: ["dashboard", "bootstrap"] })}>Refresh status</Button></div></form></Card></>; }
function Readiness({ data }) { const queryClient = useQueryClient(); return <><PageHeader eyebrow="Platform / Settings" title="HA readiness" description="Validate the control plane before relying on production workloads." /><Card><div className="signal-list"><Signal icon={Server} label="Clusters" value={`${list(data, "clusters").length} discovered`} tone="success" /><Signal icon={HardDrive} label="Storage" value="Controller managed" /><Signal icon={ShieldCheck} label="Readiness checks" value="Run from operator" tone="warning" /></div><Button onClick={() => action("/ha-readiness/check").then(() => queryClient.invalidateQueries({ queryKey: ["dashboard", "bootstrap"] }))}><RefreshCw size={15} /> Run readiness check</Button></Card></>; }
function CloudConnections({ data }) { const navigate = useNavigate(); const items = list(data, "cloudConnections"); return <><PageHeader eyebrow="Platform / Settings" title="Cloud connections" description="External provider adapters are managed independently from project resources." actions={<Button onClick={() => navigate({ to: "/cloud-connections/new" })}><Plus size={15} /> New connection</Button>} /><Card>{items.length ? <div className="setting-list">{items.map((item) => <div className="setting-row" key={resourceName(item)}><div className="setting-icon"><Cloud size={17} /></div><div><strong>{resourceName(item)}</strong><small>{item.spec?.provider || "Provider"}</small></div><Badge tone="warning">Unavailable</Badge></div>)}</div> : <Empty icon={Cloud} title="No cloud connections" description="AWS credentials and adapters are unavailable in this release." action={<Button onClick={() => navigate({ to: "/cloud-connections/new" })}>Add connection</Button>} />}</Card></>; }
function UtilityPage({ title, description, icon: Icon = CircleHelp, data, kind }) { const navigate = useNavigate(); const clusters = list(data, "clusters"); if (kind === "cluster") return <><PageHeader eyebrow="Infrastructure" title={title} description={description} /><div className="resource-grid">{clusters.length ? clusters.map((cluster) => <Card key={resourceName(cluster)}><div className="card-heading"><div><div className="eyebrow">Cluster</div><h2>{resourceName(cluster)}</h2></div><Status value={condition(cluster)} /></div><div className="detail-list"><Detail label="Namespace" value={cluster.metadata?.namespace || "default"} /><Detail label="Phase" value={cluster.status?.phase || "Pending"} /><Detail label="Add-ons" value={cluster.status?.conditions?.find((item) => item.type === "AddonsReady")?.status || "Pending"} /></div></Card>) : <Empty icon={Icon} title="No clusters found" description="Create a GeassCluster to see control-plane capacity here." />}</div></>; if (kind === "observability") return <><PageHeader eyebrow="Infrastructure" title={title} description={description} /><div className="stat-grid">{(data?.metrics || []).map((metric) => <Card key={metric.title}><div className="stat-icon"><Activity size={18} /></div><div className="stat-value">{metric.value}</div><div className="stat-label">{metric.title}</div><div className="stat-detail">{metric.state}</div></Card>)}</div><Card><div className="callout"><Gauge size={19} /><div><strong>Prometheus-backed signals</strong><p>Values refresh when you reload the dashboard and are queried from the configured metrics service.</p></div></div></Card></>; return <><PageHeader eyebrow="Geass" title={title} description={description} /><Card><div className="callout"><Icon size={20} /><div><strong>Build with Geass</strong><p>Projects contain isolated environments. Add services, databases, caches, and object storage from a project workspace.</p></div></div><div className="page-actions"><Button onClick={() => navigate({ to: "/projects" })}>Open projects <ArrowRight size={15} /></Button><Button variant="outline" onClick={() => navigate({ to: "/settings" })}>Platform settings</Button></div></Card></>; }

const projectSearch = (search) => {
  const value = (key) => typeof search[key] === "string" && search[key] ? search[key] : undefined;
  return { environment: value("environment"), panel: value("panel"), resource: value("resource"), view: value("view") };
};

function DashboardScreen({ mode }) {
  const { data, error, isPending, refetch } = useBootstrap();
  const navigate = useNavigate();
  const routeState = useRouterState({ select: (state) => ({ location: state.location, params: state.matches.at(-1)?.params || {} }) });
  const path = routeState.location.pathname;
  const search = routeState.location.search || {};
  const params = routeState.params;
  if (error) return <div className="error-screen"><AlertTriangle size={24} /><h1>Dashboard unavailable</h1><p>{error.message}</p><Button onClick={() => refetch()}>Try again</Button></div>;
  if (isPending || !data) return <div className="loading-screen"><div className="spinner" />Loading Geass…</div>;

  const projects = list(data, "projects");
  const directKind = mode.startsWith("resource-detail:") ? mode.slice("resource-detail:".length) : "";
  const projectName = mode === "project" || mode.startsWith("project:") ? params.projectName : directKind ? params.name : "";
  const directItems = directKind ? list(data, { apps: "apps", databases: "databases", "logical-databases": "logicalDatabases", caches: "caches", "object-stores": "objectStores" }[directKind]) : [];
  const directItem = directItems.find((item) => resourceName(item) === projectName);
  const project = projects.find((item) => resourceName(item) === (projectName || directItem?.spec?.project));
  let title = "Dashboard";
  let content;
  let environment;
  let setEnvironment;

  if (mode === "projects") {
    title = "Projects";
    content = <Projects data={data} />;
  } else if ((mode === "project" || mode.startsWith("project:")) && project) {
    title = project.spec?.displayName || resourceName(project);
    environment = search.environment || project.spec?.environments?.[0] || "";
    setEnvironment = (next) => navigate({ search: (previous) => ({ ...previous, environment: next || undefined }) });
    const panel = mode.startsWith("project:") ? mode.slice("project:".length) : search.panel;
    content = panel ? <ProjectPanel project={project} data={data} panel={panel} environment={environment} reload={refetch} /> : <ProjectCanvas project={project} data={data} environment={environment} setEnvironment={setEnvironment} search={search} reload={refetch} />;
  } else if (directKind && directItem && project) {
    title = project.spec?.displayName || resourceName(project);
    content = <ResourceDetail project={project} data={data} kind={directKind} name={resourceName(directItem)} view={search.view || "overview"} reload={refetch} />;
  } else if (mode.startsWith("resource-list:")) {
    const kind = mode.slice("resource-list:".length);
    title = kind;
    content = <ResourceList kind={kind} data={data} />;
  } else if (mode === "settings" || mode.startsWith("settings:")) {
    const page = mode.split(":")[1] || "general";
    title = page === "general" ? "Settings" : page === "github" ? "GitHub App" : page === "ha" ? "HA readiness" : page === "cloud" ? "Cloud connections" : "Domain";
    content = <PlatformSettings data={data} page={page} reload={refetch} />;
  } else if (mode === "cluster") {
    title = "Cluster";
    content = <UtilityPage title="Clusters" description="Cluster capacity and node health for the Geass control plane." icon={Server} data={data} kind="cluster" />;
  } else if (mode === "observability") {
    title = "Observability";
    content = <UtilityPage title="Observability" description="Platform-wide health signals from Kubernetes and Prometheus." icon={Activity} data={data} kind="observability" />;
  } else if (mode === "docs") {
    title = "Docs";
    content = <UtilityPage title="Docs" description="Build with Geass using projects and isolated environments." data={data} />;
  } else {
    title = "Page not found";
    content = <UtilityPage title="Page not found" description="The requested dashboard page does not exist." data={data} />;
  }

  return <Layout title={title} project={project} environment={environment} setEnvironment={setEnvironment} fullHeight={mode === "project" && !search.panel}>{content}</Layout>;
}

const rootRoute = createRootRoute({ component: () => <Outlet /> });
const route = (path, mode, validateSearch) => createRoute({ getParentRoute: () => rootRoute, path, validateSearch, component: () => <DashboardScreen mode={mode} /> });
const routeTree = rootRoute.addChildren([
  createRoute({ getParentRoute: () => rootRoute, path: "/", component: () => <DashboardScreen mode="projects" /> }),
  route("/projects", "projects"),
  route("/projects/$projectName/logs", "project:logs", projectSearch),
  route("/projects/$projectName/observability", "project:observability", projectSearch),
  route("/projects/$projectName/settings", "project:settings", projectSearch),
  route("/projects/$projectName", "project", projectSearch),
  route("/apps", "resource-list:apps"),
  route("/apps/$name", "resource-detail:apps", projectSearch),
  route("/databases", "resource-list:databases"),
  route("/databases/$name", "resource-detail:databases", projectSearch),
  route("/logical-databases", "resource-list:logical-databases"),
  route("/logical-databases/$name", "resource-detail:logical-databases", projectSearch),
  route("/caches", "resource-list:caches"),
  route("/caches/$name", "resource-detail:caches", projectSearch),
  route("/object-stores", "resource-list:object-stores"),
  route("/object-stores/$name", "resource-detail:object-stores", projectSearch),
  route("/settings", "settings"),
  route("/settings/domain", "settings:domain"),
  route("/settings/github", "settings:github"),
  route("/ha-readiness", "settings:ha"),
  route("/cloud-connections", "settings:cloud"),
  route("/cloud-connections/new", "settings:cloud"),
  route("/cluster", "cluster"),
  route("/observability", "observability"),
  route("/docs", "docs"),
]);
const router = createRouter({ routeTree, defaultPreload: "intent" });
const queryClient = new QueryClient();

createRoot(document.getElementById("root")).render(
  <QueryClientProvider client={queryClient}>
    <RouterProvider router={router} />
    <NoticeHost />
  </QueryClientProvider>
);
