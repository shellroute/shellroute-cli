package config

import (
	"fmt"
	"regexp"
)

// Upstream is a saved proxy of the user's own, stored in config.toml as a
// [[upstream]] table. URL carries the credentials; the file is mode 0600.
type Upstream struct {
	Name    string `toml:"name"`
	URL     string `toml:"url"`
	Country string `toml:"country,omitempty"` // display label only; not verified
	Note    string `toml:"note,omitempty"`
}

// Upstream names are 3 to 32 characters of lowercase letters, digits,
// underscore or hyphen, so a name can never look like a two-letter country.
var upstreamNameRe = regexp.MustCompile(`^[a-z0-9_-]{3,32}$`)

// ValidateUpstreamName checks a saved upstream name.
func ValidateUpstreamName(name string) error {
	if len(name) == 2 {
		return fmt.Errorf("upstream name %q looks like a country code; use 3 to 32 characters", name)
	}
	if !upstreamNameRe.MatchString(name) {
		return fmt.Errorf("upstream name must be 3 to 32 characters: lowercase letters, digits, _ or -")
	}
	return nil
}

// FindUpstream returns the saved upstream with the given name.
func (c *Config) FindUpstream(name string) (*Upstream, bool) {
	for i := range c.Upstreams {
		if c.Upstreams[i].Name == name {
			return &c.Upstreams[i], true
		}
	}
	return nil, false
}

// AddUpstream saves an upstream, replacing one with the same name.
// Returns true when an existing entry was replaced.
func (c *Config) AddUpstream(u Upstream) bool {
	for i := range c.Upstreams {
		if c.Upstreams[i].Name == u.Name {
			c.Upstreams[i] = u
			return true
		}
	}
	c.Upstreams = append(c.Upstreams, u)
	return false
}

// RemoveUpstream deletes a saved upstream. Returns false when it did not exist.
func (c *Config) RemoveUpstream(name string) bool {
	for i := range c.Upstreams {
		if c.Upstreams[i].Name == name {
			c.Upstreams = append(c.Upstreams[:i], c.Upstreams[i+1:]...)
			return true
		}
	}
	return false
}
