import { Link } from "@tanstack/react-router";

export function ProjectSubnav({ projectSlug, environment, active }) {
  if (!projectSlug) return null;
  const search = environment ? { environment } : undefined;
  const items = [
    { id: "resources", label: "Resources", to: `/projects/${projectSlug}` },
    { id: "monitor", label: "Metrics", to: `/projects/${projectSlug}/monitor` },
    { id: "settings", label: "Settings", to: `/projects/${projectSlug}/settings` },
  ];
  return (
    <div className="cf-tabs">
      {items.map((item) => (
        <Link
          key={item.id}
          to={item.to}
          search={search}
          activeOptions={{ exact: true }}
          className={active === item.id ? "cf-tab cf-tab-on" : "cf-tab"}
        >
          {item.label}
        </Link>
      ))}
    </div>
  );
}
