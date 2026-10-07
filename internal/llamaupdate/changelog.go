package llamaupdate

import (
	"context"
	"fmt"
	"strings"
	"time"
)

// changelogTTL is how long a changelog is kept before GitHub is asked again.
// It keeps the Changelog button from using up GitHub's hourly limit.
const changelogTTL = 15 * time.Minute

type changelogCache struct {
	releases []Release
	at       time.Time
}

// Changelog returns the component's recent releases, newest first, with
// their notes. For llama.cpp, a nightly build's notes are only the title of
// the change it was built from.
func (m *Manager) Changelog(ctx context.Context, component string) ([]Release, error) {
	var repo string
	var n int
	switch {
	case component == ComponentLlamaSwap && m.Swap != nil:
		repo, n = swapRepo, 15
	case component == ComponentLlamaCpp && m.Cpp != nil:
		repo, n = cppRepo, 40
	case component == ComponentLlamaSwap || component == ComponentLlamaCpp:
		return nil, fmt.Errorf("%s updates are off", component)
	default:
		return nil, fmt.Errorf("unknown component %q", component)
	}

	m.mu.Lock()
	if c, ok := m.changelogs[component]; ok && time.Since(c.at) < changelogTTL {
		m.mu.Unlock()
		return c.releases, nil
	}
	m.mu.Unlock()

	rs, err := m.GitHub.Recent(ctx, repo, n)
	if err != nil {
		return nil, err
	}
	out := []Release{}
	for _, r := range rs {
		if component == ComponentLlamaSwap && (r.Prerelease || !swapTag.MatchString(r.Tag)) {
			continue
		}
		if cppBuild.MatchString(r.Tag) {
			r.Notes = buildTitle(r.Notes)
		}
		out = append(out, r)
	}
	m.mu.Lock()
	if m.changelogs == nil {
		m.changelogs = map[string]changelogCache{}
	}
	m.changelogs[component] = changelogCache{out, time.Now()}
	m.mu.Unlock()
	return out, nil
}

// buildTitle picks the change's title out of a llama.cpp nightly build's
// notes. The notes hold the commit message inside "<details open>", then
// links to every download.
func buildTitle(notes string) string {
	body, ok := strings.CutPrefix(strings.TrimSpace(notes), "<details open>")
	if !ok {
		return ""
	}
	body, _, _ = strings.Cut(body, "</details>")
	title, _, _ := strings.Cut(strings.TrimSpace(body), "\n")
	return strings.TrimSpace(title)
}
