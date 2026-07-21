import { useState } from "react";
import type { MouseEvent } from "react";
import { Link as RouterLink, useLocation, useNavigate } from "react-router-dom";
import Avatar from "@mui/material/Avatar";
import Box from "@mui/material/Box";
import Divider from "@mui/material/Divider";
import IconButton from "@mui/material/IconButton";
import ListItemIcon from "@mui/material/ListItemIcon";
import Menu from "@mui/material/Menu";
import MenuItem from "@mui/material/MenuItem";
import Stack from "@mui/material/Stack";
import ToggleButton from "@mui/material/ToggleButton";
import ToggleButtonGroup from "@mui/material/ToggleButtonGroup";
import Typography from "@mui/material/Typography";
import CheckIcon from "@mui/icons-material/Check";
import MenuIcon from "@mui/icons-material/Menu";
import OpenInNewIcon from "@mui/icons-material/OpenInNew";
import type { ThemePreference } from "../types";
import { formatThemeLabel } from "../lib/format";

const THEME_OPTIONS: ThemePreference[] = ["system", "light", "dark"];

export default function HeaderMenu({
  themePreference,
  onThemePreferenceChange,
  onFleetRefresh,
}: {
  themePreference: ThemePreference;
  onThemePreferenceChange: (next: ThemePreference) => void;
  onFleetRefresh: () => void;
}) {
  const [anchorEl, setAnchorEl] = useState<HTMLElement | null>(null);
  const navigate = useNavigate();
  const location = useLocation();
  const open = Boolean(anchorEl);

  function handleClose() {
    setAnchorEl(null);
  }

  function handleNavigate(path: string) {
    if (path === "/" && location.pathname === "/") {
      onFleetRefresh();
      window.scrollTo({ top: 0, behavior: "smooth" });
    } else {
      navigate(path);
    }
    handleClose();
  }

  return (
    <div>
      <IconButton
        aria-label="Open controller menu"
        aria-haspopup="menu"
        aria-expanded={open}
        onClick={(event: MouseEvent<HTMLElement>) => setAnchorEl(event.currentTarget)}
        size="large"
      >
        <MenuIcon />
      </IconButton>

      <Menu anchorEl={anchorEl} open={open} onClose={handleClose} aria-label="Controller menu">
        <Box sx={{ px: 2, py: 1 }}>
          <Stack direction="row" spacing={1.5} sx={{ alignItems: "center" }}>
            <Avatar sx={{ width: 32, height: 32, fontSize: "0.78rem", fontWeight: 700, bgcolor: "primary.main" }}>
              CA
            </Avatar>
            <div>
              <Typography variant="caption" color="text.secondary" component="p" sx={{ m: 0 }}>
                Signed in as
              </Typography>
              <Typography variant="body2" component="p" sx={{ m: 0, fontWeight: 700 }}>
                Controller Admin
              </Typography>
            </div>
          </Stack>
        </Box>
        <Divider />
        <MenuItem
          component={RouterLink}
          to="/"
          selected={location.pathname === "/"}
          onClick={(event) => {
            if (location.pathname === "/") {
              event.preventDefault();
            }
            handleNavigate("/");
          }}
        >
          Fleet
        </MenuItem>
        <MenuItem component={RouterLink} to="/alerts" selected={location.pathname === "/alerts"} onClick={handleClose}>
          Alerts
        </MenuItem>
        <MenuItem component={RouterLink} to="/settings" selected={location.pathname === "/settings"} onClick={handleClose}>
          Settings
        </MenuItem>
        <MenuItem
          component="a"
          href="https://foehammer82.github.io/strom/getting-started/"
          target="_blank"
          rel="noreferrer"
          onClick={handleClose}
        >
          Docs
          <ListItemIcon sx={{ minWidth: 0, ml: 1 }}>
            <OpenInNewIcon fontSize="small" />
          </ListItemIcon>
        </MenuItem>
        <Divider />
        <Box sx={{ px: 2, py: 1 }}>
          <Typography variant="caption" color="text.secondary" component="p" sx={{ mb: 1 }}>
            Appearance
          </Typography>
          <ToggleButtonGroup
            value={themePreference}
            exclusive
            fullWidth
            size="small"
            aria-label="Color mode"
            onChange={(_event, value: ThemePreference | null) => {
              if (value) {
                onThemePreferenceChange(value);
                handleClose();
              }
            }}
          >
            {THEME_OPTIONS.map((option) => (
              <ToggleButton key={option} value={option} aria-label={formatThemeLabel(option)}>
                {themePreference === option ? <CheckIcon fontSize="small" sx={{ mr: 0.5 }} /> : null}
                {formatThemeLabel(option)}
              </ToggleButton>
            ))}
          </ToggleButtonGroup>
        </Box>
      </Menu>
    </div>
  );
}
