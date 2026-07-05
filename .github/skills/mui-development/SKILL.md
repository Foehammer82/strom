---
name: mui-development
description: 'Reference conventions for building or migrating the Wattkeeper controller web app (controller/web) to Material UI (MUI). Use when generating, editing, or reviewing React + MUI component code, theming, or planning the MUI adoption/migration.'
user-invocable: true
---

# MUI (Material UI) Development Reference

This is a reference document, not a step-by-step procedure. Consult it before
generating or editing any React + MUI code in `controller/web`, and check the linked
documentation before inventing component APIs or theming approaches from memory.

## 1. Reference Documentation

Check these before generating component code:

- MUI Getting Started: https://mui.com/material-ui/getting-started/
- Full Component Catalog: https://mui.com/material-ui/all-components/
- Theming: https://mui.com/material-ui/customization/theming/
- Theme Component Overrides: https://mui.com/material-ui/customization/theme-components/
- MUI System (sx prop): https://mui.com/system/getting-started/
- Composition Patterns: https://mui.com/material-ui/guides/composition/
- MUI X (DataGrid, DatePickers, Charts): https://mui.com/x/introduction/
- Accessibility Guide: https://mui.com/material-ui/guides/accessibility/
- Material Design 3 Spec: https://m3.material.io/
- Material Design 3 Foundations: https://m3.material.io/foundations
- Official Example Projects: https://github.com/mui/material-ui/tree/master/examples
- MUI Source (for prop signatures): https://github.com/mui/material-ui

Bonus reference: the official
[MUI theming agent skill](https://github.com/mui/material-ui/tree/master/skills/material-ui-theming)
gives an AI assistant full context on `createTheme`, palette, color schemes, CSS
variables, and TypeScript augmentation — consult it for anything theming-related that
isn't already covered by this file.

**Important caveat on the Material Design 3 links:** Material UI implements
**Material Design 2**, not Material Design 3. The M3 links above are useful for design
tokens, spacing, and interaction-pattern inspiration, but do not assume MUI components
have literal M3 parity, and do not invent M3-only component APIs that don't exist in
MUI. If a design decision needs true M3 fidelity, say so explicitly rather than
approximating it silently.

## 2. Stack Assumptions

- **Current state:** the MUI migration described by this skill is complete.
  `controller/web` is React 19.1, TypeScript 5.8 (strict-leaning), Vite 6,
  `react-router-dom` 7, `@mui/material`/`@mui/icons-material`/Emotion, and
  `recharts` (used only inside `src/components/ChartCard.tsx` — see the follow-up
  note in section 7). There is no hand-rolled `styles.css` anymore; all styling goes
  through the theme in [`controller/web/src/theme.ts`](../../../controller/web/src/theme.ts)
  and per-component `sx`/`styled()`.
- **Note on the resolved MUI version:** an unpinned `npm install` in this repo resolved
  `@mui/material` to a fairly new major version whose typings trim some historical
  convenience props (e.g. `Stack`'s `alignItems`, `Typography`'s `fontWeight`,
  `TextField`'s `inputProps`). See the "Typing quirks" callout in section 7 before
  assuming a prop exists — verify against the installed types or fall back to `sx`.
- **Styling engine:** Emotion (`@emotion/react` + `@emotion/styled`) — this is MUI's
  default and the only styling engine this project uses. Do not introduce
  styled-components or a second CSS-in-JS library alongside it.
- **Icons:** `@mui/icons-material`, imported per-icon (see Component Preferences).
- **Charts/tables:** prefer MUI X (`@mui/x-charts`, `@mui/x-data-grid`) over adding a
  second charting/table library for any *new* view. `recharts` remains only in
  `ChartCard` as an explicitly tracked follow-up (see section 7) — do not extend it
  with new usage elsewhere; use `@mui/x-charts` for new charts and
  `@mui/x-data-grid`/MUI `Table` for new tabular views (see `AlertsPage` for the
  current `Table` pattern).

## 3. Theming Conventions

The project's theme is centralized in a single `createTheme()` call in
[`controller/web/src/theme.ts`](../../../controller/web/src/theme.ts). It uses MUI's
`cssVariables`/`colorSchemes` support with a custom `colorSchemeSelector:
'[data-theme="%s"]'` so the existing `system` / `light` / `dark` preference state in
`App.tsx` (persisted to `localStorage`, still driving `document.documentElement.dataset.theme`)
continues to control the resolved color scheme without maintaining two parallel
theming systems.

When extending the theme:

- Add new palette/typography/spacing values to `theme.ts`'s `createTheme()` call
  rather than hand-picking new colors or reintroducing CSS custom properties.
- Use MUI's built-in light/dark color-scheme support (`colorSchemes` /
  `cssVariables`) for anything theme-dependent; don't reintroduce a manual
  `data-theme` CSS attribute system beyond the one selector already wired for
  `system`/`light`/`dark` switching.
- Extend `theme.typography` and `theme.spacing` for any new type scale or spacing
  values. Never hardcode a hex color, pixel spacing value, or font stack directly in a
  component when an equivalent theme token exists or should exist.
- If a genuinely new design token is needed, add it to the theme (via the documented
  [custom theme variables](https://mui.com/material-ui/customization/theming/#theme-configuration-variables)
  pattern) rather than inventing an ad hoc local constant.

## 4. Component Preferences

- Prefer `Stack` for one-directional layout (rows/columns with consistent gaps) over
  manually reproducing flexbox with a raw `sx={{ display: 'flex', ... }}`.
- Use the modern `Grid` (Grid2-based, unified `Grid` in current MUI versions) for
  two-dimensional/responsive layouts instead of the legacy `Grid` v1 API. Check the
  component catalog for the current import path before writing new grid code, since
  this has changed across MUI versions.
- Decision rule for styling:
  - **`sx` prop** — one-off, local layout/spacing/style tweaks on a single component
    instance.
  - **`styled()`** — a component you'll reuse in multiple places, or one that needs
    named style variants.
  - **Theme `components` overrides** — a change that should apply to *every* instance
    of a MUI component app-wide (e.g. "all `Button`s use this border radius").
  - If you find yourself repeating the same `sx` object in multiple places, that's a
    signal to promote it to `styled()` or a theme override instead.
- Import icons individually: `import ContentCopyIcon from '@mui/icons-material/ContentCopy';`.
  Never `import * as Icons from '@mui/icons-material'` or barrel-import the whole icon
  package — it defeats tree-shaking and bloats the bundle.
- Favor composition (`slots`/`slotProps`, children) over fighting a component's
  internals — see the [Composition Patterns](https://mui.com/material-ui/guides/composition/)
  guide before reaching for a workaround.

## 5. Do / Don't List

- **Don't** use inline `style={{ ... }}` objects on MUI components — use `sx` (or
  `styled()` for reusable cases).
- **Don't** ship icon-only buttons or controls without an `aria-label`.
- **Don't** override MUI internals with CSS-specificity hacks (`!important`, deep
  descendant selectors targeting MUI's internal classes) — use the theme's
  `components.styleOverrides` / `variants` API instead.
- **Don't** mix Emotion with another styling library, or reintroduce global hand-rolled
  CSS for anything MUI already themes.
- **Don't** hardcode a color, spacing value, or font that duplicates (or should be) a
  theme token.
- **Don't** suppress focus outlines/visible focus indicators for the sake of visual
  polish.
- **Do** reach for an existing MUI/MUI X component before building a custom one.
- **Do** keep new component code in TypeScript with explicit prop types.

## 6. Accessibility Requirements

Baseline expectations for any generated or edited component, per the
[MUI accessibility guide](https://mui.com/material-ui/guides/accessibility/):

- Every interactive control has an accessible name (visible label, `aria-label`, or
  `aria-labelledby`).
- Icon-only buttons always have an `aria-label` describing the action, not the icon.
- Dialogs/modals trap focus while open and restore focus to the triggering element on
  close — this must be preserved when migrating the existing `confirmRequest`
  modal pattern in `App.tsx` to MUI's `Dialog`.
- Visible focus indicators are never removed or hidden.
- Color combinations meet WCAG 2.1 AA contrast in both light and dark theme, matching
  or exceeding the current custom CSS tokens.
- Toasts/snackbars use appropriate live-region semantics (MUI's `Snackbar` handles
  this by default — don't rebuild toast behavior manually once migrated).

## 7. Few-Shot Examples

These are finished, in-repo examples from the `controller/web` MUI migration. Match
this project's actual style (import ordering, `sx` usage, status-color mapping via
small helper functions, `component={Link}`/`component={RouterLink}` composition)
rather than generic MUI examples found elsewhere.

### Fleet card (status-colored `Card` + `Chip`, from `src/components/NodeCard.tsx`)

```tsx
import type { ChipProps } from "@mui/material/Chip";

function statusColor(status: string): ChipProps["color"] {
  if (status === "pending") return "warning";
  if (status === "adopted-online") return "success";
  return "error";
}

// ...
<Card
  variant="outlined"
  sx={{ height: "100%", display: "flex", flexDirection: "column", borderLeft: 4, borderLeftColor: `${color}.main` }}
>
  <CardContent sx={{ flexGrow: 1 }}>
    <Stack direction="row" sx={{ justifyContent: "space-between", alignItems: "flex-start", gap: 1.5 }}>
      <Box sx={{ minWidth: 0 }}>
        <Typography variant="overline" color="text.secondary" component="p" sx={{ m: 0 }}>
          {node.id}
        </Typography>
        <Typography variant="h6" component="h4" sx={{ m: 0 }}>
          {title}
        </Typography>
      </Box>
      <Chip label={node.status} color={color} size="small" />
    </Stack>
    {/* ...metadata rows, UPS summary list... */}
  </CardContent>
  <CardActions sx={{ justifyContent: "space-between", px: 2, pb: 2 }}>
    {/* primary action button + <OverflowMenu items={menuItems} /> */}
  </CardActions>
</Card>
```

Key conventions shown here: a small `statusColor()` helper mapping domain status to a
MUI palette key (never hardcode the hex), `borderLeftColor` driven by that same
palette key via template-string `sx`, and composing the reusable `OverflowMenu` inside
`CardActions` instead of rebuilding a menu per card.

### UPS detail metric grid + chart wrapper (from `src/pages/UPSDetailPage.tsx` and `src/components/ChartCard.tsx`)

```tsx
<Grid container spacing={1.75} sx={{ mb: 2.5 }}>
  {[
    ["Status", detail.status || detail.metrics?.status || "unknown"],
    ["Battery", detail.metrics?.battery_charge_percent != null ? `${detail.metrics.battery_charge_percent}%` : "unknown"],
    // ...
  ].map(([label, value]) => (
    <Grid key={label} size={{ xs: 12, sm: 6, md: 2.4 }}>
      <Card variant="outlined">
        <CardContent>
          <Typography variant="overline" color="text.secondary">{label}</Typography>
          <Typography variant="h6" component="strong" sx={{ display: "block" }}>{value}</Typography>
        </CardContent>
      </Card>
    </Grid>
  ))}
</Grid>

<Grid container spacing={1.75}>
  <Grid size={{ xs: 12, md: 4 }}>
    <ChartCard title="Battery charge" data={chargeHistory} stroke="#14b8a6" />
  </Grid>
  {/* ...load, runtime... */}
</Grid>
```

`ChartCard` wraps `recharts` (not yet replaced with `@mui/x-charts` — see the
follow-up note below) in a themed `Card`/`CardContent`, and reads chart tick/tooltip
colors from `useTheme()` so it matches light/dark mode instead of hardcoding
`currentColor` or fixed hex values.

**Known follow-up (explicitly not done in this pass):** `ChartCard` still uses
`recharts`. Per this skill's guidance to prefer MUI X over a second charting library,
replacing `recharts` with `@mui/x-charts` in `ChartCard` is tracked as follow-up work,
not silently left half-styled.

### Confirm dialog + toast pattern (from `src/components/ConfirmDialog.tsx` and `src/components/ToastSnackbar.tsx`)

```tsx
// ConfirmDialog.tsx — tone (danger/warn) maps to MUI's error/warning palette colors,
// and MUI's Dialog handles focus trap/restore automatically.
<Dialog open onClose={onCancel} aria-labelledby="confirm-dialog-title" aria-describedby="confirm-dialog-description" maxWidth="xs" fullWidth>
  <DialogTitle id="confirm-dialog-title">
    <Typography variant="overline" color="text.secondary" component="p" sx={{ m: 0, lineHeight: 1.4 }}>
      Confirm action
    </Typography>
    {request.title}
  </DialogTitle>
  <DialogContent>
    <DialogContentText id="confirm-dialog-description">{request.message}</DialogContentText>
  </DialogContent>
  <DialogActions>
    <Button onClick={onCancel} color="inherit">{request.cancelLabel || "Cancel"}</Button>
    <Button
      onClick={onAccept}
      variant="contained"
      color={request.tone === "danger" ? "error" : request.tone === "warn" ? "warning" : "primary"}
      autoFocus
    >
      {request.confirmLabel}
    </Button>
  </DialogActions>
</Dialog>
```

```tsx
// ToastSnackbar.tsx — Snackbar's own autoHideDuration replaces a manual setTimeout;
// don't run both a Snackbar timer and an app-level timer for the same toast.
<Snackbar
  open={Boolean(message)}
  autoHideDuration={3200}
  onClose={(_event, reason) => {
    if (reason === "clickaway") return;
    onClose();
  }}
  anchorOrigin={{ vertical: "bottom", horizontal: "center" }}
>
  <Alert onClose={onClose} severity="info" variant="filled" sx={{ width: "100%" }}>
    {message}
  </Alert>
</Snackbar>
```

### Typing quirks discovered in this project's resolved MUI version

The `@mui/material` version this project resolves to (installed via unpinned
`^`-range `npm install`) trims some historical convenience props from the typed API.
Confirmed in this codebase:

- `Stack` does not type `alignItems`/`justifyContent` as direct props — pass them via
  `sx={{ alignItems: ... }}` instead.
- `Typography` does not type `fontWeight` as a direct prop — use
  `sx={{ fontWeight: ... }}`.
- `TextField` has no `inputProps` shorthand — use
  `slotProps={{ htmlInput: { min: 1, max: 100 } }}`.

If a "no overload matches this call" error blames the `component` prop on a
polymorphic component, check whether the prop you added is actually typed on that
component in the installed version before assuming your usage is wrong — it may need
to move into `sx` instead.
