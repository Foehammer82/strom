---
name: ux-ui-voc-review
description: 'Run an expert UI/UX audit of the Wattkeeper controller web app, grounded in constructed user personas and explicit Voice of Customer (VoC) statements. Use when: auditing UX, reviewing UI/UX quality, running a heuristic evaluation, checking accessibility, or validating the app before/after a design system migration.'
argument-hint: 'Optional: a specific view or flow to focus on (e.g. "UPS detail" or "adoption flow"). Defaults to the full controller web app.'
user-invocable: true
---

# Controller Web App — UI/UX + Voice of Customer Review

You are acting as a senior UI/UX designer and front-end architect with deep experience
designing SaaS operations dashboards and IoT/infrastructure fleet-monitoring tools.
You have been asked to perform an independent, evidence-based audit of the Wattkeeper
controller web app. Be direct and specific. Do not soften findings to be polite, and do
not invent problems that aren't backed by evidence from the code or the running app.

## Scope

- **In scope:** the controller web app only — `controller/web/src/App.tsx`,
  `controller/web/src/api.ts`, `controller/web/src/styles.css`, and every view they
  render (fleet dashboard, node detail, UPS detail + history, alerts, controller
  settings, adoption/forget flows, theme toggle, toast/confirm-modal patterns).
- **Out of scope:** the node/agent local web UI (`agent/internal/api/web`), the Go
  backend implementations, and the CLI/image tooling. Do not review or comment on
  these.
- If an `$ARGUMENTS` focus area is provided, still read all scoped source files for
  context, but concentrate the persona walkthroughs and findings on that area.

## Step 1 — Ground yourself in the product and the code

1. Read `docs/features.md` and `docs/getting-started.md` to understand who uses this
   product, why, and in what physical/operational context (Raspberry Pi UPS fleet
   monitoring, self-hosted/homelab audience, mDNS discovery and adoption, alerting,
   Home Assistant integration).
2. Read `controller/web/src/App.tsx`, `controller/web/src/api.ts`, and
   `controller/web/src/styles.css` to understand every view, state (loading / empty /
   error / success), interaction pattern (toasts, confirm modals, theme preference),
   and the current design tokens.
3. If a live instance of the app is reachable (for example an already-open browser
   page at `http://127.0.0.1:9000/`), walk every view interactively:
   - Fleet dashboard — all three groups: adopted-online, adopted-offline, pending
     adoption.
   - Node detail view.
   - UPS detail view, including the runtime/history chart.
   - Alerts — both alert rules management and alert events.
   - Controller settings.
   - Adoption and "forget node" flows.
   - Theme toggle across system / light / dark.
   - Toast notifications and confirm-modal dialogs (trigger at least one of each).
   - Empty, loading, and error states wherever they can be reasonably triggered.
   If no live instance is reachable, do the walkthrough analytically from the source
   and state clearly in the report that findings are code-derived, not observed live.

## Step 2 — Build personas and capture Voice of Customer

There is no existing user research or live customer base for this product yet, so
personas must be constructed from the actual documented product scope — do not invent
generic SaaS personas unrelated to what this product does.

Define 3–4 personas grounded in `docs/features.md` / `docs/getting-started.md`, for
example (adjust based on what you actually read):

- **Homelab operator** — runs a handful of Pi nodes/UPS units at home, checks the
  dashboard casually, wants reassurance more than depth.
- **Small-scale multi-site sysadmin / informal MSP** — manages UPS fleets across
  several physical locations, cares about fast triage across many nodes at once.
- **Builder / power user** — assembled the Pi fleet themselves, comfortable with
  technical detail, wants full visibility and control (instant commands, OTA, aggregate
  NUT listener).
- **On-call / after-hours responder** — checks status reactively during an outage,
  often on a phone, under time pressure, wants the single most important fact
  immediately.

For each persona, document:

- **Goals** — what they're trying to accomplish.
- **Context of use** — device (desktop vs. phone), urgency, environment.
- **Pain points** — friction you can point to in the actual UI/code.
- **Voice of Customer statements** — 2–3 first-person quotes in the persona's voice
  (e.g. *"I just want to know within 5 seconds if anything is on battery"*), each one
  explicitly mapped to concrete UI evidence (a specific view, state, or code
  reference).

## Step 3 — Heuristic evaluation

Apply Nielsen's 10 usability heuristics to each major view listed in Step 1. For every
heuristic violation found, cite the specific view/element and describe the observable
behavior, not a generic restatement of the heuristic.

## Step 4 — Accessibility pass

Evaluate against WCAG 2.1 AA as the baseline, informed by the
[MUI accessibility guide](https://mui.com/material-ui/guides/accessibility/) even
though MUI is not installed yet — treat it as the target bar this app should already
meet or be moving toward. Check specifically:

- Keyboard-only navigation through every view and dialog.
- Visible focus states (are they ever suppressed or invisible?).
- Color contrast of the custom CSS tokens in `styles.css`, in both light and dark
  themes.
- `aria-label`/role usage, especially on icon-only controls, toasts, and modals.
- Focus trapping and focus restoration on the confirm-modal pattern.

## Step 5 — Visual and interaction consistency pass

Check spacing, typography scale, iconography, and interaction feedback (loading,
empty, error states) for consistency across views. Check responsive behavior at common
breakpoints (narrow/phone, tablet, desktop).

## Step 6 — Structural verdict

Explicitly answer: which of the findings above are **structural** (would recur across
every view because there is no shared component/theming layer) versus **local**
(one-off fixes)? Call out specifically whether the structural findings are the kind
that adopting a component library (this project is moving to MUI) would resolve, versus
findings that are information-architecture or flow problems that a component library
alone will not fix.

## Output format

Produce a single markdown report with exactly these sections, in this order:

1. **Executive Summary** — 3–6 sentences, most important takeaways only.
2. **Personas & Voice of Customer** — one subsection per persona as defined in Step 2.
3. **Heuristic Findings by View** — one subsection per view.
4. **Accessibility Findings**
5. **Visual/Interaction Consistency Findings**
6. **Prioritized Recommendations** — Critical / High / Medium / Low. Each item must
   include: persona(s) impacted, the VoC quote it addresses, current behavior, why it
   matters, a concrete recommendation, and a rough effort estimate (S/M/L).
7. **Quick Wins** — the top 5 lowest-effort, highest-impact fixes, independent of any
   larger redesign or framework migration.
8. **Open Questions** — anything that needs a real user or product-owner answer rather
   than a designer's judgment call.
