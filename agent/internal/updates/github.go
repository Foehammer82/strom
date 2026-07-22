package updates

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

const (
	defaultGitHubAPIBaseURL = "https://api.github.com"
	defaultRepository       = "Foehammer82/strom"
	userAgentPrefix         = "strom-agent-updates"

	// maxReleaseResponseBytes bounds the GitHub release metadata response.
	// GitHub release JSON for this repo is a few KB; this is a generous cap
	// that still prevents unbounded memory use from a misbehaving or
	// compromised endpoint.
	maxReleaseResponseBytes = 2 * 1024 * 1024

	// maxAssetBytes bounds any individual downloaded release asset. The
	// manifest and signature are tiny; agent tarballs are a few MB, so this
	// cap is generous while still preventing unbounded downloads.
	maxAssetBytes = 128 * 1024 * 1024
)

// releaseAsset mirrors the subset of the GitHub release asset schema this
// package uses.
type releaseAsset struct {
	Name               string `json:"name"`
	Size               int64  `json:"size"`
	BrowserDownloadURL string `json:"browser_download_url"`
}

// Release mirrors the subset of the GitHub release schema this package
// uses.
type Release struct {
	TagName    string         `json:"tag_name"`
	Draft      bool           `json:"draft"`
	Prerelease bool           `json:"prerelease"`
	HTMLURL    string         `json:"html_url"`
	Assets     []releaseAsset `json:"assets"`
}

// AssetByName returns the first asset with an exact filename match, or false
// if none exists. Matching is by exact name only; nothing in this package
// ever guesses or constructs a download URL.
func (r Release) AssetByName(name string) (releaseAsset, bool) {
	for _, asset := range r.Assets {
		if asset.Name == name {
			return asset, true
		}
	}
	return releaseAsset{}, false
}

// GitHubClient fetches public release metadata and assets for the Strom
// repository. It never authenticates and only ever reads public data.
type GitHubClient struct {
	// HTTPClient is the client used for all requests. Its Transport's
	// redirect policy is not modified by this package; callers embedding
	// this in production code should provide a client whose CheckRedirect
	// enforces an HTTPS + GitHub-controlled host allowlist (see
	// NewDownloadHTTPClient).
	HTTPClient *http.Client
	// BaseURL defaults to the public GitHub API. Overridable for tests.
	BaseURL string
	// Repository is "owner/repo". Defaults to the public Strom repository.
	Repository string
	// AgentVersion is included in the User-Agent header for observability.
	AgentVersion string
	// ValidateDownloadURL validates an asset download URL before it is
	// fetched. Defaults to validateDownloadURL (HTTPS + GitHub-controlled
	// host allowlist). Tests may override this to point at a local fixture
	// server; production code should leave it unset.
	ValidateDownloadURL func(rawURL string) error
}

func (c *GitHubClient) httpClient() *http.Client {
	if c.HTTPClient != nil {
		return c.HTTPClient
	}
	return &http.Client{Timeout: 15 * time.Second}
}

func (c *GitHubClient) baseURL() string {
	if strings.TrimSpace(c.BaseURL) != "" {
		return strings.TrimRight(c.BaseURL, "/")
	}
	return defaultGitHubAPIBaseURL
}

func (c *GitHubClient) repository() string {
	if strings.TrimSpace(c.Repository) != "" {
		return c.Repository
	}
	return defaultRepository
}

func (c *GitHubClient) userAgent() string {
	version := strings.TrimSpace(c.AgentVersion)
	if version == "" {
		version = "dev"
	}
	return fmt.Sprintf("%s/%s", userAgentPrefix, version)
}

// LatestRelease fetches the latest stable (non-draft, non-prerelease)
// GitHub release. GitHub's "/releases/latest" endpoint already excludes
// drafts and prereleases; this method double-checks those flags and the tag
// shape as defense in depth before returning.
func (c *GitHubClient) LatestRelease(ctx context.Context) (Release, error) {
	requestURL := fmt.Sprintf("%s/repos/%s/releases/latest", c.baseURL(), c.repository())
	body, err := c.getBounded(ctx, requestURL, maxReleaseResponseBytes)
	if err != nil {
		return Release{}, err
	}
	var release Release
	if err := json.Unmarshal(body, &release); err != nil {
		return Release{}, fmt.Errorf("decode GitHub release response: %w", err)
	}
	if release.Draft {
		return Release{}, fmt.Errorf("latest release %s is a draft", release.TagName)
	}
	if release.Prerelease {
		return Release{}, fmt.Errorf("latest release %s is a prerelease", release.TagName)
	}
	if !IsStableVersion(release.TagName) {
		return Release{}, fmt.Errorf("latest release tag %q is not a stable vMAJOR.MINOR.PATCH version", release.TagName)
	}
	return release, nil
}

// DownloadAsset downloads a release asset by its exact browser_download_url,
// as returned from a Release fetched via LatestRelease. It enforces the
// maxAssetBytes cap and returns an error if the server reports or delivers
// more than that.
func (c *GitHubClient) DownloadAsset(ctx context.Context, downloadURL string) ([]byte, error) {
	validate := c.ValidateDownloadURL
	if validate == nil {
		validate = validateDownloadURL
	}
	if err := validate(downloadURL); err != nil {
		return nil, err
	}
	return c.getBounded(ctx, downloadURL, maxAssetBytes)
}

func (c *GitHubClient) getBounded(ctx context.Context, requestURL string, limit int64) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, requestURL, nil)
	if err != nil {
		return nil, fmt.Errorf("build request for %s: %w", requestURL, err)
	}
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("User-Agent", c.userAgent())

	response, err := c.httpClient().Do(req)
	if err != nil {
		return nil, fmt.Errorf("request %s: %w", requestURL, err)
	}
	defer func() {
		_ = response.Body.Close()
	}()

	if response.StatusCode == http.StatusForbidden || response.StatusCode == http.StatusTooManyRequests {
		return nil, fmt.Errorf("GitHub rate limit or access denied (status %d) for %s", response.StatusCode, requestURL)
	}
	if response.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("unexpected status %d for %s", response.StatusCode, requestURL)
	}
	if response.ContentLength > limit {
		return nil, fmt.Errorf("response for %s exceeds %d byte limit", requestURL, limit)
	}

	limited := io.LimitReader(response.Body, limit+1)
	body, err := io.ReadAll(limited)
	if err != nil {
		return nil, fmt.Errorf("read response for %s: %w", requestURL, err)
	}
	if int64(len(body)) > limit {
		return nil, fmt.Errorf("response for %s exceeds %d byte limit", requestURL, limit)
	}
	return body, nil
}

// validateDownloadURL rejects anything that is not an HTTPS URL on a host
// GitHub controls for release asset delivery. This is defense in depth on
// top of the HTTP client's redirect policy.
func validateDownloadURL(rawURL string) error {
	parsed, err := url.Parse(rawURL)
	if err != nil {
		return fmt.Errorf("parse download URL: %w", err)
	}
	if parsed.Scheme != "https" {
		return fmt.Errorf("download URL must use https, got %q", parsed.Scheme)
	}
	if !isAllowedAssetHost(parsed.Hostname()) {
		return fmt.Errorf("download URL host %q is not an approved GitHub asset host", parsed.Hostname())
	}
	return nil
}

func isAllowedAssetHost(host string) bool {
	host = strings.ToLower(host)
	return host == "github.com" ||
		strings.HasSuffix(host, ".githubusercontent.com") ||
		strings.HasSuffix(host, ".github.com")
}

// NewDownloadHTTPClient returns an http.Client suitable for production use:
// bounded timeout and a redirect policy that only follows redirects to
// GitHub-controlled hosts over HTTPS, capped at a small number of hops.
func NewDownloadHTTPClient(timeout time.Duration) *http.Client {
	return &http.Client{
		Timeout: timeout,
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			if len(via) >= 5 {
				return fmt.Errorf("too many redirects for %s", req.URL)
			}
			return validateDownloadURL(req.URL.String())
		},
	}
}
