// notus-swap sits in front of llama-swap, records inference traffic, and
// serves its own web UI under /notus/.
package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"os"
	"os/signal"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/wispborne/notus-swap/internal/api"
	"github.com/wispborne/notus-swap/internal/autoload"
	"github.com/wispborne/notus-swap/internal/capture"
	"github.com/wispborne/notus-swap/internal/gpu"
	"github.com/wispborne/notus-swap/internal/idlewait"
	"github.com/wispborne/notus-swap/internal/llamaconfig"
	"github.com/wispborne/notus-swap/internal/llamaswap"
	"github.com/wispborne/notus-swap/internal/llamaupdate"
	"github.com/wispborne/notus-swap/internal/logbuf"
	"github.com/wispborne/notus-swap/internal/metrics"
	"github.com/wispborne/notus-swap/internal/peers"
	"github.com/wispborne/notus-swap/internal/proxy"
	"github.com/wispborne/notus-swap/internal/rapl"
	"github.com/wispborne/notus-swap/internal/restart"
	"github.com/wispborne/notus-swap/internal/selfupdate"
	"github.com/wispborne/notus-swap/internal/service"
	"github.com/wispborne/notus-swap/internal/store"
	"github.com/wispborne/notus-swap/web"
)

// Set at build time with -ldflags "-X main.version=...".
var version = "dev"

func main() {
	listen := flag.String("listen", env("NOTUS_LISTEN", ":8080"), "address to listen on")
	upstream := flag.String("upstream", env("NOTUS_UPSTREAM", "http://127.0.0.1:8081"), "llama-swap base URL")
	dbPath := flag.String("db", env("NOTUS_DB", "notus-swap.db"), "SQLite database file")
	keepDays := flag.Int("keep-days", 90, "keep request and response text for this many days (Settings page can override)")
	keepGB := flag.Int64("keep-gb", 20, "maximum GB of request and response text; delete oldest first (Settings page can override)")
	unit := flag.String("llama-swap-unit", env("NOTUS_LLAMA_SWAP_UNIT", "llama-swap.service"), "llama-swap's systemd unit, for the restart button")
	svcKind := flag.String("llama-swap-service", env("NOTUS_LLAMA_SWAP_SERVICE", defaultService()), "how llama-swap is run: systemd (its unit is -llama-swap-unit), process (notus-swap runs it), or none")
	swapArgs := flag.String("llama-swap-args", env("NOTUS_LLAMA_SWAP_ARGS", "-watch-config"), "with -llama-swap-service process: more arguments for llama-swap, after -config and -listen")
	cfgPath := flag.String("llama-swap-config", env("NOTUS_LLAMA_SWAP_CONFIG", ""), "llama-swap's config.yaml, for the config editor (empty turns it off)")
	cfgBin := flag.String("llama-swap-bin", env("NOTUS_LLAMA_SWAP_BIN", ""), "llama-swap binary, for checking configs and for updates (default: next to the config)")
	cppDir := flag.String("llama-cpp-dir", env("NOTUS_LLAMA_CPP_DIR", ""), "folder holding llama.cpp builds and the \"current\" link, for updates (empty turns them off)")
	cppFlavor := flag.String("llama-cpp-flavor", env("NOTUS_LLAMA_CPP_FLAVOR", ""), "llama.cpp build to download, such as ubuntu-vulkan-x64 or win-cuda-12.4-x64 (default: the one current points at)")
	drmRoot := flag.String("drm-root", "/sys/class/drm", "GPU sensor directory (use a fake tree for tests)")
	nvidiaSMI := flag.String("nvidia-smi", env("NOTUS_NVIDIA_SMI", "nvidia-smi"), "nvidia-smi program, for reading NVIDIA cards (empty turns it off)")
	powercapRoot := flag.String("powercap-root", "/sys/class/powercap", "where to find the CPU's RAPL energy counters")
	baseWatts := flag.Float64("system-base-watts", envFloat("NOTUS_SYSTEM_BASE_WATTS", 35), "estimated power of everything except the CPU and GPUs (board, RAM, drives, fans)")
	psuEff := flag.Float64("psu-efficiency", envFloat("NOTUS_PSU_EFFICIENCY", 0.90), "power supply efficiency for estimated wall power")
	sqlAPI := flag.Bool("sql-api", envBool("NOTUS_SQL_API"), "answer read-only SQL queries at POST /notus/api/query")
	flag.Parse()

	// Without systemd, a small parent process starts notus-swap again when
	// it restarts, as systemd's Restart=always would.
	if wantSupervisor() {
		os.Exit(supervise(slog.New(slog.NewTextHandler(os.Stderr, nil))))
	}

	// The log goes to stderr (journald under systemd), and its newest 256 KB
	// stay in memory for the Logs page.
	logs := logbuf.New(256 << 10)
	log := slog.New(slog.NewTextHandler(io.MultiWriter(os.Stderr, logs), nil))

	// A freshly installed update counts its starts here, and gives way to the
	// previous binary if it keeps failing before it can confirm itself.
	updater := selfupdate.FromEnv(version)
	updater.RemoveLeftovers()
	if rolledBack, err := updater.CheckOnStart(); err != nil {
		log.Error("checking a pending update", "err", err)
	} else if rolledBack {
		log.Error("new version stopped twice before 30 seconds; restored previous version; restarting")
		os.Exit(restartExitCode())
	} else if m := updater.Pending(); m != nil {
		log.Info("confirming an update", "from", m.From, "to", m.To, "attempt", m.Attempts)
	}

	bin := *cfgBin
	if bin == "" && *cfgPath != "" {
		bin = llamaconfig.DefaultBin(*cfgPath)
	}
	svc, err := llamaSwapService(*svcKind, *unit, bin, *cfgPath, *upstream, *swapArgs, log)
	if err != nil {
		log.Error("exiting", "err", err)
		os.Exit(1)
	}

	// llama-swap and llama.cpp updates each turn on when their paths are set.
	llamaUpdates := &llamaupdate.Manager{GitHub: &llamaupdate.GitHub{Token: os.Getenv("GITHUB_TOKEN")}, Log: log}
	if bin != "" {
		llamaUpdates.Swap = &llamaupdate.LlamaSwap{Bin: bin, Config: *cfgPath, Upstream: *upstream}
		if svc != nil {
			llamaUpdates.Swap.Restart = svc.Restart
		}
	}
	if *cppDir != "" {
		llamaUpdates.Cpp = &llamaupdate.LlamaCpp{Dir: *cppDir, Flavor: *cppFlavor}
	}

	restarting, err := run(log, logs, *listen, *upstream, *dbPath, api.Retention{Days: *keepDays, GB: *keepGB}, svc,
		sensors{*drmRoot, *nvidiaSMI, *powercapRoot, *baseWatts, *psuEff}, &llamaconfig.Editor{Path: *cfgPath, Bin: *cfgBin}, updater, llamaUpdates, *sqlAPI,
		api.FlagSettings(flag.CommandLine, flagEnv, os.Getenv))
	if err != nil {
		log.Error("exiting", "err", err)
		os.Exit(1)
	}
	if restarting {
		os.Exit(restartExitCode())
	}
}

// flagEnv names the env setting each flag reads its default from. Keep it
// the same as the env calls in main.
var flagEnv = map[string]string{
	"listen": "NOTUS_LISTEN", "upstream": "NOTUS_UPSTREAM", "db": "NOTUS_DB",
	"llama-swap-unit": "NOTUS_LLAMA_SWAP_UNIT", "llama-swap-service": "NOTUS_LLAMA_SWAP_SERVICE",
	"llama-swap-args": "NOTUS_LLAMA_SWAP_ARGS", "llama-swap-config": "NOTUS_LLAMA_SWAP_CONFIG",
	"llama-swap-bin": "NOTUS_LLAMA_SWAP_BIN", "llama-cpp-dir": "NOTUS_LLAMA_CPP_DIR",
	"llama-cpp-flavor": "NOTUS_LLAMA_CPP_FLAVOR", "nvidia-smi": "NOTUS_NVIDIA_SMI",
	"system-base-watts": "NOTUS_SYSTEM_BASE_WATTS", "psu-efficiency": "NOTUS_PSU_EFFICIENCY",
}

// defaultService is systemd on Linux. Elsewhere notus-swap runs llama-swap
// itself.
func defaultService() string {
	if runtime.GOOS == "linux" {
		return "systemd"
	}
	return "process"
}

// llamaSwapService makes the controller for -llama-swap-service. It is nil
// for "none".
func llamaSwapService(kind, unit, bin, cfg, upstream, args string, log *slog.Logger) (service.Controller, error) {
	switch kind {
	case "systemd":
		return &service.Systemd{Unit: unit}, nil
	case "process":
		up, err := url.Parse(upstream)
		if err != nil {
			return nil, err
		}
		if up.Port() == "" {
			return nil, fmt.Errorf("the upstream address %s needs a port, so notus-swap knows where to start llama-swap", upstream)
		}
		return &service.Process{
			Bin: bin, Config: cfg, Listen: up.Host, Args: strings.Fields(args), Log: log,
			Stdout: os.Stdout, Stderr: os.Stderr,
		}, nil
	case "none", "":
		return nil, nil
	}
	return nil, fmt.Errorf("-llama-swap-service must be systemd, process or none, not %q", kind)
}

// sensors holds the settings for reading power.
type sensors struct {
	drmRoot, nvidiaSMI, powercapRoot string
	baseWatts, psuEff                float64
}

// run serves until notus-swap is stopped or restarts. restarting reports a
// planned restart, after which notus-swap should be started again.
func run(log *slog.Logger, logs *logbuf.Buffer, listen, upstream, dbPath string, keep api.Retention, svc service.Controller, sn sensors, cfg *llamaconfig.Editor, updater *selfupdate.Updater, llamaUpdates *llamaupdate.Manager, sqlAPI bool, flags []api.Setting) (restarting bool, err error) {
	up, err := url.Parse(upstream)
	if err != nil {
		return false, err
	}
	st, err := store.Open(dbPath)
	if err != nil {
		return false, err
	}
	defer st.Close()
	var ro *store.ReadOnly
	if sqlAPI {
		if ro, err = store.OpenReadOnly(dbPath); err != nil {
			return false, err
		}
		defer ro.Close()
		log.Info("the SQL endpoint is on", "path", "/notus/api/query")
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	// When notus-swap runs llama-swap itself, it starts it now, unless one is
	// already running, such as one left running by the last restart.
	if p, ok := svc.(*service.Process); ok {
		runCtx, endRun := context.WithCancel(ctx)
		defer endRun()
		go p.Run(runCtx)
		// Stopping notus-swap stops llama-swap too. A restart leaves it
		// running, so models stay loaded.
		defer func() {
			if restarting {
				return
			}
			sctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
			defer cancel()
			log.Info("stopping llama-swap")
			if err := p.Stop(sctx); err != nil {
				log.Error("stopping llama-swap", "err", err)
			}
		}()
	}

	if n, err := st.MarkInterrupted(ctx); err != nil {
		return false, err
	} else if n > 0 {
		log.Info("marked requests from the last run as interrupted", "count", n)
	}
	hub := capture.NewHub()
	gpus := gpu.NewSampler(sn.drmRoot)
	gpus.NvidiaSMI = sn.nvidiaSMI
	rec := &metrics.Recorder{
		Store: st, GPUs: gpus, CPU: rapl.NewMeter(sn.powercapRoot),
		InFlight: hub.Works, Log: log, BaseWatts: sn.baseWatts, PSUEff: sn.psuEff,
	}
	go rec.Run(ctx)
	hub.Account = rec.Account
	hub.Overlap = rec.Overlap
	hub.Settle = func() []int64 { return rec.Settle(context.WithoutCancel(ctx)) }
	go func() {
		time.Sleep(3 * time.Second)
		if p := rec.Latest().CPUProblem; p != "" {
			log.Warn("CPU power unavailable; system power estimate disabled", "reason", p)
		}
	}()

	monitor := llamaswap.NewMonitor(up)
	// Read and Load are filled in once the API exists, before Run starts.
	loader := &autoload.Loader{Status: monitor.Status, Log: log}
	monitor.OnTransition = func(t llamaswap.Transition) {
		t.Auto = loader.Observe(t)
		rec.OnTransition(t)
	}
	props := func(model string) *store.ModelProps {
		p, err := st.GetModelProps(ctx, model)
		if err != nil {
			log.Error("reading a model's limits", "model", model, "err", err)
		}
		return p
	}
	hub.Props = props
	cmds := &cmdHashes{m: map[string]string{}}
	hub.Cmd = cmds.get
	slots := &capture.SlotPoller{Hub: hub, Fetch: monitor.FetchSlots, Ready: monitor.Ready, ID: monitor.ID, Log: log}
	go slots.Run(ctx)
	strata := &capture.StrataStats{Store: st, Hub: hub, Fetch: monitor.FetchStrata, Ready: monitor.Ready, ID: monitor.ID, Log: log}
	hub.Strata = strata.Finished
	monitor.OnReady = func(model string) {
		slots.Loaded(model)
		go learnProps(ctx, log, st, monitor, props, model)
		go learnCmd(ctx, log, st, monitor, cmds, model)
	}
	go monitor.Run(ctx, 2*time.Second)

	servers, err := peers.Open(ctx, st, log)
	if err != nil {
		return false, err
	}
	go servers.Run(ctx)

	pruneNow := make(chan struct{}, 1)
	plan := restart.New(hub.Idle)
	a := &api.API{
		Store: st, Hub: hub, LlamaSwap: monitor, Metrics: rec, Config: cfg,
		ConfigHeld: &llamaconfig.Held{Editor: cfg, Wait: &idlewait.Waiter{Idle: hub.Idle}, Log: log}, Upstream: up, Log: log, Version: version,
		Updater: updater, LlamaUpdates: llamaUpdates, Logs: logs, Restart: plan, RetentionDefault: keep, AutoLoad: loader,
		LlamaSwapService: svc, SelfRestart: selfRestart(), ReadOnly: ro, Flags: flags, Peers: servers,
		PruneNow: func() {
			select {
			case pruneNow <- struct{}{}:
			default:
			}
		},
	}
	a.Capture = capture.Handler(st, hub, proxy.New(up, log), log)
	loader.Read = func() autoload.Setting { return a.DefaultModelSetting(ctx) }
	loader.Load = a.LoadModel
	go loader.Run(ctx)
	go prune(ctx, log, st, func() api.Retention { return a.RetentionSetting(ctx, keep) }, pruneNow)
	// A source saved on the Settings page replaces the environment's.
	updater.SetOverride(a.UpdateSourceSetting(ctx))
	// Checks for new releases, so the sidebar can mark System when one is out.
	go updater.Watch(ctx, 15*time.Minute)
	// GitHub allows 60 API calls an hour without a token; this uses 3.
	go llamaUpdates.Watch(ctx, time.Hour)
	go func() {
		n, err := st.FillCachedTokens(ctx, func(b []byte) *int64 { return capture.ParseStored(b).CachedTokens() })
		if err != nil {
			log.Error("could not fill in cached tokens for older requests", "err", err)
		} else if n > 0 {
			log.Info("filled in cached tokens for older requests", "count", n)
		}
		n, err = st.FillBuilds(ctx, func(b []byte) string { return capture.ParseStored(b).Build })
		if err != nil {
			log.Error("could not fill in server builds for older requests", "err", err)
		} else if n > 0 {
			log.Info("filled in server builds for older requests", "count", n)
		}
		n, err = st.FillDrafts(ctx, func(b []byte) (*int64, *int64) {
			if t := capture.ParseStored(b).Timings; t != nil {
				return t.DraftN, t.DraftNAccepted
			}
			return nil, nil
		})
		if err != nil {
			log.Error("could not fill in draft counts for older requests", "err", err)
		} else if n > 0 {
			log.Info("filled in draft counts for older requests", "count", n)
		}
		n, err = metrics.Recount(ctx, st, time.Now())
		if err != nil {
			log.Error("could not recount energy for older requests", "err", err)
		} else if n > 0 {
			log.Info("recounted energy for older requests that overlapped others", "count", n)
		}
		n, err = metrics.FillOverlap(ctx, st, time.Now())
		if err != nil {
			log.Error("could not work out overlap for older requests", "err", err)
		} else if n > 0 {
			log.Info("worked out overlap for older requests", "count", n)
		}
		n, err = st.FillPreviews(ctx, func(b []byte) (string, string) {
			p := capture.RequestPreviews(b)
			return p.UserEnd, p.Last
		})
		if err != nil {
			log.Error("could not fill in prompt previews for older requests", "err", err)
		} else if n > 0 {
			log.Info("filled in prompt previews for older requests", "count", n)
		}
		n, err = capture.Remeasure(ctx, st)
		if err != nil {
			log.Error("could not measure speeds again for older requests", "err", err)
		} else if n > 0 {
			log.Info("measured speeds again for older requests", "count", n)
		}
		n, err = capture.RetagIfChanged(ctx, st)
		if err != nil {
			log.Error("could not work out notable things for older requests", "err", err)
		} else if n > 0 {
			log.Info("worked out notable things for older requests", "count", n)
		}
		n, err = capture.RecheckIfChanged(ctx, st, props)
		if err != nil {
			log.Error("could not check older requests for issues", "err", err)
		} else if n > 0 {
			counts, _ := st.IssueCounts(ctx)
			log.Info("checked older requests for issues", "count", n, "issues", counts)
		}
	}()

	mux := http.NewServeMux()
	a.Register(mux)
	mux.Handle("/notus/api/", http.NotFoundHandler())
	mux.Handle("/notus/", web.Handler())
	mux.Handle("GET /notus", http.RedirectHandler("/notus/", http.StatusFound))
	// The site root opens notus-swap. llama-swap's own UI stays at /ui/.
	mux.Handle("GET /{$}", http.RedirectHandler("/notus/", http.StatusFound))
	mux.Handle("/", a.Capture)

	ln, fromSystemd, err := listener(listen)
	if err != nil {
		return false, err
	}
	if fromSystemd {
		listen = "socket from systemd"
	}
	shuttingDown, endGets := context.WithCancel(context.Background())
	defer endGets()
	// No write timeout: streamed responses can run for many minutes.
	srv := &http.Server{Handler: endOnShutdown(shuttingDown, mux), ReadHeaderTimeout: 10 * time.Second}
	errc := make(chan error, 1)
	go func() { errc <- srv.Serve(ln) }()
	log.Info("notus-swap started", "version", version, "listen", listen, "upstream", upstream, "db", dbPath)
	// An update counts as good once this version has served for 30 seconds.
	go func() {
		select {
		case <-ctx.Done():
		case <-time.After(30 * time.Second):
			if m := updater.Pending(); m != nil {
				updater.Confirm()
				log.Info("update confirmed", "from", m.From, "to", m.To)
			}
		}
	}()

	planned := make(chan bool, 1)
	go func() {
		if atOnce, ok := plan.Wait(ctx); ok {
			planned <- atOnce
		}
	}()

	drain := 10 * time.Second
	select {
	case err := <-errc:
		return false, err
	case <-ctx.Done():
		log.Info("shutting down")
	case atOnce := <-planned:
		restarting = true
		p := plan.Pending()
		if atOnce {
			log.Info("restarting without waiting for requests", "reason", p.Reason, "in_flight", hub.Count())
		} else {
			// Nothing is in flight, so this only covers a request that
			// arrives in the moment before the listener closes.
			drain = 5 * time.Minute
			log.Info("restarting", "reason", p.Reason, "waited", time.Since(p.Since).Round(time.Second))
		}
	}
	// The UI's live feed and log streams never end on their own.
	endGets()
	sctx, cancel := context.WithTimeout(context.Background(), drain)
	defer cancel()
	if err := srv.Shutdown(sctx); err != nil && !errors.Is(err, context.DeadlineExceeded) {
		return restarting, err
	}
	return restarting, nil
}

// endOnShutdown ends GET requests once shuttingDown is done. GETs are page
// loads, polls, and streams such as the live feed and logs. Inference
// requests are POSTs, and are left to finish.
func endOnShutdown(shuttingDown context.Context, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			next.ServeHTTP(w, r)
			return
		}
		ctx, cancel := context.WithCancel(r.Context())
		defer cancel()
		defer context.AfterFunc(shuttingDown, cancel)()
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}

// learnProps reads a model's limits from its llama-server once it is ready,
// then checks again that model's requests stored without a context size.
func learnProps(ctx context.Context, log *slog.Logger, st *store.Store, monitor *llamaswap.Monitor, props func(string) *store.ModelProps, model string) {
	// Asking a model that isn't loaded would make llama-swap load it.
	if !monitor.Ready(model) {
		return
	}
	p, err := monitor.FetchProps(ctx, model)
	if err != nil {
		log.Debug("could not read a model's limits", "model", model, "err", err)
		return
	}
	names := monitor.Names(model)
	caps, _ := json.Marshal(p.Caps)
	saved := store.ModelProps{NCtx: p.NCtx, NPredict: p.NPredict, Slots: p.Slots, Build: p.Build, Caps: caps}
	if err := st.SaveModelProps(ctx, names, saved, time.Now()); err != nil {
		log.Error("saving a model's limits", "model", model, "err", err)
		return
	}
	if n, err := capture.Recheck(ctx, st, props, names); err != nil {
		log.Error("checking a model's older requests for issues", "model", model, "err", err)
	} else if n > 0 {
		log.Info("checked older requests again now that the context size is known", "model", model, "count", n)
	}
	if capture.IsStrata(&saved) {
		if s, ok := strataEngine(ctx, monitor, model); ok {
			saved.Engine, _ = json.Marshal(s.Engine)
			if err := st.SaveModelProps(ctx, names, saved, time.Now()); err != nil {
				log.Error("saving Strata's engine settings", "model", model, "err", err)
			}
		}
	}
}

// strataEngine reads a Strata model's /metrics once its engine has loaded.
// Strata answers llama-swap's health check before then, and its engine's
// settings are only known after. ok is false for other servers, or when the
// model stops first.
func strataEngine(ctx context.Context, monitor *llamaswap.Monitor, model string) (s llamaswap.Strata, ok bool) {
	for {
		s, err := monitor.FetchStrata(ctx, model)
		if err != nil {
			return s, false
		}
		if s.Loaded() {
			return s, true
		}
		select {
		case <-ctx.Done():
			return s, false
		case <-time.After(5 * time.Second):
		}
		if !monitor.Ready(model) {
			return s, false
		}
	}
}

// cmdHashes holds the command hash of each loaded model, under its ID and
// each alias.
type cmdHashes struct {
	mu sync.Mutex
	m  map[string]string
}

func (c *cmdHashes) get(model string) string {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.m[model]
}

// learnCmd reads the command llama-swap started a model with, once the model
// is ready, and keeps it for tagging that model's requests.
func learnCmd(ctx context.Context, log *slog.Logger, st *store.Store, monitor *llamaswap.Monitor, cmds *cmdHashes, model string) {
	running, err := monitor.FetchRunning(ctx)
	if err != nil {
		log.Debug("could not read llama-swap's running models", "err", err)
		return
	}
	for _, r := range running {
		if r.Model != model || r.Cmd == "" {
			continue
		}
		args := r.Args()
		// A Strata command names only its config file, so its engine's
		// settings are added. Other servers have no Strata /metrics.
		if s, ok := strataEngine(ctx, monitor, model); ok {
			args = append(args, s.SettingArgs()...)
		}
		hash := llamaswap.HashArgs(args)
		if err := st.SaveModelCmd(ctx, hash, args, time.Now()); err != nil {
			log.Error("saving a model's command", "model", model, "err", err)
			return
		}
		cmds.mu.Lock()
		for _, name := range monitor.Names(model) {
			cmds.m[name] = hash
		}
		cmds.mu.Unlock()
		return
	}
}

// prune deletes old bodies at start-up, once an hour, and whenever now
// fires (the retention setting changed). keep reads the current setting.
func prune(ctx context.Context, log *slog.Logger, st *store.Store, keep func() api.Retention, now <-chan struct{}) {
	t := time.NewTicker(time.Hour)
	defer t.Stop()
	for {
		k := keep()
		n, err := st.PruneBodies(ctx, time.Now().Add(-time.Duration(k.Days)*24*time.Hour), k.GB<<30)
		if err != nil && ctx.Err() == nil {
			log.Error("pruning bodies", "err", err)
		} else if n > 0 {
			log.Info("pruned request bodies", "count", n)
		}
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		case <-now:
		}
	}
}

func envFloat(key string, fallback float64) float64 {
	if v, err := strconv.ParseFloat(os.Getenv(key), 64); err == nil {
		return v
	}
	return fallback
}

// envBool is true when key is set to 1 or true.
func envBool(key string) bool {
	v, _ := strconv.ParseBool(os.Getenv(key))
	return v
}

func env(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}
