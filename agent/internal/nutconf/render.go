package nutconf

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"unicode"
)

const (
	DefaultNamesPath = "/var/lib/strom/names.json"
	maxUPSNameLength = 20
)

type UPSDUser struct {
	Username string
	Password string
}

func AssignStableNames(devices []DetectedUPS, persisted map[string]string) ([]DetectedUPS, map[string]string) {
	assigned := make([]DetectedUPS, len(devices))
	copy(assigned, devices)

	nextMap := make(map[string]string, len(devices))
	usedNames := make(map[string]struct{}, len(devices))

	for index := range assigned {
		device := assigned[index]
		key := device.StableKey()
		preferred := persisted[key]
		name := preferred
		if name == "" {
			name = nextAvailableUPSName(stableNameSource(device), usedNames)
		} else if _, taken := usedNames[name]; taken {
			name = nextAvailableUPSName(stableNameSource(device), usedNames)
		}

		device.Name = name
		assigned[index] = device
		nextMap[key] = name
		usedNames[name] = struct{}{}
	}

	return assigned, nextMap
}

func LoadNameMap(path string) (map[string]string, error) {
	if path == "" {
		path = DefaultNamesPath
	}

	content, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return map[string]string{}, nil
		}
		return nil, fmt.Errorf("read names map: %w", err)
	}

	if len(content) == 0 {
		return map[string]string{}, nil
	}

	var persisted map[string]string
	if err := json.Unmarshal(content, &persisted); err != nil {
		return nil, fmt.Errorf("decode names map: %w", err)
	}

	if persisted == nil {
		return map[string]string{}, nil
	}

	return persisted, nil
}

func SaveNameMap(path string, names map[string]string) (bool, error) {
	if path == "" {
		path = DefaultNamesPath
	}

	content, err := json.MarshalIndent(names, "", "  ")
	if err != nil {
		return false, fmt.Errorf("encode names map: %w", err)
	}
	content = append(content, '\n')

	changed, err := WriteIfChanged(path, string(content))
	if err != nil {
		return false, fmt.Errorf("write names map: %w", err)
	}

	return changed, nil
}

func RenderUPSConf(devices []DetectedUPS) string {
	sorted := make([]DetectedUPS, len(devices))
	copy(sorted, devices)
	sort.Slice(sorted, func(i, j int) bool {
		left := sorted[i].Name
		if left == "" {
			left = sorted[i].StableKey()
		}
		right := sorted[j].Name
		if right == "" {
			right = sorted[j].StableKey()
		}
		return left < right
	})

	var builder strings.Builder
	for index, device := range sorted {
		name := device.Name
		if name == "" {
			name = nextAvailableUPSName(stableNameSource(device), map[string]struct{}{})
		}
		builder.WriteString("[")
		builder.WriteString(name)
		builder.WriteString("]\n")
		builder.WriteString("  driver = ")
		builder.WriteString(device.Driver)
		builder.WriteString("\n")
		builder.WriteString("  port = ")
		builder.WriteString(device.Port)
		builder.WriteString("\n")
		if desc := upsDescription(device); desc != "" {
			builder.WriteString("  desc = ")
			builder.WriteString(desc)
			builder.WriteString("\n")
		}
		if index < len(sorted)-1 {
			builder.WriteString("\n")
		}
	}

	return builder.String()
}

func RenderNutConf() string {
	return "MODE=netserver\n"
}

func RenderUPSDConf() string {
	return "LISTEN 0.0.0.0 3493\nLISTEN :: 3493\n"
}

func RenderUPSDUsers(user UPSDUser) string {
	var builder strings.Builder
	builder.WriteString("# TODO: replace this local plaintext credential with controller provisioning in Phase 3.\n")
	builder.WriteString("[")
	builder.WriteString(user.Username)
	builder.WriteString("]\n")
	builder.WriteString("  password = ")
	builder.WriteString(user.Password)
	builder.WriteString("\n")
	builder.WriteString("  actions = SET\n")
	builder.WriteString("  instcmds = ALL\n")
	return builder.String()
}

func WriteIfChanged(path, content string) (bool, error) {
	newBytes := []byte(content)
	newHash := hashContent(newBytes)

	existingBytes, err := os.ReadFile(path)
	if err == nil && hashContent(existingBytes) == newHash {
		return false, nil
	}
	if err != nil && !os.IsNotExist(err) {
		return false, fmt.Errorf("read existing file: %w", err)
	}

	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return false, fmt.Errorf("create parent dir: %w", err)
	}

	tempFile, err := os.CreateTemp(dir, filepath.Base(path)+".tmp-*")
	if err != nil {
		return false, fmt.Errorf("create temp file: %w", err)
	}
	tempPath := tempFile.Name()
	cleanup := true
	defer func() {
		if cleanup {
			_ = os.Remove(tempPath)
		}
	}()

	if _, err := tempFile.Write(newBytes); err != nil {
		_ = tempFile.Close()
		return false, fmt.Errorf("write temp file: %w", err)
	}
	if err := tempFile.Chmod(0o640); err != nil {
		_ = tempFile.Close()
		return false, fmt.Errorf("chmod temp file: %w", err)
	}
	if err := applyNutOwnership(tempPath); err != nil {
		_ = tempFile.Close()
		return false, fmt.Errorf("apply file ownership: %w", err)
	}
	if err := tempFile.Close(); err != nil {
		return false, fmt.Errorf("close temp file: %w", err)
	}
	if err := os.Rename(tempPath, path); err != nil {
		return false, fmt.Errorf("rename temp file: %w", err)
	}
	if err := os.Chmod(path, 0o640); err != nil {
		return false, fmt.Errorf("chmod final file: %w", err)
	}

	cleanup = false
	return true, nil
}

func stableNameSource(device DetectedUPS) string {
	if device.Serial != "" {
		return device.Serial
	}
	return device.Bus + device.VendorID + device.ProductID
}

func nextAvailableUPSName(source string, used map[string]struct{}) string {
	base := normalizeUPSName(source)
	name := base
	for suffix := 2; ; suffix++ {
		if _, exists := used[name]; !exists {
			return name
		}
		suffixText := fmt.Sprintf("-%d", suffix)
		trimmedBase := trimNameBase(base, len(suffixText))
		name = trimmedBase + suffixText
	}
}

func normalizeUPSName(source string) string {
	var builder strings.Builder
	builder.WriteString("ups-")
	for _, r := range strings.ToLower(source) {
		if unicode.IsDigit(r) || (r >= 'a' && r <= 'z') {
			builder.WriteRune(r)
		}
	}
	name := builder.String()
	if name == "ups-" {
		name = "ups-device"
	}
	if len(name) > maxUPSNameLength {
		name = name[:maxUPSNameLength]
	}
	return name
}

func trimNameBase(base string, suffixLength int) string {
	maxBaseLength := maxUPSNameLength - suffixLength
	if maxBaseLength < 4 {
		maxBaseLength = 4
	}
	if len(base) > maxBaseLength {
		return base[:maxBaseLength]
	}
	return base
}

func upsDescription(device DetectedUPS) string {
	parts := make([]string, 0, 2)
	if device.Vendor != "" {
		parts = append(parts, device.Vendor)
	}
	if device.Product != "" {
		parts = append(parts, device.Product)
	}
	return strings.TrimSpace(strings.Join(parts, " "))
}

func hashContent(content []byte) string {
	sum := sha256.Sum256(content)
	return hex.EncodeToString(sum[:])
}
