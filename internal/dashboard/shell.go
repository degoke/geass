package dashboard

import (
	"fmt"
	"html/template"
	"net/url"
	"strings"
)

func activeNavForPath(path string) string {
	switch {
	case path == "/" || strings.HasPrefix(path, "/projects"):
		return "projects"
	case strings.HasPrefix(path, "/ha-readiness") || strings.HasPrefix(path, "/cloud-connections") || strings.HasPrefix(path, "/object-storage") || strings.HasPrefix(path, "/cluster") || strings.HasPrefix(path, "/settings/"):
		return "settings"
	case isSettingsPath(path):
		return "settings"
	default:
		return ""
	}
}

func navItem(href, label, icon, activeNav, id string) string {
	return navItemActive(href, label, icon, activeNav == id)
}

func navItemActive(href, label, icon string, active bool) string {
	activeClass := ""
	if active {
		activeClass = " nav-item-active"
	}
	return fmt.Sprintf(`<a class="nav-item%s" href="%s" aria-label="%s">%s<span class="nav-tooltip" role="tooltip">%s</span></a>`, activeClass, href, template.HTMLEscapeString(label), icon, template.HTMLEscapeString(label))
}

type shellContext struct {
	title        string
	path         string
	project      string
	displayName  string
	environment  string
	environments []string
	panel        string
}

func (ctx shellContext) inProject() bool {
	return ctx.project != ""
}

func appShell(ctx shellContext, body string) string {
	sidebar := globalSidebar(activeNavForPath(ctx.path))
	if ctx.inProject() {
		sidebar = projectSidebar(ctx)
	}

	return fmt.Sprintf(`<!DOCTYPE html>
<html data-theme="dark">
<head>
<meta charset="utf-8">
<meta name="viewport" content="width=device-width, initial-scale=1">
<title>%s</title>
<link rel="preconnect" href="https://fonts.googleapis.com">
<link rel="preconnect" href="https://fonts.gstatic.com" crossorigin>
<link href="https://fonts.googleapis.com/css2?family=Inter:wght@400;500;550;600&display=swap" rel="stylesheet">
<script src="https://unpkg.com/htmx.org@2.0.4"></script>
<style>%s</style>
</head>
<body>
<div class="app-shell">
%s
<div class="main-panel">
%s
<main class="main-content%s">%s</main>
</div>
<button class="sidebar-toggle" type="button" aria-label="Open navigation" id="sidebar-toggle">
	<svg width="20" height="20" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.5" aria-hidden="true"><path d="M4 6h16M4 12h16M4 18h16" stroke-linecap="round"/></svg>
</button>
</div>
<script>%s</script>
</body>
</html>`, template.HTMLEscapeString(ctx.title), geassStyles(), sidebar, topbarHTML(ctx), mainContentClass(ctx), body, shellScripts())
}

func mainContentClass(ctx shellContext) string {
	if ctx.inProject() && isProjectWorkspacePath(ctx.path) {
		return " main-content-canvas"
	}
	return ""
}

func isProjectWorkspacePath(path string) bool {
	if !strings.HasPrefix(path, "/projects/") {
		return false
	}
	rest := strings.Trim(strings.TrimPrefix(path, "/projects/"), "/")
	if rest == "" || rest == "new" || rest == "create" || rest == "options" {
		return false
	}
	return !strings.Contains(rest, "/")
}

func globalSidebar(activeNav string) string {
	primaryNav := strings.Join([]string{
		navItem("/projects", "Projects", navIconFolder(), activeNav, "projects"),
	}, "")
	secondaryNav := strings.Join([]string{
		navItem("/settings", "Settings", navIconSettings(), activeNav, "settings"),
	}, "")
	return fmt.Sprintf(`<aside class="sidebar" aria-label="Primary navigation">
	<div class="sidebar-top">
		<a href="/projects" class="sidebar-brand" aria-label="Geass home">%s<span class="nav-tooltip" role="tooltip">Geass</span></a>
		<nav class="sidebar-nav" aria-label="Projects">%s</nav>
	</div>
	<div class="sidebar-bottom">
		<nav class="sidebar-nav sidebar-nav-secondary" aria-label="Settings">%s</nav>
		%s
	</div>
</aside>`, geassLogoMark(), primaryNav, secondaryNav, themeToggleButton())
}

func projectSidebar(ctx shellContext) string {
	return fmt.Sprintf(`<aside class="sidebar sidebar-project" aria-label="Project navigation">
	<div class="sidebar-top">
		<a href="/projects" class="sidebar-brand" aria-label="All projects">%s<span class="nav-tooltip" role="tooltip">All projects</span></a>
		<nav class="sidebar-nav" aria-label="Project">%s</nav>
	</div>
	<div class="sidebar-bottom">
		%s
	</div>
</aside>`, geassLogoMark(), projectSidebarNav(ctx), themeToggleButton())
}

func projectSidebarNav(ctx shellContext) string {
	if !ctx.inProject() {
		return ""
	}
	base := "/projects/" + template.URLQueryEscaper(ctx.project)
	environment := ctx.environment
	if environment == "" && len(ctx.environments) > 0 {
		environment = ctx.environments[0]
	}
	workspaceQuery := sidebarPanelQuery(environment, "")
	logsQuery := sidebarPanelQuery(environment, "logs")
	obsQuery := sidebarPanelQuery(environment, "observability")
	settingsQuery := sidebarPanelQuery("", "settings")
	workspaceActive := ctx.panel == "" && !strings.Contains(ctx.path, "/settings")
	return strings.Join([]string{
		navItemActive(base+workspaceQuery, "Workspace", navIconGrid(), workspaceActive),
		navItemActive(base+logsQuery, "Logs", navIconDocs(), ctx.panel == "logs"),
		navItemActive(base+obsQuery, "Observability", navIconPulse(), ctx.panel == "observability"),
		navItemActive(base+settingsQuery, "Project settings", navIconSettings(), ctx.panel == "settings" || ctx.panel == "variables" || ctx.panel == "environments" || ctx.panel == "usage"),
	}, "")
}

func sidebarPanelQuery(environment, panel string) string {
	q := url.Values{}
	if environment != "" {
		q.Set("environment", environment)
	}
	if panel != "" {
		q.Set("panel", panel)
	}
	encoded := q.Encode()
	if encoded == "" {
		return ""
	}
	return "?" + encoded
}

func themeToggleButton() string {
	return `<button id="theme-toggle" class="nav-item nav-item-ghost" type="button" aria-label="Switch to light mode"><span class="theme-icon theme-icon-light">` + navIconSun() + `</span><span class="theme-icon theme-icon-dark">` + navIconMoon() + `</span><span class="nav-tooltip" id="theme-toggle-label" role="tooltip">Light mode</span></button>`
}

func navIconGrid() string {
	return `<svg width="16" height="16" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.5" aria-hidden="true"><rect x="3" y="3" width="7" height="7" rx="1"/><rect x="14" y="3" width="7" height="7" rx="1"/><rect x="3" y="14" width="7" height="7" rx="1"/><rect x="14" y="14" width="7" height="7" rx="1"/></svg>`
}
func navIconCluster() string {
	return `<svg width="16" height="16" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.5" aria-hidden="true"><circle cx="12" cy="5" r="2"/><circle cx="5" cy="18" r="2"/><circle cx="19" cy="18" r="2"/><path d="M12 7v5m0 0-7 4m7-4 7 4" stroke-linecap="round"/></svg>`
}
func navIconService() string {
	return `<svg width="16" height="16" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.5" aria-hidden="true"><rect x="4" y="4" width="16" height="16" rx="2"/><path d="M8 9h8M8 13h5M8 17h3" stroke-linecap="round"/></svg>`
}
func navIconDatabase() string {
	return `<svg width="16" height="16" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.5" aria-hidden="true"><ellipse cx="12" cy="5" rx="7" ry="3"/><path d="M5 5v7c0 1.7 3.1 3 7 3s7-1.3 7-3V5M5 12v7c0 1.7 3.1 3 7 3s7-1.3 7-3v-7"/></svg>`
}
func navIconStorage() string {
	return `<svg width="16" height="16" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.5" aria-hidden="true"><path d="M4 7h16v13H4zM4 7l2-3h12l2 3M9 11h6" stroke-linejoin="round" stroke-linecap="round"/></svg>`
}
func navIconPulse() string {
	return `<svg width="16" height="16" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.5" aria-hidden="true"><path d="M3 12h4l2-7 4 14 2-7h6" stroke-linecap="round" stroke-linejoin="round"/></svg>`
}
func navIconDocs() string {
	return `<svg width="16" height="16" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.5" aria-hidden="true"><path d="M6 3h9l3 3v15H6zM15 3v4h3M9 12h6M9 16h6" stroke-linecap="round" stroke-linejoin="round"/></svg>`
}

func geassLogoMark() string {
	return `<svg class="sidebar-brand-mark" viewBox="0 0 64 64" aria-hidden="true"><path d="M47.75 15.9A23 23 0 1 0 52.8 39H34" fill="none" stroke-width="6" stroke-linecap="round" stroke-linejoin="round"/><path d="M52.8 39V51" fill="none" stroke-width="6" stroke-linecap="round"/><circle cx="32" cy="32" r="4" fill="currentColor"/><path d="M32 28V18M28.5 34 20 39M35.5 34 44 39" fill="none" stroke-width="2.5" stroke-linecap="round"/></svg>`
}

func navIconFolder() string {
	return `<svg width="16" height="16" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.5" aria-hidden="true"><path d="M3 7a2 2 0 0 1 2-2h4l2 2h8a2 2 0 0 1 2 2v8a2 2 0 0 1-2 2H5a2 2 0 0 1-2-2V7z" stroke-linejoin="round"/></svg>`
}

func navIconSettings() string {
	return `<svg width="16" height="16" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.5" aria-hidden="true"><circle cx="12" cy="12" r="3"/><path d="M12 2v2M12 20v2M4.93 4.93l1.41 1.41M17.66 17.66l1.41 1.41M2 12h2M20 12h2M4.93 19.07l1.41-1.41M17.66 6.34l1.41-1.41" stroke-linecap="round"/></svg>`
}

func navIconSun() string {
	return `<svg width="16" height="16" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.5" aria-hidden="true"><circle cx="12" cy="12" r="4"/><path d="M12 2v2M12 20v2M4.93 4.93l1.41 1.41M17.66 17.66l1.41 1.41M2 12h2M20 12h2M4.93 19.07l1.41-1.41M17.66 6.34l1.41-1.41" stroke-linecap="round"/></svg>`
}

func navIconMoon() string {
	return `<svg width="16" height="16" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.5" aria-hidden="true"><path d="M20 14.5A8.5 8.5 0 0 1 9.5 4 7 7 0 1 0 20 14.5z" stroke-linejoin="round"/></svg>`
}

func shellScripts() string {
	return `(function () {
	const root = document.documentElement;
	const button = document.getElementById("theme-toggle");
	const label = document.getElementById("theme-toggle-label");
	if (button) {
		const saved = localStorage.getItem("geass.theme");
		if (saved) root.dataset.theme = saved;
		const update = () => {
			const dark = root.dataset.theme !== "light";
			if (label) label.textContent = dark ? "Light mode" : "Dark mode";
			button.setAttribute("aria-label", dark ? "Switch to light mode" : "Switch to dark mode");
		};
		update();
		button.addEventListener("click", () => {
			root.dataset.theme = root.dataset.theme === "light" ? "dark" : "light";
			localStorage.setItem("geass.theme", root.dataset.theme);
			update();
		});
	}
	const toggle = document.getElementById("sidebar-toggle");
	const shell = document.querySelector(".app-shell");
	if (toggle && shell) {
		toggle.addEventListener("click", () => shell.classList.toggle("sidebar-open"));
	}
	// Keep mutation action paths (/save, /create, /delete, …) out of the address bar.
	// Only attach HTMX when the form will redirect (HX-Redirect / 303). Forms that return
	// HTML fragments must set their own hx-target (e.g. #app-config) so we do not
	// replace <body> with a partial.
	const enhanceMutationForms = (scope) => {
		const rootEl = scope && scope.querySelectorAll ? scope : document;
		rootEl.querySelectorAll("form").forEach((form) => {
			const method = (form.getAttribute("method") || "get").toLowerCase();
			if (method !== "post") return;
			if (form.hasAttribute("hx-post") || form.hasAttribute("data-native-submit")) return;
			const action = form.getAttribute("action") || "";
			if (/^https?:\/\//i.test(action)) return;
			form.setAttribute("hx-post", action || window.location.pathname);
			form.setAttribute("hx-push-url", "false");
			if (!form.hasAttribute("hx-target")) {
				form.setAttribute("hx-target", "body");
				form.setAttribute("hx-swap", "none");
			}
			if (window.htmx) window.htmx.process(form);
		});
	};
	// Keep small fragment actions usable when the optional HTMX CDN script is
	// unavailable, which is common in local or air-gapped Kubernetes setups.
	const nativeHXRequest = async (trigger) => {
		const endpoint = trigger.getAttribute("hx-post");
		const targetSelector = trigger.getAttribute("hx-target");
		const target = targetSelector ? document.querySelector(targetSelector) : trigger;
		if (!endpoint || !target) return;
		if (trigger.dataset.nativeHXBusy === "true") return;
		trigger.dataset.nativeHXBusy = "true";
		if ("disabled" in trigger) trigger.disabled = true;
		try {
			const response = await fetch(endpoint, {
				method: "POST",
				credentials: "same-origin",
				headers: { "HX-Request": "true" }
			});
			if (!response.ok) throw new Error("request failed");
			const html = await response.text();
			const template = document.createElement("template");
			template.innerHTML = html.trim();
			const replacement = template.content.firstElementChild;
			if (!replacement) throw new Error("empty response");
			target.replaceWith(replacement);
			nativeHXWire(replacement);
		} catch (_) {
			trigger.dataset.nativeHXBusy = "false";
			if ("disabled" in trigger) trigger.disabled = false;
		}
	};
	const nativeHXWire = (scope) => {
		if (window.htmx) return;
		const rootEl = scope && scope.querySelectorAll ? scope : document;
		rootEl.querySelectorAll("[hx-post][hx-target]").forEach((trigger) => {
			if (trigger.dataset.nativeHXBound === "true") return;
			trigger.dataset.nativeHXBound = "true";
			trigger.addEventListener("click", (event) => {
				event.preventDefault();
				void nativeHXRequest(trigger);
			});
		});
		const status = rootEl.matches?.("#domain-verify-status")
			? rootEl
			: rootEl.querySelector?.("#domain-verify-status");
		if (!status || !status.hasAttribute("hx-post") || !status.getAttribute("hx-trigger")?.includes("every 5s")) return;
		if (status.dataset.nativeHXPoll === "true") return;
		status.dataset.nativeHXPoll = "true";
		setTimeout(() => void nativeHXRequest(status), 5000);
	};
	enhanceMutationForms(document);
	nativeHXWire(document);
	document.body.addEventListener("htmx:afterSwap", (event) => {
		enhanceMutationForms(event.detail?.target || document);
	});
	document.addEventListener("click", (event) => {
		const trigger = event.target.closest('a[href="#resource-chooser"]');
		if (!trigger) return;
		event.preventDefault();
		const dialog = document.getElementById("resource-chooser");
		if (dialog?.showModal) dialog.showModal();
	});
	document.querySelectorAll(".topology-toolbar").forEach((toolbar) => {
		const canvas = toolbar.nextElementSibling;
		const surface = canvas?.querySelector("[data-topology-canvas]") || canvas;
		if (!surface) return;
		let zoom = 1;
		const applyZoom = () => { surface.style.transform = "scale(" + zoom + ")"; if (canvas) canvas.dataset.zoom = String(zoom); };
		toolbar.querySelectorAll("[data-topology-action]").forEach((control) => {
			control.addEventListener("click", () => {
				switch (control.dataset.topologyAction) {
				case "fit": zoom = 1; applyZoom(); break;
				case "zoom-in": zoom = Math.min(1.4, zoom + 0.1); applyZoom(); break;
				case "zoom-out": zoom = Math.max(0.7, zoom - 0.1); applyZoom(); break;
				case "fullscreen":
					if (canvas.requestFullscreen) canvas.requestFullscreen();
					break;
				}
			});
		});
	});
	document.querySelectorAll("[data-copy-value]").forEach((control) => {
		control.addEventListener("click", async () => {
			const value = control.dataset.copyValue || "";
			try {
				await navigator.clipboard.writeText(value);
				control.textContent = "Copied";
				setTimeout(() => { control.textContent = "Copy"; }, 1200);
			} catch (_) {
				control.textContent = "Copy unavailable";
			}
		});
	});
	document.addEventListener("input", (event) => {
		const search = event.target.closest("[data-variable-search]");
		if (!search) return;
		const query = search.value.trim().toLowerCase();
		const panel = search.closest("section");
		if (!panel) return;
		panel.querySelectorAll("[data-variable-row]").forEach((row) => {
			row.hidden = query !== "" && !row.dataset.variableKey.toLowerCase().includes(query);
		});
	});
	document.addEventListener("input", (event) => {
		const search = event.target.closest("[data-settings-search]");
		if (!search) return;
		const query = search.value.trim().toLowerCase();
		document.querySelectorAll("[data-settings-link]").forEach((link) => {
			link.hidden = query !== "" && !link.dataset.settingsLabel.includes(query);
		});
		document.querySelectorAll("[data-settings-section]").forEach((section) => {
			const text = section.dataset.settingsSection.toLowerCase();
			section.hidden = query !== "" && !text.includes(query);
		});
	});
	document.addEventListener("input", (event) => {
		if (!event.target.closest("[data-confirm-input]")) return;
		syncConfirmButtons();
	});
	const syncConfirmButtons = () => {
		document.querySelectorAll("[data-confirm-input]").forEach((input) => {
			const target = input.dataset.confirmTarget;
			const matches = input.value === input.dataset.confirmValue;
			document.querySelectorAll("[data-confirm-button]").forEach((button) => {
				if (button.dataset.confirmButton !== target) return;
				button.disabled = !matches;
				if (matches) button.removeAttribute("disabled");
				else button.setAttribute("disabled", "");
			});
		});
	};
	syncConfirmButtons();
	document.addEventListener("click", (event) => {
		const trigger = event.target.closest("[data-open-dialog]");
		if (!trigger || trigger.disabled) return;
		event.preventDefault();
		const dialog = document.getElementById(trigger.getAttribute("data-open-dialog"));
		if (dialog?.showModal) dialog.showModal();
	});
	document.querySelectorAll("[data-environment-multiselect]").forEach((root) => {
		const selected = root.querySelector("[data-env-selected]");
		const preset = root.querySelector("[data-env-preset]");
		const custom = root.querySelector("[data-env-custom]");
		const addCustom = root.querySelector("[data-env-add-custom]");
		if (!selected) return;
		const current = () => Array.from(selected.querySelectorAll('input[name="environments"]')).map((input) => input.value);
		const syncPreset = () => {
			if (!preset) return;
			const active = new Set(current());
			Array.from(preset.options).forEach((option) => {
				if (!option.value) return;
				option.hidden = active.has(option.value);
			});
			preset.value = "";
		};
		const addEnv = (value) => {
			value = value.trim().toLowerCase();
			if (!value || current().includes(value)) return false;
			const chip = document.createElement("span");
			chip.className = "environment-chip";
			chip.dataset.env = value;
			chip.append(document.createTextNode(value + " "));
			const remove = document.createElement("button");
			remove.type = "button";
			remove.className = "environment-chip-remove";
			remove.setAttribute("aria-label", "Remove " + value);
			remove.textContent = "×";
			const hidden = document.createElement("input");
			hidden.type = "hidden";
			hidden.name = "environments";
			hidden.value = value;
			chip.append(remove, hidden);
			selected.appendChild(chip);
			syncPreset();
			return true;
		};
		selected.addEventListener("click", (event) => {
			const remove = event.target.closest(".environment-chip-remove");
			if (!remove) return;
			remove.closest(".environment-chip")?.remove();
			syncPreset();
		});
		preset?.addEventListener("change", () => {
			if (!preset.value) return;
			addEnv(preset.value);
		});
		const submitCustom = () => {
			if (!custom) return;
			if (addEnv(custom.value)) custom.value = "";
		};
		addCustom?.addEventListener("click", submitCustom);
		custom?.addEventListener("keydown", (event) => {
			if (event.key === "Enter") {
				event.preventDefault();
				submitCustom();
			}
		});
		syncPreset();
	});
	document.addEventListener("keydown", (event) => {
		if ((event.metaKey || event.ctrlKey) && event.key.toLowerCase() === "k") {
			const search = document.getElementById("project-search");
			if (search) { event.preventDefault(); search.focus(); }
		}
	});
})();`
}
