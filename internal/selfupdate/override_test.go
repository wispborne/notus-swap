package selfupdate

import (
	"context"
	"testing"
)

func TestOverride(t *testing.T) {
	u := &Updater{GitHubRepo: "wispborne/notus-swap", GitHubToken: "gh"}
	if got := u.Source(); got != "GitHub (wispborne/notus-swap)" {
		t.Fatalf("from the environment: %q", got)
	}
	u.SetOverride(Override{Kind: "gitea", GiteaURL: "https://git.example.com", GiteaRepo: "o/r", GiteaUser: "o", GiteaToken: "t"})
	c := u.conn()
	if u.Source() != "Gitea (o/r)" || c.githubToken != "" {
		t.Errorf("gitea override: %q, GitHub token %q", u.Source(), c.githubToken)
	}
	if got := u.downloadURL(c, "v1", "f"); got != "https://git.example.com/o/r/releases/download/v1/f" {
		t.Errorf("download address %q", got)
	}
	u.SetOverride(Override{Kind: "github", GitHubRepo: "me/fork"})
	if u.Source() != "GitHub (me/fork)" || u.conn().githubToken != "gh" {
		t.Errorf("github override: %q keeps the environment's token: %v", u.Source(), u.conn().githubToken == "gh")
	}
	u.SetOverride(Override{Kind: "off"})
	if u.Configured() {
		t.Error("off should turn updates off")
	}
	if _, err := u.Releases(context.Background()); err == nil {
		t.Error("releases while off should fail")
	}
	u.SetOverride(Override{})
	if u.Source() != u.EnvSource() || u.EnvSource() != "GitHub (wispborne/notus-swap)" {
		t.Errorf("back to the environment: %q", u.Source())
	}
}

func TestOverrideCleanAndCheck(t *testing.T) {
	o := Override{Kind: "github", GitHubRepo: " o/r ", GiteaToken: "left over"}.Clean()
	if o.GiteaToken != "" || o.GitHubRepo != "o/r" || o.Check() != nil {
		t.Errorf("clean github: %+v", o)
	}
	if (Override{Kind: "weird", GiteaToken: "t"}).Clean() != (Override{}) {
		t.Error("an unknown kind should clean to the environment")
	}
	for _, bad := range []Override{
		{Kind: "github", GitHubRepo: "no-slash"},
		{Kind: "gitea", GiteaURL: "git.example.com", GiteaRepo: "o/r", GiteaUser: "o", GiteaToken: "t"},
		{Kind: "gitea", GiteaURL: "https://git.example.com", GiteaRepo: "o/r", GiteaUser: "o"},
		{Kind: "gitea", GiteaURL: "https://git.example.com", GiteaRepo: "o/r/x", GiteaUser: "o", GiteaToken: "t"},
	} {
		if bad.Check() == nil {
			t.Errorf("%+v passed the check", bad)
		}
	}
}
