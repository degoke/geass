import { createRoot } from "react-dom/client";
import "@fontsource-variable/inter";
import "@fontsource-variable/geist-mono";
import "@mantine/core/styles.css";
import "@mantine/code-highlight/styles.css";
import "@mantine/notifications/styles.css";
import { MantineProvider } from "@mantine/core";
import { Notifications } from "@mantine/notifications";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { RouterProvider } from "@tanstack/react-router";
import { geassTheme } from "@/theme";
import { router } from "@/router";
import "@/styles.css";

const queryClient = new QueryClient();

createRoot(document.getElementById("root")).render(
  <MantineProvider theme={geassTheme} forceColorScheme="dark">
    <Notifications position="top-right" />
    <QueryClientProvider client={queryClient}>
      <RouterProvider router={router} />
    </QueryClientProvider>
  </MantineProvider>
);
