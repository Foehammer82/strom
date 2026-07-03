package nutconf

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestAssignStableNamesPersistsAndCollidesDeterministically(t *testing.T) {
	t.Parallel()

	devices := []DetectedUPS{
		{Driver: "usbhid-ups", Port: "auto", Serial: "ABC-123"},
		{Driver: "usbhid-ups", Port: "auto", Serial: "ABC 123"},
		{Driver: "usbhid-ups", Port: "auto", Bus: "002", VendorID: "051d", ProductID: "0002"},
	}
	persisted := map[string]string{
		devices[2].StableKey(): "ups-fallbackkept",
	}

	assigned, nextMap := AssignStableNames(devices, persisted)

	if assigned[0].Name != "ups-abc123" {
		t.Fatalf("first name = %q, want %q", assigned[0].Name, "ups-abc123")
	}
	if assigned[1].Name != "ups-abc123-2" {
		t.Fatalf("second name = %q, want %q", assigned[1].Name, "ups-abc123-2")
	}
	if assigned[2].Name != "ups-fallbackkept" {
		t.Fatalf("fallback name = %q, want persisted value", assigned[2].Name)
	}
	if nextMap[devices[0].StableKey()] != "ups-abc123" {
		t.Fatalf("stored name mismatch: %#v", nextMap)
	}
	if len(assigned[1].Name) > maxUPSNameLength {
		t.Fatalf("collided name too long: %q", assigned[1].Name)
	}
	if len(assigned[2].Name) > maxUPSNameLength {
		t.Fatalf("persisted name unexpectedly changed: %q", assigned[2].Name)
	}
	if nextMap[devices[2].StableKey()] != "ups-fallbackkept" {
		t.Fatalf("persisted mapping lost: %#v", nextMap)
	}
	if assigned[2].StableKey() != "fallback:002:051d:0002" {
		t.Fatalf("fallback stable key mismatch: %q", assigned[2].StableKey())
	}
	if len(nextMap) != 3 {
		t.Fatalf("name map length = %d, want 3", len(nextMap))
	}
	if assigned[0].Name == assigned[1].Name {
		t.Fatal("colliding serials should receive unique names")
	}
	if assigned[0].Name == "" || assigned[1].Name == "" || assigned[2].Name == "" {
		t.Fatal("all devices should receive names")
	}
	if strings.Contains(assigned[0].Name, "-") && assigned[0].Name != "ups-abc123" {
		t.Fatalf("unexpected normalization: %q", assigned[0].Name)
	}
	if _, ok := nextMap[devices[1].StableKey()]; !ok {
		t.Fatalf("missing persisted mapping for %q", devices[1].StableKey())
	}
	if nextMap[devices[1].StableKey()] != "ups-abc123-2" {
		t.Fatalf("second persisted mapping mismatch: %#v", nextMap)
	}
	if nextMap[devices[2].StableKey()] == nextMap[devices[1].StableKey()] {
		t.Fatalf("distinct devices should not share persisted names: %#v", nextMap)
	}
	if assigned[2].Port != "auto" {
		t.Fatalf("assign should preserve original fields: %#v", assigned[2])
	}
	if assigned[2].Driver != "usbhid-ups" {
		t.Fatalf("assign should preserve driver: %#v", assigned[2])
	}
	if assigned[2].VendorID != "051d" {
		t.Fatalf("assign should preserve vendorid: %#v", assigned[2])
	}
	if assigned[2].ProductID != "0002" {
		t.Fatalf("assign should preserve productid: %#v", assigned[2])
	}
	if assigned[2].Bus != "002" {
		t.Fatalf("assign should preserve bus: %#v", assigned[2])
	}
	if assigned[2].Serial != "" {
		t.Fatalf("fallback device should still have empty serial: %#v", assigned[2])
	}
	if strings.ContainsAny(assigned[0].Name, "ABCDEFGHIJKLMNOPQRSTUVWXYZ ") {
		t.Fatalf("normalized name contains invalid chars: %q", assigned[0].Name)
	}
	if len(assigned[0].Name) > maxUPSNameLength {
		t.Fatalf("normalized name too long: %q", assigned[0].Name)
	}
	if len(assigned[1].Name) > maxUPSNameLength {
		t.Fatalf("normalized collision name too long: %q", assigned[1].Name)
	}
	if nextMap[devices[0].StableKey()] == nextMap[devices[2].StableKey()] {
		t.Fatalf("serial and fallback keys should not share names: %#v", nextMap)
	}
	if assigned[0].Name != nextMap[devices[0].StableKey()] {
		t.Fatalf("assigned name should match stored map: %#v %#v", assigned, nextMap)
	}
	if assigned[1].Name != nextMap[devices[1].StableKey()] {
		t.Fatalf("assigned name should match stored map: %#v %#v", assigned, nextMap)
	}
	if assigned[2].Name != nextMap[devices[2].StableKey()] {
		t.Fatalf("assigned name should match stored map: %#v %#v", assigned, nextMap)
	}
	if nextMap[devices[2].StableKey()] != persisted[devices[2].StableKey()] {
		t.Fatalf("persisted fallback name should remain stable: %#v", nextMap)
	}
	if !strings.HasPrefix(assigned[0].Name, "ups-") || !strings.HasPrefix(assigned[1].Name, "ups-") || !strings.HasPrefix(assigned[2].Name, "ups-") {
		t.Fatalf("all names should be ups-prefixed: %#v", assigned)
	}
	if assigned[0].Name == assigned[2].Name {
		t.Fatalf("name reuse across different keys is not allowed: %#v", assigned)
	}
	if assigned[1].Name == assigned[2].Name {
		t.Fatalf("name reuse across different keys is not allowed: %#v", assigned)
	}
	if !strings.HasSuffix(assigned[1].Name, "-2") {
		t.Fatalf("collision suffix missing: %q", assigned[1].Name)
	}
	if strings.Contains(assigned[0].Name, "_") || strings.Contains(assigned[1].Name, "_") {
		t.Fatalf("underscores should be stripped from names: %#v", assigned)
	}
	if len(strings.TrimPrefix(assigned[0].Name, "ups-")) == 0 {
		t.Fatalf("normalized name missing body: %q", assigned[0].Name)
	}
	if len(strings.TrimPrefix(assigned[1].Name, "ups-")) == 0 {
		t.Fatalf("normalized collision name missing body: %q", assigned[1].Name)
	}
	if len(strings.TrimPrefix(assigned[2].Name, "ups-")) == 0 {
		t.Fatalf("fallback name missing body: %q", assigned[2].Name)
	}
	if assigned[0].Name != "ups-abc123" {
		t.Fatalf("unexpected first assignment: %#v", assigned)
	}
	if assigned[1].Name != "ups-abc123-2" {
		t.Fatalf("unexpected second assignment: %#v", assigned)
	}
	if assigned[2].Name != "ups-fallbackkept" {
		t.Fatalf("unexpected third assignment: %#v", assigned)
	}
	if len(nextMap) != len(devices) {
		t.Fatalf("nextMap length = %d, want %d", len(nextMap), len(devices))
	}
	if assigned[0].Name == nextMap[devices[1].StableKey()] {
		t.Fatalf("collision should have forced distinct persisted name: %#v", nextMap)
	}
	if assigned[1].Name == nextMap[devices[0].StableKey()] {
		t.Fatalf("collision should have forced distinct persisted name: %#v", nextMap)
	}
	if strings.Contains(assigned[0].Name, " ") || strings.Contains(assigned[1].Name, " ") {
		t.Fatalf("spaces should be stripped from names: %#v", assigned)
	}
	if assigned[0].Name == "ups-abc-123" {
		t.Fatalf("punctuation should be stripped from names: %q", assigned[0].Name)
	}
	if assigned[0].Name == "ups-abc 123" {
		t.Fatalf("whitespace should be stripped from names: %q", assigned[0].Name)
	}
	if !strings.Contains(RenderUPSConf(assigned), "[ups-abc123]") {
		t.Fatalf("render should use assigned names: %s", RenderUPSConf(assigned))
	}
	if !strings.Contains(RenderUPSConf(assigned), "[ups-fallbackkept]") {
		t.Fatalf("render should use persisted fallback name: %s", RenderUPSConf(assigned))
	}
	if count := strings.Count(RenderUPSConf(assigned), "["); count != 3 {
		t.Fatalf("render should emit one section per UPS, got %d", count)
	}
	if !strings.Contains(RenderUPSConf(assigned), "driver = usbhid-ups") {
		t.Fatalf("render missing driver lines: %s", RenderUPSConf(assigned))
	}
	if !strings.Contains(RenderUPSConf(assigned), "port = auto") {
		t.Fatalf("render missing port lines: %s", RenderUPSConf(assigned))
	}
	if strings.Contains(RenderUPSConf(assigned), "ABC") {
		t.Fatalf("render should use normalized names, not raw serials: %s", RenderUPSConf(assigned))
	}
	if !strings.HasSuffix(RenderNutConf(), "\n") || !strings.HasSuffix(RenderUPSDConf(), "\n") {
		t.Fatal("render helpers should end with a newline")
	}
	if !strings.Contains(RenderUPSDUsers(UPSDUser{Username: "agent", Password: "secret"}), "Phase 3") {
		t.Fatal("upsd.users renderer should keep the provisioning TODO")
	}
	if !strings.Contains(RenderUPSDUsers(UPSDUser{Username: "agent", Password: "secret"}), "password = secret") {
		t.Fatal("upsd.users renderer should include plaintext password for phase 1")
	}
	if !strings.Contains(RenderUPSDUsers(UPSDUser{Username: "agent", Password: "secret"}), "[agent]") {
		t.Fatal("upsd.users renderer should include the user section")
	}
	if !strings.Contains(RenderUPSDConf(), "LISTEN :: 3493") {
		t.Fatal("upsd.conf renderer should include IPv6 listen")
	}
	if RenderNutConf() != "MODE=netserver\n" {
		t.Fatalf("nut.conf renderer mismatch: %q", RenderNutConf())
	}
	if !strings.Contains(RenderUPSDConf(), "LISTEN 0.0.0.0 3493") {
		t.Fatal("upsd.conf renderer should include IPv4 listen")
	}
	if !strings.Contains(RenderUPSConf(assigned), "\n\n[") {
		t.Fatal("ups.conf renderer should separate sections with blank lines")
	}
	if strings.HasPrefix(RenderUPSConf(nil), "[") {
		t.Fatal("empty render should produce empty output")
	}
	if RenderUPSConf(nil) != "" {
		t.Fatalf("empty render mismatch: %q", RenderUPSConf(nil))
	}
	if nextAvailableUPSName("serial", map[string]struct{}{"ups-serial": {}}) != "ups-serial-2" {
		t.Fatal("collision helper should append -2")
	}
	if normalizeUPSName("ABCDEFGHIJ1234567890XYZ") != "ups-abcdefghij123456" {
		t.Fatalf("normalize truncation mismatch: %q", normalizeUPSName("ABCDEFGHIJ1234567890XYZ"))
	}
	if normalizeUPSName("***") != "ups-device" {
		t.Fatalf("normalize fallback mismatch: %q", normalizeUPSName("***"))
	}
	if stableNameSource(devices[2]) != "002051d0002" {
		t.Fatalf("fallback stable source mismatch: %q", stableNameSource(devices[2]))
	}
	if stableNameSource(devices[0]) != "ABC-123" {
		t.Fatalf("serial stable source mismatch: %q", stableNameSource(devices[0]))
	}
	if trimNameBase("ups-abcdefghijklmnop", 2) != "ups-abcdefghijklmn" {
		t.Fatalf("trimNameBase mismatch: %q", trimNameBase("ups-abcdefghijklmnop", 2))
	}
	if hashContent([]byte("a")) == hashContent([]byte("b")) {
		t.Fatal("hash helper should distinguish different content")
	}
	if upsDescription(DetectedUPS{Vendor: "APC", Product: "Back-UPS"}) != "APC Back-UPS" {
		t.Fatalf("unexpected description: %q", upsDescription(DetectedUPS{Vendor: "APC", Product: "Back-UPS"}))
	}
	if upsDescription(DetectedUPS{}) != "" {
		t.Fatalf("empty description mismatch: %q", upsDescription(DetectedUPS{}))
	}
	if !strings.Contains(RenderUPSConf([]DetectedUPS{{Name: "ups-one", Driver: "dummy", Port: "auto", Vendor: "APC", Product: "Back-UPS"}}), "desc = APC Back-UPS") {
		t.Fatal("ups.conf renderer should include desc when available")
	}
	if !strings.Contains(RenderUPSConf([]DetectedUPS{{Driver: "dummy", Port: "auto", Serial: "SERIAL"}}), "[ups-serial]") {
		t.Fatal("ups.conf renderer should derive a name when none is preassigned")
	}
	if strings.Contains(RenderUPSConf([]DetectedUPS{{Name: "ups-one", Driver: "dummy", Port: "auto"}}), "desc =") {
		t.Fatal("ups.conf renderer should omit desc when empty")
	}
	if RenderUPSDUsers(UPSDUser{}) == "" {
		t.Fatal("upsd.users renderer should still render deterministic output")
	}
	if !strings.HasPrefix(RenderUPSDUsers(UPSDUser{}), "# TODO:") {
		t.Fatal("upsd.users renderer should start with the phase TODO comment")
	}
	if !strings.Contains(RenderUPSDUsers(UPSDUser{}), "actions = SET") {
		t.Fatal("upsd.users renderer should grant actions access")
	}
	if !strings.Contains(RenderUPSDUsers(UPSDUser{}), "instcmds = ALL") {
		t.Fatal("upsd.users renderer should grant command access")
	}
	if strings.Contains(RenderUPSDConf(), "MODE=") {
		t.Fatal("upsd.conf renderer should only render listen directives")
	}
	if strings.Contains(RenderNutConf(), "LISTEN") {
		t.Fatal("nut.conf renderer should only render mode")
	}
	if strings.Contains(RenderUPSConf(assigned), "serial:") {
		t.Fatal("ups.conf renderer should not expose stable-key internals")
	}
	if strings.Contains(RenderUPSConf(assigned), "fallback:") {
		t.Fatal("ups.conf renderer should not expose fallback stable-key internals")
	}
	if strings.Count(RenderUPSConf(assigned), "driver =") != 3 {
		t.Fatalf("unexpected driver count: %s", RenderUPSConf(assigned))
	}
	if strings.Count(RenderUPSConf(assigned), "port =") != 3 {
		t.Fatalf("unexpected port count: %s", RenderUPSConf(assigned))
	}
	if strings.Count(RenderUPSConf(assigned), "desc =") != 0 {
		t.Fatalf("unexpected desc count for devices without metadata: %s", RenderUPSConf(assigned))
	}
	if normalizeUPSName("ABC123") == "ABC123" {
		t.Fatal("normalized names should be lowercase and prefixed")
	}
	if normalizeUPSName("abc123") != "ups-abc123" {
		t.Fatalf("normalize basic mismatch: %q", normalizeUPSName("abc123"))
	}
	if normalizeUPSName("abc123") == normalizeUPSName("ABC123") {
		// expected equality; keep assertion explicit to avoid accidental behavior drift.
	} else {
		t.Fatalf("normalize should be case-insensitive: %q vs %q", normalizeUPSName("abc123"), normalizeUPSName("ABC123"))
	}
	if trimNameBase("ups-short", 8) != "ups-short" {
		t.Fatalf("short base should not be trimmed: %q", trimNameBase("ups-short", 8))
	}
	if trimNameBase("ups-abcdefghijklmno", 4) != "ups-abcdefghijkl" {
		t.Fatalf("trimmed base mismatch: %q", trimNameBase("ups-abcdefghijklmno", 4))
	}
	if nextAvailableUPSName("ABC-123", map[string]struct{}{}) != "ups-abc123" {
		t.Fatalf("name helper mismatch: %q", nextAvailableUPSName("ABC-123", map[string]struct{}{}))
	}
	if nextAvailableUPSName("ABC-123", map[string]struct{}{"ups-abc123": {}, "ups-abc123-2": {}}) != "ups-abc123-3" {
		t.Fatalf("name helper collision mismatch: %q", nextAvailableUPSName("ABC-123", map[string]struct{}{"ups-abc123": {}, "ups-abc123-2": {}}))
	}
	if normalizeUPSName("12345678901234567890") != "ups-1234567890123456" {
		t.Fatalf("numeric truncation mismatch: %q", normalizeUPSName("12345678901234567890"))
	}
	if normalizeUPSName("apc_serial-1") != "ups-apcserial1" {
		t.Fatalf("punctuation stripping mismatch: %q", normalizeUPSName("apc_serial-1"))
	}
	if trimNameBase("ups-1234567890123456", 2) != "ups-12345678901234" {
		t.Fatalf("trimmed numeric base mismatch: %q", trimNameBase("ups-1234567890123456", 2))
	}
	if trimNameBase("ups-1234", 50) != "ups-" {
		t.Fatalf("minimum trim length mismatch: %q", trimNameBase("ups-1234", 50))
	}
	if nextAvailableUPSName("***", map[string]struct{}{}) != "ups-device" {
		t.Fatalf("empty-source fallback mismatch: %q", nextAvailableUPSName("***", map[string]struct{}{}))
	}
	if nextAvailableUPSName("***", map[string]struct{}{"ups-device": {}}) != "ups-device-2" {
		t.Fatalf("empty-source collision mismatch: %q", nextAvailableUPSName("***", map[string]struct{}{"ups-device": {}}))
	}
}

func TestLoadSaveNameMapAndWriteIfChanged(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	path := filepath.Join(dir, "names.json")

	loaded, err := LoadNameMap(path)
	if err != nil {
		t.Fatalf("LoadNameMap() missing file error = %v", err)
	}
	if len(loaded) != 0 {
		t.Fatalf("expected empty map for missing file, got %#v", loaded)
	}

	changed, err := SaveNameMap(path, map[string]string{"serial:abc": "ups-abc"})
	if err != nil {
		t.Fatalf("SaveNameMap() error = %v", err)
	}
	if !changed {
		t.Fatal("first save should report changed")
	}

	loaded, err = LoadNameMap(path)
	if err != nil {
		t.Fatalf("LoadNameMap() error = %v", err)
	}
	if loaded["serial:abc"] != "ups-abc" {
		t.Fatalf("loaded map mismatch: %#v", loaded)
	}

	changed, err = SaveNameMap(path, map[string]string{"serial:abc": "ups-abc"})
	if err != nil {
		t.Fatalf("SaveNameMap() second call error = %v", err)
	}
	if changed {
		t.Fatal("second identical save should not report changed")
	}

	textPath := filepath.Join(dir, "ups.conf")
	changed, err = WriteIfChanged(textPath, "abc\n")
	if err != nil {
		t.Fatalf("WriteIfChanged() error = %v", err)
	}
	if !changed {
		t.Fatal("first write should report changed")
	}

	content, err := os.ReadFile(textPath)
	if err != nil {
		t.Fatalf("read written file: %v", err)
	}
	if string(content) != "abc\n" {
		t.Fatalf("written content mismatch: %q", string(content))
	}

	changed, err = WriteIfChanged(textPath, "abc\n")
	if err != nil {
		t.Fatalf("WriteIfChanged() identical error = %v", err)
	}
	if changed {
		t.Fatal("identical write should not report changed")
	}

	changed, err = WriteIfChanged(textPath, "abcd\n")
	if err != nil {
		t.Fatalf("WriteIfChanged() update error = %v", err)
	}
	if !changed {
		t.Fatal("content update should report changed")
	}

	content, err = os.ReadFile(textPath)
	if err != nil {
		t.Fatalf("read updated file: %v", err)
	}
	if string(content) != "abcd\n" {
		t.Fatalf("updated content mismatch: %q", string(content))
	}
	info, err := os.Stat(textPath)
	if err != nil {
		t.Fatalf("stat updated file: %v", err)
	}
	if runtime.GOOS != "windows" && info.Mode().Perm() != 0o640 {
		t.Fatalf("file mode = %o, want 640", info.Mode().Perm())
	}

	jsonContent, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read names map: %v", err)
	}
	if !strings.HasSuffix(string(jsonContent), "\n") {
		t.Fatalf("names map should end with newline: %q", string(jsonContent))
	}
	if !strings.Contains(string(jsonContent), "\"serial:abc\": \"ups-abc\"") {
		t.Fatalf("names map content mismatch: %q", string(jsonContent))
	}
}
