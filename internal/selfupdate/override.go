package selfupdate

import (
	"errors"
	"net/url"
	"strings"
	"time"
)

// Override is an update source saved on the Settings page. It replaces the
// GITEA_ and NOTUS_GITHUB_REPO settings from the environment.
type Override struct {
	// Kind is "" (use the environment), "gitea", "github" or "off".
	Kind       string `json:"kind"`
	GiteaURL   string `json:"gitea_url,omitempty"`
	GiteaRepo  string `json:"gitea_repo,omitempty"`
	GiteaUser  string `json:"gitea_user,omitempty"`
	GiteaToken string `json:"gitea_token,omitempty"`
	GitHubRepo string `json:"github_repo,omitempty"`
}

// Clean trims the fields and drops the ones Kind doesn't use, so a token
// isn't kept after switching away from Gitea.
func (o Override) Clean() Override {
	t := strings.TrimSpace
	switch o.Kind {
	case "gitea":
		return Override{Kind: o.Kind, GiteaURL: strings.TrimRight(t(o.GiteaURL), "/"), GiteaRepo: t(o.GiteaRepo), GiteaUser: t(o.GiteaUser), GiteaToken: t(o.GiteaToken)}
	case "github":
		return Override{Kind: o.Kind, GitHubRepo: t(o.GitHubRepo)}
	case "off":
		return Override{Kind: o.Kind}
	}
	return Override{}
}

// Check returns what is missing or wrong, in words for the Settings page.
func (o Override) Check() error {
	switch o.Kind {
	case "", "off":
		return nil
	case "gitea":
		u, err := url.Parse(o.GiteaURL)
		if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
			return errors.New("the Gitea address must start with http:// or https://")
		}
		if !isRepo(o.GiteaRepo) {
			return errors.New("the Gitea repository must be written as owner/name")
		}
		if o.GiteaUser == "" || o.GiteaToken == "" {
			return errors.New("Gitea needs a user name and a token")
		}
	case "github":
		if !isRepo(o.GitHubRepo) {
			return errors.New("the GitHub repository must be written as owner/name")
		}
	default:
		return errors.New("unknown update source " + o.Kind)
	}
	return nil
}

func isRepo(s string) bool {
	owner, name, ok := strings.Cut(s, "/")
	return ok && owner != "" && name != "" && !strings.ContainsAny(name, "/ ") && !strings.Contains(owner, " ")
}

// SetOverride replaces the update source. The zero Override goes back to the
// environment's settings. The cached release list is dropped, since it came
// from the old source.
func (u *Updater) SetOverride(o Override) {
	u.mu.Lock()
	defer u.mu.Unlock()
	u.override = o
	u.gen++
	u.cache, u.cachedAt = nil, time.Time{}
}

// Override returns the saved update source.
func (u *Updater) Override() Override {
	u.mu.Lock()
	defer u.mu.Unlock()
	return u.override
}

// EnvSource names the source the environment's settings give, as Source
// does, whatever the override is.
func (u *Updater) EnvSource() string {
	return u.envConn().name()
}
