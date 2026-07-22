package api

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"sort"
	"strings"
	"unicode"

	"github.com/Foehammer82/strom/agent/internal/nutconf"
)

const (
	defaultUPSMetadataPath = "/var/lib/strom/ups-metadata.json"
	maxUPSMetadataText     = 120
	maxUPSMetadataTags     = 12
)

type upsMetadata struct {
	DisplayName string   `json:"display_name,omitempty"`
	Tags        []string `json:"tags,omitempty"`
}

func loadUPSMetadata(path string) (map[string]upsMetadata, error) {
	if strings.TrimSpace(path) == "" {
		path = defaultUPSMetadataPath
	}
	content, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return map[string]upsMetadata{}, nil
		}
		return nil, fmt.Errorf("read UPS metadata: %w", err)
	}
	if len(content) == 0 {
		return map[string]upsMetadata{}, nil
	}

	metadata := map[string]upsMetadata{}
	if err := json.Unmarshal(content, &metadata); err != nil {
		return nil, fmt.Errorf("decode UPS metadata: %w", err)
	}
	for name, value := range metadata {
		normalized, err := normalizeUPSMetadata(value)
		if err != nil {
			return nil, fmt.Errorf("validate UPS metadata for %s: %w", name, err)
		}
		metadata[name] = normalized
	}
	return metadata, nil
}

func saveUPSMetadata(path string, metadata map[string]upsMetadata) (bool, error) {
	if strings.TrimSpace(path) == "" {
		path = defaultUPSMetadataPath
	}
	if metadata == nil {
		metadata = map[string]upsMetadata{}
	}
	content, err := json.MarshalIndent(metadata, "", "  ")
	if err != nil {
		return false, fmt.Errorf("encode UPS metadata: %w", err)
	}
	changed, err := nutconf.WriteIfChanged(path, string(append(content, '\n')))
	if err != nil {
		return false, fmt.Errorf("write UPS metadata: %w", err)
	}
	return changed, nil
}

func normalizeUPSMetadata(metadata upsMetadata) (upsMetadata, error) {
	var err error
	if metadata.DisplayName, err = normalizeUPSMetadataText(metadata.DisplayName, "display name"); err != nil {
		return upsMetadata{}, err
	}
	if len(metadata.Tags) > maxUPSMetadataTags {
		return upsMetadata{}, fmt.Errorf("at most %d tags are allowed", maxUPSMetadataTags)
	}

	tags := make([]string, 0, len(metadata.Tags))
	seen := make(map[string]struct{}, len(metadata.Tags))
	for _, tag := range metadata.Tags {
		normalized, err := normalizeUPSMetadataText(tag, "tag")
		if err != nil {
			return upsMetadata{}, err
		}
		if normalized == "" {
			continue
		}
		key := strings.ToLower(normalized)
		if _, exists := seen[key]; exists {
			continue
		}
		seen[key] = struct{}{}
		tags = append(tags, normalized)
	}
	sort.Slice(tags, func(i, j int) bool { return strings.ToLower(tags[i]) < strings.ToLower(tags[j]) })
	if len(tags) == 0 {
		metadata.Tags = nil
	} else {
		metadata.Tags = tags
	}
	return metadata, nil
}

func normalizeUPSMetadataText(value, field string) (string, error) {
	value = strings.TrimSpace(value)
	if len(value) > maxUPSMetadataText {
		return "", fmt.Errorf("%s must be %d characters or fewer", field, maxUPSMetadataText)
	}
	if strings.IndexFunc(value, unicode.IsControl) >= 0 {
		return "", errors.New("metadata cannot contain control characters")
	}
	return value, nil
}
