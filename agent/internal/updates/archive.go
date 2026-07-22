package updates

import (
	"archive/tar"
	"compress/gzip"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"path"
	"strings"
)

// agentBinaryName is the executable file name expected inside every agent
// release tarball, matching the layout produced by
// tools/__main__.py:release_agent_artifacts.
const agentBinaryName = "strom-agent"

// ExtractAgentBinary verifies archiveBytes against the expected size and
// SHA-256 digest, then extracts and returns the single strom-agent
// executable it contains, verified against expectedBinarySHA256.
//
// It deliberately rejects anything that does not look exactly like the
// known-good tarball layout: symlinks/hardlinks, device/fifo entries, more
// than one regular file matching the expected binary name, path traversal,
// or an executable found somewhere other than "<top-level-dir>/strom-agent".
func ExtractAgentBinary(archiveBytes []byte, expectedSize int64, expectedArchiveSHA256, expectedBinarySHA256 string) ([]byte, error) {
	if int64(len(archiveBytes)) != expectedSize {
		return nil, fmt.Errorf("archive size %d does not match expected size %d", len(archiveBytes), expectedSize)
	}
	archiveDigest := sha256.Sum256(archiveBytes)
	if hex.EncodeToString(archiveDigest[:]) != strings.ToLower(expectedArchiveSHA256) {
		return nil, fmt.Errorf("archive sha256 does not match manifest")
	}

	gzipReader, err := gzip.NewReader(strings.NewReader(string(archiveBytes)))
	if err != nil {
		return nil, fmt.Errorf("open gzip archive: %w", err)
	}
	defer func() {
		_ = gzipReader.Close()
	}()

	tarReader := tar.NewReader(gzipReader)
	var (
		binary     []byte
		foundCount int
	)
	for {
		header, err := tarReader.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			return nil, fmt.Errorf("read tar entry: %w", err)
		}

		cleanName := path.Clean(header.Name)
		if strings.HasPrefix(cleanName, "../") || cleanName == ".." || path.IsAbs(cleanName) {
			return nil, fmt.Errorf("archive entry %q escapes the archive root", header.Name)
		}

		baseName := path.Base(cleanName)
		if baseName != agentBinaryName {
			// Anything else (README.md, deploy/*) is expected packaging
			// content that this installer does not use; skip it without
			// reading its content.
			continue
		}

		switch header.Typeflag {
		case tar.TypeReg:
			// fall through to extraction below
		default:
			return nil, fmt.Errorf("archive entry %q has unsupported type %v, want a regular file", header.Name, header.Typeflag)
		}

		segments := strings.Split(cleanName, "/")
		if len(segments) != 2 {
			return nil, fmt.Errorf("archive entry %q is not at the expected <release-dir>/%s path", header.Name, agentBinaryName)
		}

		if foundCount > 0 {
			return nil, fmt.Errorf("archive contains more than one %s entry", agentBinaryName)
		}
		foundCount++

		if header.Size <= 0 {
			return nil, fmt.Errorf("archive entry %q has invalid size %d", header.Name, header.Size)
		}
		const maxBinarySize = 256 * 1024 * 1024
		if header.Size > maxBinarySize {
			return nil, fmt.Errorf("archive entry %q exceeds maximum binary size", header.Name)
		}

		content, err := io.ReadAll(io.LimitReader(tarReader, header.Size+1))
		if err != nil {
			return nil, fmt.Errorf("extract %q: %w", header.Name, err)
		}
		if int64(len(content)) != header.Size {
			return nil, fmt.Errorf("archive entry %q size mismatch", header.Name)
		}
		binary = content
	}

	if foundCount == 0 {
		return nil, fmt.Errorf("archive does not contain a %s executable", agentBinaryName)
	}

	binaryDigest := sha256.Sum256(binary)
	if hex.EncodeToString(binaryDigest[:]) != strings.ToLower(expectedBinarySHA256) {
		return nil, fmt.Errorf("extracted binary sha256 does not match manifest")
	}

	return binary, nil
}
