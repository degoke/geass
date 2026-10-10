import { Stack } from "@mantine/core";

export function PageFrame({ context, notice, nav, header, children, className }) {
  return (
    <Stack gap="md" className={className}>
      {header}
      {context}
      {notice}
      {nav}
      {children}
    </Stack>
  );
}
