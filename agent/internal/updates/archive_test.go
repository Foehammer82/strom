package updates

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"crypto/sha256"
	"encoding/hex"
	"testing"
)

func buildTarGz(t *testing.T, entries []tarEntry) []byte {
	t.Helper()
	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gz)
	for _, entry := range entries {
		header := &tar.Header{
			Name:     entry.name,
			Typeflag: entry.typeflag,
			Mode:     0o755,
			Size:     int64(len(entry.content)),
		}
		if entry.linkname != "" {
			header.Linkname = entry.linkname
		}
		if err := tw.WriteHeader(header); err != nil {
			t.Fatalf("write tar header: %v", err)
		}
		if entry.typeflag == tar.TypeReg {
			if _, err := tw.Write(entry.content); err != nil {
				t.Fatalf("write tar content: %v", err)
			}
		}
	}
	if err := tw.Close(); err != nil {
		t.Fatalf("close tar writer: %v", err)
	}
	if err := gz.Close(); err != nil {
		t.Fatalf("close gzip writer: %v", err)
	}
	return buf.Bytes()
}

type tarEntry struct {
	name     string
	typeflag byte
	content  []byte
	linkname string
}

func sha256Hex(data []byte) string {
	digest := sha256.Sum256(data)
	return hex.EncodeToString(digest[:])
}

func TestExtractAgentBinary(t *testing.T) {
	binaryContent := []byte("fake-binary-content")

	t.Run("valid archive", func(t *testing.T) {
		archive := buildTarGz(t, []tarEntry{
			{name: "strom-agent-v1.0.0-linux-arm64/strom-agent", typeflag: tar.TypeReg, content: binaryContent},
			{name: "strom-agent-v1.0.0-linux-arm64/README.md", typeflag: tar.TypeReg, content: []byte("readme")},
		})
		got, err := ExtractAgentBinary(archive, int64(len(archive)), sha256Hex(archive), sha256Hex(binaryContent))
		if err != nil {
			t.Fatalf("expected success, got error: %v", err)
		}
		if !bytes.Equal(got, binaryContent) {
			t.Fatalf("extracted content mismatch: got %q want %q", got, binaryContent)
		}
	})

	t.Run("wrong expected size", func(t *testing.T) {
		archive := buildTarGz(t, []tarEntry{
			{name: "d/strom-agent", typeflag: tar.TypeReg, content: binaryContent},
		})
		if _, err := ExtractAgentBinary(archive, int64(len(archive))+1, sha256Hex(archive), sha256Hex(binaryContent)); err == nil {
			t.Fatal("expected error for mismatched size")
		}
	})

	t.Run("wrong archive sha256", func(t *testing.T) {
		archive := buildTarGz(t, []tarEntry{
			{name: "d/strom-agent", typeflag: tar.TypeReg, content: binaryContent},
		})
		if _, err := ExtractAgentBinary(archive, int64(len(archive)), sha256Hex([]byte("wrong")), sha256Hex(binaryContent)); err == nil {
			t.Fatal("expected error for mismatched archive sha256")
		}
	})

	t.Run("wrong binary sha256", func(t *testing.T) {
		archive := buildTarGz(t, []tarEntry{
			{name: "d/strom-agent", typeflag: tar.TypeReg, content: binaryContent},
		})
		if _, err := ExtractAgentBinary(archive, int64(len(archive)), sha256Hex(archive), sha256Hex([]byte("wrong"))); err == nil {
			t.Fatal("expected error for mismatched binary sha256")
		}
	})

	t.Run("path traversal rejected", func(t *testing.T) {
		archive := buildTarGz(t, []tarEntry{
			{name: "../../etc/strom-agent", typeflag: tar.TypeReg, content: binaryContent},
		})
		if _, err := ExtractAgentBinary(archive, int64(len(archive)), sha256Hex(archive), sha256Hex(binaryContent)); err == nil {
			t.Fatal("expected error for path traversal entry")
		}
	})

	t.Run("symlink rejected", func(t *testing.T) {
		archive := buildTarGz(t, []tarEntry{
			{name: "d/strom-agent", typeflag: tar.TypeSymlink, linkname: "/usr/bin/evil"},
		})
		if _, err := ExtractAgentBinary(archive, int64(len(archive)), sha256Hex(archive), sha256Hex(binaryContent)); err == nil {
			t.Fatal("expected error for symlink entry")
		}
	})

	t.Run("nested path rejected", func(t *testing.T) {
		archive := buildTarGz(t, []tarEntry{
			{name: "a/b/strom-agent", typeflag: tar.TypeReg, content: binaryContent},
		})
		if _, err := ExtractAgentBinary(archive, int64(len(archive)), sha256Hex(archive), sha256Hex(binaryContent)); err == nil {
			t.Fatal("expected error for entry not at <dir>/strom-agent depth")
		}
	})

	t.Run("duplicate binary entries rejected", func(t *testing.T) {
		archive := buildTarGz(t, []tarEntry{
			{name: "d1/strom-agent", typeflag: tar.TypeReg, content: binaryContent},
			{name: "d2/strom-agent", typeflag: tar.TypeReg, content: binaryContent},
		})
		if _, err := ExtractAgentBinary(archive, int64(len(archive)), sha256Hex(archive), sha256Hex(binaryContent)); err == nil {
			t.Fatal("expected error for duplicate strom-agent entries")
		}
	})

	t.Run("missing binary", func(t *testing.T) {
		archive := buildTarGz(t, []tarEntry{
			{name: "d/README.md", typeflag: tar.TypeReg, content: []byte("readme")},
		})
		if _, err := ExtractAgentBinary(archive, int64(len(archive)), sha256Hex(archive), sha256Hex(binaryContent)); err == nil {
			t.Fatal("expected error when archive has no strom-agent entry")
		}
	})
}
