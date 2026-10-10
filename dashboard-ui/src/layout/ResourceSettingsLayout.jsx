import { useEffect, useMemo, useState } from "react";
import { SettingsRailContext } from "@/layout/SettingsShell";
import { Input } from "@/components/geass-ui";

function matchesFilter(query, parts) {
  const q = String(query || "").trim().toLowerCase();
  if (!q) return true;
  return parts.some((part) => String(part || "").toLowerCase().includes(q));
}

export function ResourceSettingsLayout({ sections, children }) {
  const [filter, setFilter] = useState("");
  const visible = useMemo(
    () => sections.filter((section) => matchesFilter(filter, section.keywords || [section.label])),
    [sections, filter],
  );

  useEffect(() => {
    const onKeyDown = (event) => {
      if (event.key !== "/" || event.metaKey || event.ctrlKey || event.altKey) return;
      const tag = event.target?.tagName?.toLowerCase();
      if (tag === "input" || tag === "textarea" || tag === "select" || event.target?.isContentEditable) return;
      event.preventDefault();
      document.getElementById("rw-resource-settings-filter")?.focus();
    };
    window.addEventListener("keydown", onKeyDown);
    return () => window.removeEventListener("keydown", onKeyDown);
  }, []);

  return (
    <SettingsRailContext.Provider value={true}>
      <div className="rw-settings-shell rw-rail-settings rw-resource-settings-shell">
        <div className="rw-resource-settings-filter">
          <Input
            id="rw-resource-settings-filter"
            placeholder="Filter settings…"
            value={filter}
            onChange={(event) => setFilter(event.currentTarget.value)}
            aria-label="Filter settings"
          />
          <span className="rw-resource-settings-filter-hint" aria-hidden="true">/</span>
        </div>
        <div className="rw-resource-settings-body">
          <div className="rw-resource-settings-main">{children(visible, filter)}</div>
          <SectionAnchors sections={visible} />
        </div>
      </div>
    </SettingsRailContext.Provider>
  );
}

function SectionAnchors({ sections }) {
  const [active, setActive] = useState("");
  const ids = sections.map((section) => section.id).join(",");

  useEffect(() => {
    if (typeof IntersectionObserver === "undefined") return undefined;
    const targets = ids.split(",").map((id) => document.getElementById(id)).filter(Boolean);
    if (!targets.length) return undefined;
    const observer = new IntersectionObserver(
      (entries) => {
        const visibleEntry = entries.filter((entry) => entry.isIntersecting).sort((a, b) => a.boundingClientRect.top - b.boundingClientRect.top)[0];
        if (visibleEntry) setActive(visibleEntry.target.id);
      },
      { rootMargin: "-10% 0px -70% 0px" },
    );
    targets.forEach((target) => observer.observe(target));
    return () => observer.disconnect();
  }, [ids]);

  if (sections.length < 2) return null;
  return (
    <nav className="rw-resource-settings-anchors" aria-label="Settings sections">
      {sections.map((section) => (
        <button
          key={section.id}
          type="button"
          className={`rw-resource-settings-anchor${(active || sections[0].id) === section.id ? " is-active" : ""}${section.danger ? " is-danger" : ""}`}
          onClick={() => {
            setActive(section.id);
            document.getElementById(section.id)?.scrollIntoView({ behavior: "smooth", block: "start" });
          }}
        >
          {section.label}
        </button>
      ))}
    </nav>
  );
}

export function ResourceSettingsSection({ id, icon: Icon, title, children, hidden }) {
  if (hidden) return null;
  return (
    <section id={id} className="rw-resource-settings-section">
      <div className="rw-resource-settings-section-rail" aria-hidden="true">
        <div className="rw-resource-settings-section-icon">
          {Icon ? <Icon size={15} strokeWidth={1.75} /> : null}
        </div>
      </div>
      <div className="rw-resource-settings-section-content">
        <h2 className="rw-resource-settings-section-heading">{title}</h2>
        {children}
      </div>
    </section>
  );
}

export function ResourceSettingsBlock({ title, description, children }) {
  return (
    <div className="rw-resource-settings-block">
      {title ? <h3 className="rw-settings-subtitle">{title}</h3> : null}
      {description ? <p className="rw-rail-body">{description}</p> : null}
      {children}
    </div>
  );
}

export function resourceSectionVisible(filter, keywords) {
  return matchesFilter(filter, keywords);
}
