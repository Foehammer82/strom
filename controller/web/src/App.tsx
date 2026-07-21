import { useEffect, useRef, useState } from "react";
import { Route, Routes } from "react-router-dom";
import AppBar from "@mui/material/AppBar";
import Box from "@mui/material/Box";
import Stack from "@mui/material/Stack";
import Toolbar from "@mui/material/Toolbar";
import Typography from "@mui/material/Typography";
import HeaderMenu from "./components/HeaderMenu";
import ConfirmDialog from "./components/ConfirmDialog";
import ToastSnackbar from "./components/ToastSnackbar";
import FleetPage from "./pages/FleetPage";
import AlertsPage from "./pages/AlertsPage";
import SettingsPage from "./pages/SettingsPage";
import type { ConfirmRequest, ThemePreference, ToastRequest, ToastSeverity } from "./types";

const THEME_PREF_STORAGE_KEY = "strom-theme-preference";
const LEGACY_THEME_STORAGE_KEY = "strom-theme";

function normalizeThemePreference(value: string | null): ThemePreference | null {
  if (value === "system" || value === "light" || value === "dark") {
    return value;
  }
  return null;
}

function App() {
  const [themePreference, setThemePreference] = useState<ThemePreference>("system");
  const [systemPrefersDark, setSystemPrefersDark] = useState<boolean>(() => window.matchMedia("(prefers-color-scheme: dark)").matches);
  const [toast, setToast] = useState<ToastRequest | null>(null);
  const [confirmRequest, setConfirmRequest] = useState<ConfirmRequest | null>(null);
  const [fleetRefreshSignal, setFleetRefreshSignal] = useState(0);
  const confirmResolverRef = useRef<((accepted: boolean) => void) | null>(null);

  function showToast(message: string, severity: ToastSeverity = "info") {
    setToast({ message, severity });
  }

  useEffect(() => {
    const savedPref = normalizeThemePreference(window.localStorage.getItem(THEME_PREF_STORAGE_KEY));
    const legacyTheme = normalizeThemePreference(window.localStorage.getItem(LEGACY_THEME_STORAGE_KEY));
    setThemePreference(savedPref ?? (legacyTheme === "light" || legacyTheme === "dark" ? legacyTheme : "system"));
  }, []);

  useEffect(() => {
    const media = window.matchMedia("(prefers-color-scheme: dark)");
    const handleChange = (event: MediaQueryListEvent) => setSystemPrefersDark(event.matches);
    if (typeof media.addEventListener === "function") {
      media.addEventListener("change", handleChange);
      return () => media.removeEventListener("change", handleChange);
    }
    media.addListener(handleChange);
    return () => media.removeListener(handleChange);
  }, []);

  const resolvedTheme = themePreference === "system" ? (systemPrefersDark ? "dark" : "light") : themePreference;

  useEffect(() => {
    document.documentElement.dataset.theme = resolvedTheme;
    window.localStorage.setItem(THEME_PREF_STORAGE_KEY, themePreference);
    window.localStorage.setItem(LEGACY_THEME_STORAGE_KEY, resolvedTheme);
  }, [resolvedTheme, themePreference]);

  function requestConfirmation(request: ConfirmRequest): Promise<boolean> {
    setConfirmRequest(request);
    return new Promise((resolve) => {
      confirmResolverRef.current = resolve;
    });
  }

  function closeConfirmation(accepted: boolean) {
    if (confirmResolverRef.current) {
      confirmResolverRef.current(accepted);
      confirmResolverRef.current = null;
    }
    setConfirmRequest(null);
  }

  return (
    <Box component="main" sx={{ width: "min(1240px, calc(100vw - 32px))", mx: "auto", py: { xs: 2.5, md: 3.5 }, pb: 5 }}>
      <AppBar
        position="static"
        color="transparent"
        elevation={0}
        sx={{
          mb: 2.75,
          borderRadius: 2,
          border: 1,
          borderColor: "divider",
          bgcolor: "background.paper",
        }}
      >
        <Toolbar sx={{ justifyContent: "space-between", gap: 2, flexWrap: "wrap", py: 1.5 }}>
          <Stack direction="row" spacing={1.5} sx={{ alignItems: "center" }}>
            <Box component="img" src="/logo.svg" alt="Strom logo" sx={{ width: 40, height: 40, display: "block" }} />
            <Stack spacing={0} sx={{ justifyContent: "center" }}>
              <Typography variant="h5" component="h1" sx={{ m: 0, fontWeight: 700, lineHeight: 1.2 }}>
                Strom
              </Typography>
              <Typography
                variant="overline"
                color="text.secondary"
                component="p"
                sx={{ m: 0, lineHeight: 1.4, letterSpacing: "0.06em" }}
              >
                Controller
              </Typography>
            </Stack>
          </Stack>
          <HeaderMenu
            themePreference={themePreference}
            onFleetRefresh={() => setFleetRefreshSignal((previous) => previous + 1)}
            onThemePreferenceChange={(next) => {
              setThemePreference(next);
            }}
          />
        </Toolbar>
      </AppBar>

      <Routes>
        <Route path="/" element={<FleetPage onToast={showToast} requestConfirmation={requestConfirmation} reloadSignal={fleetRefreshSignal} />} />
        <Route path="/alerts" element={<AlertsPage onToast={showToast} requestConfirmation={requestConfirmation} />} />
        <Route path="/settings" element={<SettingsPage onToast={showToast} />} />
      </Routes>

      <ToastSnackbar request={toast} onClose={() => setToast(null)} />
      {confirmRequest ? (
        <ConfirmDialog
          request={confirmRequest}
          onAccept={() => closeConfirmation(true)}
          onCancel={() => closeConfirmation(false)}
        />
      ) : null}
    </Box>
  );
}

export default App;
