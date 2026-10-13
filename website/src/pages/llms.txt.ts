import type { APIRoute } from "astro";
import { getCollection } from "astro:content";
import { absoluteUrl, githubUrl, siteDescription, siteName, withBase } from "../lib/site";

// A plain-text map of the docs for AI assistants and agents (https://llmstxt.org). It is built from the docs
// collection, so a new page shows up here without anyone editing this file.
const sections = [
  { dir: "overview", label: "Overview" },
  { dir: "getting-started", label: "Getting started" },
  { dir: "guides", label: "Guides" },
  { dir: "operations", label: "Operations" },
  { dir: "reference", label: "Reference" },
];

export const GET: APIRoute = async ({ site }) => {
  if (!site) return new Response("", { status: 404 });

  const pages = await getCollection("docs");
  const lines = [
    `# ${siteName}`,
    "",
    `> ${siteDescription}`,
    "",
    "You can run it from a dashboard, a command line, an MCP server or an API. These pages describe what Geass does today. Start with the introduction and the install guide.",
  ];

  for (const { dir, label } of sections) {
    const inSection = pages
      .filter((page) => page.id.startsWith(`docs/${dir}/`))
      .sort((a, b) => a.id.localeCompare(b.id));
    if (inSection.length === 0) continue;
    lines.push("", `## ${label}`, "");
    for (const page of inSection) {
      const link = `- [${page.data.title}](${absoluteUrl(withBase(`${page.id}/`), site)})`;
      lines.push(page.data.description ? `${link}: ${page.data.description}` : link);
    }
  }

  lines.push("", "## Optional", "", `- [Source code and releases](${githubUrl}): the repository, issues and installer downloads`);
  return new Response(`${lines.join("\n")}\n`, { headers: { "Content-Type": "text/plain; charset=utf-8" } });
};
