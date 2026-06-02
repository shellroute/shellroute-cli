package cli

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/shellroute/shellroute-cli/internal/api"
	"github.com/shellroute/shellroute-cli/internal/config"
	"github.com/shellroute/shellroute-cli/internal/display"
)

const versionCacheTTL = 30 * time.Minute

type versionCache struct {
	Latest       string `json:"latest"`
	MinRequired  string `json:"min_required"`
	ChangelogURL string `json:"changelog_url"`
	CheckedAt    int64  `json:"checked_at"`
}

// checkVersion checks if the CLI version is up to date.
// Returns true if the CLI should proceed, false if blocked (version too old).
func checkVersion(cfg *config.Config) bool {
	v := Version()
	if flagSkipVersionCheck || v == "dev" {
		return true
	}

	info, err := getCachedOrFetchVersion(cfg)
	if err != nil {
		return true
	}

	if info.MinRequired != "" && compareVersions(v, info.MinRequired) < 0 {
		display.Error("This version (%s) is no longer supported. Minimum required: %s", v, info.MinRequired)
		fmt.Fprintf(os.Stderr, "  Update: %s\n", UpdateCommand())
		if info.ChangelogURL != "" {
			fmt.Fprintf(os.Stderr, "  Changelog: %s\n", info.ChangelogURL)
		}
		return false
	}

	if info.Latest != "" && compareVersions(v, info.Latest) < 0 {
		display.Warn("Update available: %s (you have %s). Run: %s", info.Latest, v, UpdateCommand())
	}

	return true
}

func getCachedOrFetchVersion(cfg *config.Config) (*versionCache, error) {
	cacheFile := versionCacheFile()

	if data, err := os.ReadFile(cacheFile); err == nil {
		var cached versionCache
		if json.Unmarshal(data, &cached) == nil {
			if time.Since(time.Unix(cached.CheckedAt, 0)) < versionCacheTTL {
				return &cached, nil
			}
		}
	}

	client := api.New(cfg.APIURL, "")
	resp, err := client.GetVersion()
	if err != nil {
		return nil, err
	}

	cached := &versionCache{
		Latest:       resp.Latest,
		MinRequired:  resp.MinRequired,
		ChangelogURL: resp.ChangelogURL,
		CheckedAt:    time.Now().Unix(),
	}

	if data, err := json.Marshal(cached); err == nil {
		os.WriteFile(cacheFile, data, 0600)
	}

	return cached, nil
}

func versionCacheFile() string {
	dir, err := config.Dir()
	if err != nil {
		return filepath.Join(os.TempDir(), "shellroute-version-cache.json")
	}
	return filepath.Join(dir, "version-check.json")
}
