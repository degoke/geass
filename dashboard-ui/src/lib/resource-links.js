import { resourceName } from "@/lib/api";

export function appHref(project, name, environment) {
  return `/projects/${project}/apps/${name}?environment=${environment}`;
}

export function resourceHref(project, environment, item, kind) {
  const slug = typeof project === "string" ? project : resourceName(project);
  const name = resourceName(item);
  const engine = item.spec?.engine;
  if (kind === "Service") return `/projects/${slug}/apps/${name}?environment=${environment}`;
  if (kind === "Logical DB") return `/projects/${slug}/logical-databases/${name}?environment=${environment}`;
  if (engine || kind === "Database" || kind === "Bucket" || kind === "Redis") {
    const segment = item.spec?.engine === "MinIO" || item.spec?.engine === "S3" ? "object-stores" : "databases";
    return `/projects/${slug}/${segment}/${name}?environment=${environment}`;
  }
  return `/projects/${slug}`;
}
