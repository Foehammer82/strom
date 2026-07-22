package updates

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestGitHubClientLatestRelease(t *testing.T) {
	tests := []struct {
		name    string
		body    string
		status  int
		wantErr bool
		wantTag string
	}{
		{
			name:    "stable release",
			body:    `{"tag_name":"v1.2.3","draft":false,"prerelease":false,"html_url":"https://github.com/x/y/releases/v1.2.3","assets":[]}`,
			status:  http.StatusOK,
			wantTag: "v1.2.3",
		},
		{
			name:    "draft rejected",
			body:    `{"tag_name":"v1.2.3","draft":true,"prerelease":false}`,
			status:  http.StatusOK,
			wantErr: true,
		},
		{
			name:    "prerelease rejected",
			body:    `{"tag_name":"v1.2.3-rc1","draft":false,"prerelease":true}`,
			status:  http.StatusOK,
			wantErr: true,
		},
		{
			name:    "non-stable tag rejected",
			body:    `{"tag_name":"v1.2.3-rc1","draft":false,"prerelease":false}`,
			status:  http.StatusOK,
			wantErr: true,
		},
		{
			name:    "server error",
			body:    `{}`,
			status:  http.StatusInternalServerError,
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if !strings.HasSuffix(r.URL.Path, "/releases/latest") {
					t.Fatalf("unexpected path %s", r.URL.Path)
				}
				w.WriteHeader(tt.status)
				_, _ = w.Write([]byte(tt.body))
			}))
			defer server.Close()

			client := &GitHubClient{BaseURL: server.URL, Repository: "owner/repo"}
			release, err := client.LatestRelease(context.Background())
			if (err != nil) != tt.wantErr {
				t.Fatalf("LatestRelease() error = %v, wantErr %v", err, tt.wantErr)
			}
			if err == nil && release.TagName != tt.wantTag {
				t.Fatalf("TagName = %q, want %q", release.TagName, tt.wantTag)
			}
		})
	}
}

func TestGitHubClientDownloadAssetSizeLimit(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write(make([]byte, 1024))
	}))
	defer server.Close()

	client := &GitHubClient{}
	_, err := client.DownloadAsset(context.Background(), server.URL+"/asset.tar.gz")
	if err == nil {
		t.Fatal("expected error for non-github download host")
	}
}

func TestValidateDownloadURL(t *testing.T) {
	tests := []struct {
		name    string
		url     string
		wantErr bool
	}{
		{name: "github.com https", url: "https://github.com/owner/repo/releases/download/v1/asset.tar.gz"},
		{name: "objects.githubusercontent.com https", url: "https://objects.githubusercontent.com/abc"},
		{name: "http rejected", url: "http://github.com/owner/repo", wantErr: true},
		{name: "unrelated host rejected", url: "https://evil.example.com/asset.tar.gz", wantErr: true},
		{name: "malformed url", url: "://not-a-url", wantErr: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := validateDownloadURL(tt.url)
			if (err != nil) != tt.wantErr {
				t.Fatalf("validateDownloadURL(%q) error = %v, wantErr %v", tt.url, err, tt.wantErr)
			}
		})
	}
}

func TestGitHubClientAssetByName(t *testing.T) {
	release := Release{Assets: []releaseAsset{
		{Name: "strom-agent-manifest.json", BrowserDownloadURL: "https://github.com/x/manifest.json"},
	}}
	if _, ok := release.AssetByName("strom-agent-manifest.json"); !ok {
		t.Fatal("expected exact name match to be found")
	}
	if _, ok := release.AssetByName("strom-agent-manifest.json.sig"); ok {
		t.Fatal("expected no match for a different asset name")
	}
}

func TestGitHubClientRepositoryAndBaseURLDefaults(t *testing.T) {
	client := &GitHubClient{}
	if client.repository() != defaultRepository {
		t.Fatalf("repository() = %q, want %q", client.repository(), defaultRepository)
	}
	if client.baseURL() != defaultGitHubAPIBaseURL {
		t.Fatalf("baseURL() = %q, want %q", client.baseURL(), defaultGitHubAPIBaseURL)
	}
	if got, want := client.userAgent(), fmt.Sprintf("%s/dev", userAgentPrefix); got != want {
		t.Fatalf("userAgent() = %q, want %q", got, want)
	}
}
