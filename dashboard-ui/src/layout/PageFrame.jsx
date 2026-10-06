import { Stack } from "@mantine/core";

export function PageFrame({ context, notice, nav, header, children }) {
  return (
    <Stack gap="md">
      {header}
      {context}
      {notice}
      {nav}
      {children}
    </Stack>
  );
}
