import { CodeHighlight } from "@mantine/code-highlight";

export function CodePanel({ code, language = "bash", empty = "No output yet." }) {
  const text = code && String(code).trim() ? String(code) : empty;
  return <CodeHighlight code={text} language={language} withCopyButton />;
}
