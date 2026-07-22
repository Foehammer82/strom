import { useEffect, useRef, useState } from "react";
import Box from "@mui/material/Box";
import Button from "@mui/material/Button";
import Card from "@mui/material/Card";
import CardContent from "@mui/material/CardContent";
import Dialog from "@mui/material/Dialog";
import DialogActions from "@mui/material/DialogActions";
import DialogContent from "@mui/material/DialogContent";
import DialogTitle from "@mui/material/DialogTitle";
import Grid from "@mui/material/Grid";
import IconButton from "@mui/material/IconButton";
import Stack from "@mui/material/Stack";
import TextField from "@mui/material/TextField";
import Typography from "@mui/material/Typography";
import useMediaQuery from "@mui/material/useMediaQuery";
import { useTheme } from "@mui/material/styles";
import CloseIcon from "@mui/icons-material/Close";
import ChartCard from "./ChartCard";
import StatusChip from "./StatusChip";
import { fetchUPSDetail, fetchUPSHistory, runUPSCommand, updateUPSMetadata, type UPSDetailResponse } from "../api";
import { useLiveRefresh } from "../hooks/useLiveRefresh";
import { formatBatteryTrend, formatDuration, humanizeError, statusToSeverity, toChartSeries } from "../lib/format";
import type { ConfirmRequest, ToastSeverity } from "../types";

export default function UPSDetailDialog({
  nodeId,
  upsName,
  onClose,
  onToast,
  requestConfirmation,
}: {
  nodeId: string | null;
  upsName: string | null;
  onClose: () => void;
  onToast: (message: string, severity?: ToastSeverity) => void;
  requestConfirmation: (request: ConfirmRequest) => Promise<boolean>;
}) {
  const theme = useTheme();
  const fullScreen = useMediaQuery(theme.breakpoints.down("sm"));
  const open = Boolean(nodeId && upsName);
  const [detail, setDetail] = useState<UPSDetailResponse | null>(null);
  const [chargeHistory, setChargeHistory] = useState<Array<{ timestamp: string; value: number }>>([]);
  const [loadHistory, setLoadHistory] = useState<Array<{ timestamp: string; value: number }>>([]);
  const [runtimeHistory, setRuntimeHistory] = useState<Array<{ timestamp: string; value: number }>>([]);
  const [error, setError] = useState<string | null>(null);
  const [metadataEditorOpen, setMetadataEditorOpen] = useState(false);
  const [metadataForm, setMetadataForm] = useState({ displayName: "", tags: "" });
  const [savingMetadata, setSavingMetadata] = useState(false);
  // Guards against a slow response for a previous UPS applying its result
  // after the dialog has closed or switched to a different UPS.
  const activeKeyRef = useRef(`${nodeId}/${upsName}`);

  async function load(silent = false) {
    if (!nodeId || !upsName) {
      return;
    }
    const requestedKey = `${nodeId}/${upsName}`;
    try {
      const [detailPayload, charge, loadSamples, runtime] = await Promise.all([
        fetchUPSDetail(nodeId, upsName),
        fetchUPSHistory(nodeId, upsName, "battery.charge"),
        fetchUPSHistory(nodeId, upsName, "ups.load"),
        fetchUPSHistory(nodeId, upsName, "battery.runtime"),
      ]);
      if (activeKeyRef.current !== requestedKey) {
        return;
      }
      setDetail(detailPayload);
      setChargeHistory(toChartSeries(charge.samples));
      setLoadHistory(toChartSeries(loadSamples.samples));
      setRuntimeHistory(toChartSeries(runtime.samples));
      setError(null);
    } catch (loadError) {
      if (activeKeyRef.current !== requestedKey || silent) {
        return;
      }
      setError(humanizeError(loadError));
    }
  }

  useEffect(() => {
    activeKeyRef.current = `${nodeId}/${upsName}`;
    setDetail(null);
    setChargeHistory([]);
    setLoadHistory([]);
    setRuntimeHistory([]);
    setError(null);
    if (nodeId && upsName) {
      void load();
    }
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [nodeId, upsName]);

  useLiveRefresh(() => void load(true), 10_000, open);

  async function handleCommand(commandName: string, destructive: boolean) {
    if (!nodeId || !upsName) {
      return;
    }
    if (destructive) {
      const confirmed = await requestConfirmation({
        title: `Run destructive command ${commandName}?`,
        message: "This command changes UPS state immediately and may impact attached equipment.",
        confirmLabel: "Run command",
        tone: "danger",
      });
      if (!confirmed) {
        return;
      }
    }
    try {
      const payload = await runUPSCommand(nodeId, upsName, commandName);
      onToast(`${payload.command}: ${payload.output || "OK"}`, "success");
      const updated = await fetchUPSDetail(nodeId, upsName);
      setDetail(updated);
    } catch (runError) {
      onToast(humanizeError(runError, "Command failed."), "error");
    }
  }

  function openMetadataEditor() {
    if (!detail) {
      return;
    }
    setMetadataForm({
      displayName: detail.metadata.display_name,
      tags: detail.metadata.tags.join(", "),
    });
    setMetadataEditorOpen(true);
  }

  async function handleMetadataSave() {
    if (!nodeId || !upsName) {
      return;
    }
    setSavingMetadata(true);
    try {
      const response = await updateUPSMetadata(nodeId, upsName, {
        display_name: metadataForm.displayName.trim(),
        tags: metadataForm.tags.split(",").map((tag) => tag.trim()).filter(Boolean),
      });
      setDetail((current) => current ? { ...current, metadata: response.metadata } : current);
      setMetadataEditorOpen(false);
      onToast("UPS details saved.", "success");
    } catch (saveError) {
      onToast(humanizeError(saveError, "Unable to save UPS details."), "error");
    } finally {
      setSavingMetadata(false);
    }
  }

  return (
  <>
    <Dialog open={open} onClose={onClose} maxWidth="md" fullWidth fullScreen={fullScreen}>
      <DialogTitle sx={{ display: "flex", alignItems: "flex-start", justifyContent: "space-between", gap: 1.5 }}>
        <Box sx={{ minWidth: 0 }}>
          <Typography variant="h6" component="span" sx={{ display: "block" }}>
      {detail?.metadata.display_name || detail?.name || upsName || "UPS detail"}
          </Typography>
          <Typography variant="body2" color="text.secondary">
      {detail?.metadata.display_name ? `${detail.name} - ` : ""}Live controller-side UPS detail with 24h charts and instant commands.
          </Typography>
			{detail?.live ? <Button size="small" onClick={openMetadataEditor} sx={{ mt: 0.5 }}>Edit details</Button> : null}
        </Box>
        <IconButton onClick={onClose} aria-label="Close UPS detail" size="small">
          <CloseIcon fontSize="small" />
        </IconButton>
      </DialogTitle>
      <DialogContent dividers>
        {error ? (
          <Box sx={{ p: 2.5, border: 1, borderColor: "divider", borderStyle: "dashed", borderRadius: 1.5 }}>
            <Typography color="text.secondary">{error}</Typography>
          </Box>
        ) : !detail ? (
          <Box sx={{ p: 2.5, border: 1, borderColor: "divider", borderStyle: "dashed", borderRadius: 1.5 }}>
            <Typography color="text.secondary">Loading UPS detail...</Typography>
          </Box>
        ) : (
          <>
            <Grid container spacing={1.75} sx={{ mb: 2.5 }}>
              {[
                ["Status", detail.status || detail.metrics?.status || "unknown"],
                ["Battery", detail.metrics?.battery_charge_percent != null ? `${detail.metrics.battery_charge_percent}%` : "unknown"],
                ["Load", detail.metrics?.load_percent != null ? `${detail.metrics.load_percent}%` : "unknown"],
                ["Runtime", detail.metrics?.runtime_seconds != null ? formatDuration(detail.metrics.runtime_seconds) : "unknown"],
                ["Battery trend", formatBatteryTrend(detail.battery_runtime_trend)],
              ].map(([label, value]) => (
                <Grid key={label} size={{ xs: 12, sm: 6, md: 2.4 }}>
                  <Card variant="outlined">
                    <CardContent>
                      <Typography variant="overline" color="text.secondary">
                        {label}
                      </Typography>
                      {label === "Status" ? (
                        <Box sx={{ mt: 0.5 }}>
                          <StatusChip label={value} severity={statusToSeverity(value)} />
                        </Box>
                      ) : (
                        <Typography variant="h6" component="strong" sx={{ display: "block" }}>
                          {value}
                        </Typography>
                      )}
                    </CardContent>
                  </Card>
                </Grid>
              ))}
            </Grid>

      {detail.metadata.tags.length ? (
        <Box sx={{ mb: 2.5 }}>
          <Typography variant="h6" component="h3" sx={{ m: 0 }}>
            UPS details
          </Typography>
          <Stack direction="row" spacing={1.5} useFlexGap sx={{ mt: 1, flexWrap: "wrap" }}>
            {detail.metadata.tags.map((tag) => <Typography key={tag} variant="caption" sx={{ px: 0.75, py: 0.25, border: 1, borderColor: "divider", borderRadius: 1 }}>{tag}</Typography>)}
          </Stack>
        </Box>
      ) : null}

            <Grid container spacing={1.75} sx={{ mb: 2.5 }}>
              <Grid size={{ xs: 12, md: 4 }}>
                <ChartCard title="Battery charge" data={chargeHistory} tone="primary" />
              </Grid>
              <Grid size={{ xs: 12, md: 4 }}>
                <ChartCard title="UPS load" data={loadHistory} tone="warning" />
              </Grid>
              <Grid size={{ xs: 12, md: 4 }}>
                <ChartCard title="Runtime" data={runtimeHistory} tone="success" />
              </Grid>
            </Grid>

            <Stack spacing={2.5}>
              <Box>
                <Typography variant="h6" component="h3" sx={{ m: 0 }}>
                  Instant commands
                </Typography>
                <Typography variant="body2" color="text.secondary" sx={{ mb: 1.5 }}>
                  Trusted controller-side passthrough over the pinned HTTPS channel.
                </Typography>
                {!detail.commands?.length ? (
                  <Box sx={{ p: 2.5, border: 1, borderColor: "divider", borderStyle: "dashed", borderRadius: 1.5 }}>
                    <Typography color="text.secondary">This UPS does not report any instant commands.</Typography>
                  </Box>
                ) : (
                  <Grid container spacing={1.75}>
                    {detail.commands.map((command) => (
                      <Grid key={command.name} size={{ xs: 12, sm: 6, lg: 4 }}>
                        <Card variant="outlined" sx={{ height: "100%" }}>
                          <CardContent>
                            <Typography variant="overline" color="text.secondary">
                              {command.destructive ? "Destructive" : "Command"}
                            </Typography>
                            <Typography variant="h6" component="strong" sx={{ display: "block" }}>
                              {command.name}
                            </Typography>
                            <Typography variant="body2" color="text.secondary" sx={{ mb: 1.5 }}>
                              {command.description || "No description reported by NUT."}
                            </Typography>
                            <Button
                              variant={command.destructive ? "outlined" : "contained"}
                              color={command.destructive ? "error" : "primary"}
                              size="small"
                              onClick={() => void handleCommand(command.name, command.destructive)}
                            >
                              {command.destructive ? "Confirm and run" : "Run command"}
                            </Button>
                          </CardContent>
                        </Card>
                      </Grid>
                    ))}
                  </Grid>
                )}
              </Box>

              <Box>
                <Typography variant="h6" component="h3" sx={{ m: 0 }}>
                  Latest variables
                </Typography>
                <Typography variant="body2" color="text.secondary" sx={{ mb: 1.5 }}>
                  Raw NUT variables from the most recent trusted/live or stored UPS detail.
                </Typography>
                <Grid container spacing={1.5}>
                  {Object.entries(detail.variables)
                    .sort(([left], [right]) => left.localeCompare(right))
                    .map(([key, value]) => (
                      <Grid key={key} size={{ xs: 12, sm: 6, md: 3 }}>
                        <Card variant="outlined">
                          <CardContent>
                            <Typography variant="overline" color="text.secondary">
                              {key}
                            </Typography>
                            <Typography variant="body1" component="strong" sx={{ display: "block", wordBreak: "break-word" }}>
                              {value}
                            </Typography>
                          </CardContent>
                        </Card>
                      </Grid>
                    ))}
                </Grid>
              </Box>
            </Stack>
          </>
        )}
      </DialogContent>
    </Dialog>
    <Dialog open={metadataEditorOpen} onClose={() => !savingMetadata && setMetadataEditorOpen(false)} maxWidth="sm" fullWidth>
      <DialogTitle>Edit UPS details</DialogTitle>
      <DialogContent>
        <Stack spacing={2} sx={{ pt: 1 }}>
          <TextField label="Friendly name" value={metadataForm.displayName} onChange={(event) => setMetadataForm((current) => ({ ...current, displayName: event.target.value }))} placeholder={detail?.name ?? upsName ?? ""} slotProps={{ htmlInput: { maxLength: 120 } }} autoFocus />
          <TextField label="Tags" value={metadataForm.tags} onChange={(event) => setMetadataForm((current) => ({ ...current, tags: event.target.value }))} helperText="Separate tags with commas." slotProps={{ htmlInput: { maxLength: 120 } }} />
        </Stack>
      </DialogContent>
      <DialogActions>
        <Button onClick={() => setMetadataEditorOpen(false)} disabled={savingMetadata}>Cancel</Button>
        <Button variant="contained" onClick={() => void handleMetadataSave()} disabled={savingMetadata}>Save details</Button>
      </DialogActions>
    </Dialog>
  </>
  );
}
