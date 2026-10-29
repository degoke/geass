import { createRoot } from "react-dom/client";
import "@fontsource-variable/space-grotesk";
import "@fontsource-variable/jetbrains-mono";
import "@mantine/core/styles.css";
import "@mantine/code-highlight/styles.css";
import "@mantine/notifications/styles.css";
import { MantineProvider } from "@mantine/core";
import { Notifications } from "@mantine/notifications";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { RouterProvider } from "@tanstack/react-router";
import { geassTheme } from "@/theme";
import { createDashboardRouter } from "@/router";
import "@/styles.css";

const queryClient = new QueryClient();
const router = createDashboardRouter();

createRoot(document.getElementById("root")).render(
  <MantineProvider theme={geassTheme} defaultColorScheme="auto">
    <Notifications position="top-right" />
    <QueryClientProvider client={queryClient}>
      <RouterProvider router={router} />
    </QueryClientProvider>
  </MantineProvider>
);
