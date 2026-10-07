package llamaupdate

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"strconv"
	"strings"
	"time"
)

// GitHub reads public releases through GitHub's API.
type GitHub struct {
	// API is GitHub's API address. Empty means https://api.github.com.
	// Tests point it at a fake server.
	API string
	// Token is optional. Without one, GitHub allows 60 API calls an hour
	// from each IP address. With one, it allows 5000.
	Token  string
	Client *http.Client
}

// Release is one GitHub release.
type Release struct {
	Tag        string    `json:"tag"`
	Notes      string    `json:"notes"`
	URL        string    `json:"url"` // the release's page on GitHub
	Published  time.Time `json:"published"`
	Prerelease bool      `json:"-"`
	Assets     []Asset   `json:"-"`
}

// Asset is one file attached to a release.
type Asset struct {
	Name   string
	Size   int64
	URL    string
	SHA256 string // hex, or "" when GitHub gave none
}

func (r *Release) asset(name string) (Asset, bool) {
	for _, a := range r.Assets {
		if a.Name == name {
			return a, true
		}
	}
	return Asset{}, false
}

type apiRelease struct {
	TagName     string    `json:"tag_name"`
	Body        string    `json:"body"`
	HTMLURL     string    `json:"html_url"`
	PublishedAt time.Time `json:"published_at"`
	Draft       bool      `json:"draft"`
	Prerelease  bool      `json:"prerelease"`
	Assets      []struct {
		Name   string `json:"name"`
		Size   int64  `json:"size"`
		URL    string `json:"browser_download_url"`
		Digest string `json:"digest"` // "sha256:<hex>"
	} `json:"assets"`
}

func (r apiRelease) release() Release {
	out := Release{Tag: r.TagName, Notes: strings.TrimSpace(r.Body), URL: r.HTMLURL, Published: r.PublishedAt, Prerelease: r.Prerelease}
	for _, a := range r.Assets {
		out.Assets = append(out.Assets, Asset{Name: a.Name, Size: a.Size, URL: a.URL, SHA256: strings.TrimPrefix(a.Digest, "sha256:")})
	}
	return out
}

// Latest returns the release GitHub marks as the latest. Pre-releases are
// never marked latest.
func (g *GitHub) Latest(ctx context.Context, repo string) (*Release, error) {
	var r apiRelease
	if err := g.getJSON(ctx, "/repos/"+repo+"/releases/latest", &r); err != nil {
		return nil, err
	}
	rel := r.release()
	return &rel, nil
}

// Tag returns the release with this tag.
func (g *GitHub) Tag(ctx context.Context, repo, tag string) (*Release, error) {
	var r apiRelease
	if err := g.getJSON(ctx, "/repos/"+repo+"/releases/tags/"+url.PathEscape(tag), &r); err != nil {
		return nil, err
	}
	rel := r.release()
	return &rel, nil
}

// Recent returns the n newest releases, pre-releases included, without
// drafts.
func (g *GitHub) Recent(ctx context.Context, repo string, n int) ([]Release, error) {
	var rs []apiRelease
	if err := g.getJSON(ctx, "/repos/"+repo+"/releases?per_page="+strconv.Itoa(n), &rs); err != nil {
		return nil, err
	}
	out := []Release{}
	for _, r := range rs {
		if !r.Draft {
			out = append(out, r.release())
		}
	}
	return out, nil
}

func (g *GitHub) client() *http.Client {
	if g.Client != nil {
		return g.Client
	}
	return http.DefaultClient
}

func (g *GitHub) getJSON(ctx context.Context, path string, v any) error {
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	api := g.API
	if api == "" {
		api = "https://api.github.com"
	}
	req, err := http.NewRequestWithContext(ctx, "GET", api+path, nil)
	if err != nil {
		return err
	}
	req.Header.Set("Accept", "application/vnd.github+json")
	if g.Token != "" {
		req.Header.Set("Authorization", "Bearer "+g.Token)
	}
	resp, err := g.client().Do(req)
	if err != nil {
		return fmt.Errorf("could not reach GitHub: %w", err)
	}
	defer resp.Body.Close()
	if (resp.StatusCode == http.StatusForbidden || resp.StatusCode == http.StatusTooManyRequests) &&
		resp.Header.Get("X-RateLimit-Remaining") == "0" {
		msg := "GitHub's hourly limit of API calls from this address is used up"
		if reset, err := strconv.ParseInt(resp.Header.Get("X-RateLimit-Reset"), 10, 64); err == nil {
			msg += " until " + time.Unix(reset, 0).Format("15:04")
		}
		if g.Token == "" {
			msg += ". GITHUB_TOKEN in the env file raises the limit"
		}
		return errors.New(msg)
	}
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("GitHub answered %s for %s", resp.Status, path)
	}
	return json.NewDecoder(resp.Body).Decode(v)
}

// Text downloads a small text file, such as llama.cpp's nightly-tag.txt.
func (g *GitHub) Text(ctx context.Context, a Asset) (string, error) {
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	resp, err := g.fetch(ctx, a)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	b, err := io.ReadAll(io.LimitReader(resp.Body, 4096))
	return strings.TrimSpace(string(b)), err
}

// Download saves a to path, and checks its SHA-256 when GitHub published
// one. progress, if set, is called with the number of bytes saved so far.
// On failure, path is removed.
func (g *GitHub) Download(ctx context.Context, a Asset, path string, progress func(done int64)) (err error) {
	resp, err := g.fetch(ctx, a)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	f, err := os.Create(path)
	if err != nil {
		return err
	}
	defer func() {
		if err != nil {
			os.Remove(path)
		}
	}()
	h := sha256.New()
	_, err = io.Copy(io.MultiWriter(f, h, &counter{report: progress}), resp.Body)
	if cerr := f.Close(); err == nil {
		err = cerr
	}
	if err != nil {
		return fmt.Errorf("downloading %s: %w", a.Name, err)
	}
	if got := hex.EncodeToString(h.Sum(nil)); a.SHA256 != "" && got != strings.ToLower(a.SHA256) {
		return fmt.Errorf("%s failed its checksum: got %s, expected %s", a.Name, got, a.SHA256)
	}
	return nil
}

// fetch starts downloading a release file. Release files are public, so no
// token is sent.
func (g *GitHub) fetch(ctx context.Context, a Asset) (*http.Response, error) {
	req, err := http.NewRequestWithContext(ctx, "GET", a.URL, nil)
	if err != nil {
		return nil, err
	}
	resp, err := g.client().Do(req)
	if err != nil {
		return nil, fmt.Errorf("downloading %s: %w", a.Name, err)
	}
	if resp.StatusCode != http.StatusOK {
		resp.Body.Close()
		return nil, fmt.Errorf("downloading %s: GitHub answered %s", a.Name, resp.Status)
	}
	return resp, nil
}

// counter counts bytes written through it.
type counter struct {
	n      int64
	report func(int64)
}

func (c *counter) Write(b []byte) (int, error) {
	c.n += int64(len(b))
	if c.report != nil {
		c.report(c.n)
	}
	return len(b), nil
}
