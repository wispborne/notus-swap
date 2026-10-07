// Package llamaupdate manages llama-swap and llama.cpp releases from GitHub.
// Installs run one at a time, while Status exposes their progress to the UI.
package llamaupdate

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strconv"
	"sync"
	"time"
)

const (
	ComponentLlamaSwap = "llama-swap"
	ComponentLlamaCpp  = "llama.cpp"
)

// ErrBusy means another install is still running.
var ErrBusy = errors.New("another install is still running")

// Manager checks for releases and runs installs. Swap and Cpp are nil when
// their settings are missing from the env file.
type Manager struct {
	GitHub *GitHub
	Swap   *LlamaSwap
	Cpp    *LlamaCpp
	Log    *slog.Logger

	mu        sync.Mutex
	checkedAt time.Time
	swap      swapCheck
	cpp       cppCheck
	job       *Job

	changelogs map[string]changelogCache
}

type swapCheck struct {
	latest *Release
	err    string
}

type cppCheck struct {
	release *Release // the newest weekly release, such as v0.5.0
	build   string   // the nightly build that release was made from
	nightly *Release // the newest nightly build
	err     string
}

// Job tracks an install or rollback and its progress.
type Job struct {
	Component string     `json:"component"`
	Action    string     `json:"action"` // "install" or "rollback"
	Target    string     `json:"target,omitempty"`
	Step      string     `json:"step"`
	Done      int64      `json:"done"`  // bytes downloaded so far
	Total     int64      `json:"total"` // size of the download, 0 if none
	Running   bool       `json:"running"`
	Result    string     `json:"result,omitempty"`
	Error     string     `json:"error,omitempty"`
	Started   time.Time  `json:"started"`
	Finished  *time.Time `json:"finished,omitempty"`
}

// report receives install progress.
type report interface {
	Step(text string)
	Bytes(done, total int64)
}

// Watch checks GitHub now, and then every interval.
func (m *Manager) Watch(ctx context.Context, every time.Duration) {
	if m.Swap == nil && m.Cpp == nil {
		return
	}
	for {
		m.Check(ctx)
		select {
		case <-ctx.Done():
			return
		case <-time.After(every):
		}
	}
}

// Check asks GitHub for the newest releases.
func (m *Manager) Check(ctx context.Context) {
	var sc swapCheck
	var cc cppCheck
	if m.Swap != nil {
		if rel, err := m.GitHub.Latest(ctx, swapRepo); err != nil {
			sc.err = err.Error()
		} else {
			sc.latest = rel
		}
	}
	if m.Cpp != nil {
		var err error
		if cc.release, cc.build, err = m.cppRelease(ctx); err != nil {
			cc.err = err.Error()
		}
		if cc.nightly, err = m.cppNightly(ctx); err != nil && cc.err == "" {
			cc.err = err.Error()
		}
	}
	if m.Log != nil && (sc.err != "" || cc.err != "") {
		m.Log.Warn("checking for llama-swap and llama.cpp releases", "llama-swap", sc.err, "llama.cpp", cc.err)
	}
	m.mu.Lock()
	m.swap, m.cpp, m.checkedAt = sc, cc, time.Now()
	m.mu.Unlock()
}

// cppRelease finds llama.cpp's newest weekly release. Such a release has no
// downloads of its own. It holds one file, nightly-tag.txt, naming the
// nightly build it was made from.
func (m *Manager) cppRelease(ctx context.Context) (*Release, string, error) {
	rel, err := m.GitHub.Latest(ctx, cppRepo)
	if err != nil {
		return nil, "", err
	}
	if cppBuild.MatchString(rel.Tag) {
		return rel, rel.Tag, nil
	}
	a, ok := rel.asset("nightly-tag.txt")
	if !ok {
		return rel, "", fmt.Errorf("llama.cpp %s has no nightly-tag.txt naming its build", rel.Tag)
	}
	build, err := m.GitHub.Text(ctx, a)
	if err != nil {
		return rel, "", err
	}
	if !cppBuild.MatchString(build) {
		return rel, "", fmt.Errorf("llama.cpp %s names build %q, which doesn't look like b11146", rel.Tag, build)
	}
	return rel, build, nil
}

// cppNightly finds the newest nightly build. GitHub doesn't list releases
// in build order, so the highest number among the recent ones wins.
func (m *Manager) cppNightly(ctx context.Context) (*Release, error) {
	rs, err := m.GitHub.Recent(ctx, cppRepo, 10)
	if err != nil {
		return nil, err
	}
	var best *Release
	for i := range rs {
		if n := buildNumber(rs[i].Tag); n > 0 && (best == nil || n > buildNumber(best.Tag)) {
			best = &rs[i]
		}
	}
	if best != nil {
		// Nightly notes are raw commit messages; the page links to them instead.
		best.Notes = ""
	}
	return best, nil
}

type Status struct {
	LlamaSwap SwapStatus `json:"llama_swap"`
	LlamaCpp  CppStatus  `json:"llama_cpp"`
	CheckedAt *time.Time `json:"checked_at,omitempty"`
	Job       *Job       `json:"job,omitempty"`
}

type SwapStatus struct {
	Off        string   `json:"off,omitempty"` // why updates are off
	Bin        string   `json:"bin,omitempty"`
	Installed  string   `json:"installed"`
	HasBackup  bool     `json:"has_backup"`
	Backup     string   `json:"backup,omitempty"` // version of <bin>.bak
	Latest     *Release `json:"latest,omitempty"`
	Newer      bool     `json:"newer"` // Latest is newer than Installed
	CheckError string   `json:"check_error,omitempty"`
}

type CppStatus struct {
	Off          string   `json:"off,omitempty"`
	Dir          string   `json:"dir,omitempty"`
	Flavor       string   `json:"flavor,omitempty"`
	Installed    string   `json:"installed"`
	Previous     string   `json:"previous,omitempty"` // what a roll back switches to
	Release      *Release `json:"release,omitempty"`
	ReleaseBuild string   `json:"release_build,omitempty"`
	Nightly      *Release `json:"nightly,omitempty"`
	Newer        bool     `json:"newer"` // ReleaseBuild is newer than Installed
	Problem      string   `json:"problem,omitempty"`
	CheckError   string   `json:"check_error,omitempty"`
}

// Status reads what is installed now, and adds the last check's results.
func (m *Manager) Status() Status {
	m.mu.Lock()
	sc, cc, checked := m.swap, m.cpp, m.checkedAt
	var job *Job
	if m.job != nil {
		j := *m.job
		job = &j
	}
	m.mu.Unlock()

	s := Status{Job: job}
	if !checked.IsZero() {
		s.CheckedAt = &checked
	}
	if m.Swap == nil {
		s.LlamaSwap.Off = "Set NOTUS_LLAMA_SWAP_BIN or NOTUS_LLAMA_SWAP_CONFIG in notus-swap's env file to update llama-swap from here."
	} else {
		ss := &s.LlamaSwap
		ss.Bin, ss.Installed, ss.HasBackup = m.Swap.Bin, m.Swap.Installed(), m.Swap.HasBackup()
		if ss.HasBackup {
			ss.Backup = m.Swap.Backup()
		}
		ss.Latest, ss.CheckError = sc.latest, sc.err
		ss.Newer = sc.latest != nil && ss.Installed != "" && swapNumber(sc.latest.Tag) > swapNumber(ss.Installed)
	}
	if m.Cpp == nil {
		s.LlamaCpp.Off = "Set NOTUS_LLAMA_CPP_DIR in notus-swap's env file to update llama.cpp from here."
	} else {
		cs := &s.LlamaCpp
		cs.Dir = m.Cpp.Dir
		var err error
		if cs.Installed, _, err = m.Cpp.Current(); err != nil {
			cs.Problem = err.Error()
		} else if cs.Flavor, err = m.Cpp.flavor(); err != nil {
			cs.Problem = err.Error()
		}
		cs.Previous = m.Cpp.Previous()
		cs.Release, cs.ReleaseBuild, cs.Nightly, cs.CheckError = cc.release, cc.build, cc.nightly, cc.err
		cs.Newer = cs.Installed != "" && buildNumber(cc.build) > buildNumber(cs.Installed)
	}
	return s
}

// UpdateAvailable reports whether the last check found a llama-swap release
// or a weekly llama.cpp release newer than the one installed. Nightly builds
// don't count: there are several a day.
func (m *Manager) UpdateAvailable() bool {
	s := m.Status()
	return s.LlamaSwap.Newer || s.LlamaCpp.Newer
}

// swapNumber turns "v258" into 258, and anything else into 0.
func swapNumber(tag string) int {
	if !swapTag.MatchString(tag) {
		return 0
	}
	n, _ := strconv.Atoi(tag[1:])
	return n
}

// Start begins an install or roll back in the background. action is
// "install" or "rollback". For an install, an empty target means the
// newest release from the last check.
func (m *Manager) Start(component, action, target string) error {
	var run func(context.Context, report) (string, error)
	switch {
	case component == ComponentLlamaSwap && m.Swap != nil:
		if action == "install" && target == "" {
			if s := m.Status().LlamaSwap; s.Latest != nil {
				target = s.Latest.Tag
			}
		}
		run = func(ctx context.Context, r report) (string, error) {
			if action == "rollback" {
				return m.Swap.Rollback(ctx, r)
			}
			return m.Swap.Install(ctx, m.GitHub, target, r)
		}
	case component == ComponentLlamaCpp && m.Cpp != nil:
		if action == "install" && target == "" {
			target = m.Status().LlamaCpp.ReleaseBuild
		}
		run = func(ctx context.Context, r report) (string, error) {
			if action == "rollback" {
				return m.Cpp.Rollback(ctx, r)
			}
			return m.Cpp.Install(ctx, m.GitHub, target, r)
		}
	case component == ComponentLlamaSwap || component == ComponentLlamaCpp:
		return fmt.Errorf("%s updates are off", component)
	default:
		return fmt.Errorf("unknown component %q", component)
	}
	if action != "install" && action != "rollback" {
		return fmt.Errorf("unknown action %q", action)
	}
	if action == "install" && target == "" {
		return errors.New("no release to install yet: check for updates first")
	}

	m.mu.Lock()
	if m.job != nil && m.job.Running {
		m.mu.Unlock()
		return ErrBusy
	}
	job := &Job{Component: component, Action: action, Target: target, Step: "Starting", Running: true, Started: time.Now()}
	m.job = job
	m.mu.Unlock()

	go func() {
		// Not tied to the web request, which ends long before a download does.
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Minute)
		defer cancel()
		result, err := run(ctx, jobReport{m, job})
		m.mu.Lock()
		now := time.Now()
		job.Running, job.Finished, job.Result, job.Step = false, &now, result, ""
		if err != nil {
			job.Error = err.Error()
		}
		m.mu.Unlock()
		if m.Log != nil {
			if err != nil {
				m.Log.Error(component+" "+action+" failed", "target", target, "err", err)
			} else {
				m.Log.Info(result)
			}
		}
	}()
	return nil
}

// jobReport writes a job's progress where Status can read it.
type jobReport struct {
	m   *Manager
	job *Job
}

func (r jobReport) Step(text string) {
	r.m.mu.Lock()
	r.job.Step, r.job.Done, r.job.Total = text, 0, 0
	r.m.mu.Unlock()
	if r.m.Log != nil {
		r.m.Log.Info(r.job.Component + ": " + text)
	}
}

func (r jobReport) Bytes(done, total int64) {
	r.m.mu.Lock()
	r.job.Done, r.job.Total = done, total
	r.m.mu.Unlock()
}
