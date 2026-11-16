// SPDX-License-Identifier: Apache-2.0

package main

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"github.com/spf13/cobra"
	"k8s.io/apimachinery/pkg/api/resource"
	"k8s.io/client-go/rest"

	"github.com/shivangtanwar/kbark/internal/ai"
	"github.com/shivangtanwar/kbark/internal/auth"
	"github.com/shivangtanwar/kbark/internal/autofix"
	"github.com/shivangtanwar/kbark/internal/config"
	"github.com/shivangtanwar/kbark/internal/fleet"
	"github.com/shivangtanwar/kbark/internal/incident"
	"github.com/shivangtanwar/kbark/internal/lab"
	"github.com/shivangtanwar/kbark/internal/notify"
	"github.com/shivangtanwar/kbark/internal/policy"
	"github.com/shivangtanwar/kbark/internal/remediate"
	"github.com/shivangtanwar/kbark/internal/server"
	"github.com/shivangtanwar/kbark/internal/version"
)

type serveOptions struct {
	demo           bool
	demoKubeconfig string
	contexts       []string
	addr           string
	auditLog       string
	memoryCeiling  string
	verifyTimeout  time.Duration
	verifyHold     time.Duration
	protected      []string
	noAI           bool
	inCluster      bool
	clusterName    string
	policyFile     string
	stateFile      string
	authFile       string
	publicURL      string
	notifyWebhook  string
	notifyEvents   []string
	confirmOOM     bool
	insecure       bool
	trustProxy     bool
}

var serveOpts serveOptions

var serveCmd = &cobra.Command{
	Use:   "serve",
	Short: "Watch clusters, detect pod failures and serve the autofix dashboard",
	Long: `serve watches every pod in the given kubeconfig contexts, opens an
incident for each failure it detects, and proposes a fix. Nothing is
written to a cluster until a named person approves that fix on the
dashboard.

  kbark serve --contexts kind-kbark-dev,kind-kbark-staging

Sign-in is configured with --auth-file (see "kbark users hash"). Without
it the console is open and listens on localhost only; kbark refuses to
listen on any other address without sign-in.`,
	RunE: runServe,
}

func init() {
	f := serveCmd.Flags()
	f.StringSliceVar(&serveOpts.contexts, "contexts", nil, "Kubeconfig contexts to watch, comma-separated (default: the current context)")
	f.StringVar(&serveOpts.addr, "addr", "127.0.0.1:8765", "Address the dashboard listens on")
	f.StringVar(&serveOpts.auditLog, "audit-log", "", "Audit log file (default: <user cache dir>/kbark/audit.jsonl)")
	f.StringVar(&serveOpts.memoryCeiling, "memory-ceiling", "2Gi", "Largest memory limit kbark will propose (a policy file can override it per namespace)")
	f.DurationVar(&serveOpts.verifyTimeout, "verify-timeout", 150*time.Second, "How long an applied fix has to clear the failure")
	f.DurationVar(&serveOpts.verifyHold, "verify-hold", 15*time.Second, "How long the failure must stay clear to count as verified")
	f.StringSliceVar(&serveOpts.protected, "protected-namespaces", autofix.DefaultProtected, "Namespaces where kbark detects but never proposes changes (a policy file adds finer rules)")
	f.BoolVar(&serveOpts.noAI, "no-ai", false, "Do not call an AI provider for explanations")
	f.BoolVar(&serveOpts.inCluster, "in-cluster", false, "Watch the cluster kbark runs in, using its service account")
	f.StringVar(&serveOpts.clusterName, "cluster-name", "in-cluster", "Display name for the cluster in --in-cluster mode")
	f.StringVar(&serveOpts.policyFile, "policy", "", "Policy file: per-namespace rules for what kbark may propose")
	f.StringVar(&serveOpts.stateFile, "state", "", "File where incidents and trends are kept across restarts (default: <user cache dir>/kbark/state.json; \"none\" disables)")
	f.StringVar(&serveOpts.authFile, "auth-file", "", "Users file enabling sign-in and roles (create entries with `kbark users hash`)")
	f.StringVar(&serveOpts.publicURL, "public-url", "", "Address people use to reach the console, e.g. https://kbark.example.com (for a reverse proxy)")
	f.StringVar(&serveOpts.notifyWebhook, "notify-webhook", os.Getenv("KBARK_NOTIFY_WEBHOOK"), "Webhook URL to post incident events to (Slack-compatible); defaults to $KBARK_NOTIFY_WEBHOOK")
	f.StringSliceVar(&serveOpts.notifyEvents, "notify-events", notify.DefaultEvents, "Events that trigger a notification")
	f.BoolVar(&serveOpts.confirmOOM, "confirm-oom", false, "Confirm inferred out-of-memory kills from the node's kubelet metrics (needs get on nodes/proxy)")
	f.BoolVar(&serveOpts.trustProxy, "trust-proxy", false, "Take client addresses from X-Real-IP / X-Forwarded-For; set only when all traffic arrives through your reverse proxy")
	f.BoolVar(&serveOpts.demo, "demo", false, "Enable the demo lab: the console can inject sample failures into the kbark-demo namespace, using the same credentials kbark watches with")
	f.StringVar(&serveOpts.demoKubeconfig, "demo-kubeconfig", "", "Enable the demo lab with separate credentials: a kubeconfig holding one context per watched cluster, with the same names (see deploy/demo-rbac.yaml)")
	f.BoolVar(&serveOpts.insecure, "insecure-no-auth", false, "Allow a non-loopback --addr without --auth-file. Anyone who can reach the port can approve changes")
	rootCmd.AddCommand(serveCmd)
}

func runServe(_ *cobra.Command, _ []string) error {
	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()
	o := serveOpts

	if _, err := resource.ParseQuantity(o.memoryCeiling); err != nil {
		return fmt.Errorf("--memory-ceiling: %w", err)
	}
	pol := policy.Default(o.protected, o.memoryCeiling)
	if o.policyFile != "" {
		var err error
		if pol, err = policy.Load(o.policyFile, pol); err != nil {
			return err
		}
	} else if err := pol.Validate(); err != nil {
		return err
	}

	// Sign-in. An open console is acceptable only on loopback.
	var users *auth.Manager
	if o.authFile != "" {
		var err error
		if users, err = auth.Load(o.authFile); err != nil {
			return err
		}
	}
	allowedHosts := server.LocalHosts(o.addr)
	if users == nil && allowedHosts == nil && !o.insecure {
		return fmt.Errorf("refusing to listen on %s without sign-in: anyone who can reach it could approve changes.\nAdd --auth-file (see `kbark users hash`), or pass --insecure-no-auth if another layer already restricts access", o.addr)
	}
	secureCookies := false
	if o.publicURL != "" {
		u, err := url.Parse(o.publicURL)
		if err != nil || u.Host == "" || (u.Scheme != "http" && u.Scheme != "https") {
			return fmt.Errorf("--public-url %q is not an http(s) URL", o.publicURL)
		}
		secureCookies = u.Scheme == "https"
		if allowedHosts != nil {
			allowedHosts = append(allowedHosts, u.Host)
		}
	}

	clusters, err := connectClusters(o)
	if err != nil {
		return err
	}

	cacheDir, _ := os.UserCacheDir()
	auditPath := o.auditLog
	if auditPath == "" && cacheDir != "" {
		auditPath = filepath.Join(cacheDir, "kbark", "audit.jsonl")
	}
	statePath := o.stateFile
	switch {
	case statePath == "none":
		statePath = ""
	case statePath == "" && cacheDir != "":
		statePath = filepath.Join(cacheDir, "kbark", "state.json")
	}
	audit, err := incident.OpenAudit(auditPath)
	if err != nil {
		return err
	}
	defer audit.Close()

	explainer, aiNote := buildExplainer(o.noAI)

	var hook *notify.Webhook
	if o.notifyWebhook != "" {
		if hook, err = notify.New(o.notifyWebhook, o.publicURL, o.notifyEvents); err != nil {
			return err
		}
		go hook.Run(ctx)
	}

	hub := server.NewHub()
	demoLab, err := buildLab(o, clusters)
	if err != nil {
		return err
	}
	if demoLab != nil {
		demoLab.OnChange = hub.Notify
		demoLab.Log = func(cluster, scenario, actor, note string) {
			audit.Append(incident.Entry{
				Time: time.Now(), Cluster: cluster, Namespace: lab.Namespace, Workload: "demo/" + scenario,
				Event: "demo", Actor: actor, Note: "demo lab: " + scenario + " " + note,
			})
		}
	}
	store := incident.NewStore(audit, hub.Notify)
	if hook != nil {
		store.OnEvent(hook.Notify)
	}
	engine := autofix.New(clusters, store, autofix.Config{
		VerifyTimeout: o.verifyTimeout,
		VerifyHold:    o.verifyHold,
		Policy:        pol,
		Planner:       &remediate.Planner{DryRun: true, ConfirmOOM: o.confirmOOM},
		Explainer:     explainer,
		StatePath:     statePath,
	}, hub.Notify)
	restored, err := engine.Restore()
	if err != nil {
		return err
	}

	for _, c := range clusters {
		c.Start(ctx)
	}
	done := make(chan struct{})
	go func() { engine.Run(ctx); close(done) }()

	srv := &server.Server{
		Engine: engine, Store: store, Audit: audit, Hub: hub, Auth: users, Notify: hook, Lab: demoLab,
		Info:          server.Info{Version: version.Version, AI: explainer.Name(), AuditPath: auditPath, StatePath: statePath, PublicURL: o.publicURL, Demo: demoLab != nil},
		AllowedHosts:  allowedHosts,
		SecureCookies: secureCookies,
		TrustProxy:    o.trustProxy,
	}
	httpSrv := &http.Server{Addr: o.addr, Handler: srv.Handler(), ReadHeaderTimeout: 10 * time.Second}

	ln, err := net.Listen("tcp", o.addr)
	if err != nil {
		return fmt.Errorf("listen on %s: %w", o.addr, err)
	}

	names := make([]string, len(clusters))
	for i, c := range clusters {
		names[i] = c.Name
	}
	shown := "http://" + ln.Addr().String()
	if o.publicURL != "" {
		shown = o.publicURL + " (listening on " + ln.Addr().String() + ")"
	}
	fmt.Printf("kbark autofix console: %s\n", shown)
	fmt.Printf("  clusters:  %s\n", strings.Join(names, ", "))
	fmt.Printf("  policy:    %s\n", pol.Source)
	fmt.Printf("  state:     %s (%d incidents restored)\n", orNone(statePath), restored)
	fmt.Printf("  audit log: %s\n", orNone(auditPath))
	fmt.Printf("  AI:        %s\n", aiNote)
	switch {
	case users != nil:
		fmt.Printf("  sign-in:   required (%d users from %s)\n", len(users.Users()), o.authFile)
	case allowedHosts == nil:
		fmt.Println("  sign-in:   NONE on a non-loopback address (--insecure-no-auth). Anyone who can reach this port can approve changes.")
	default:
		fmt.Println("  sign-in:   none (localhost only; operator names are self-declared)")
	}
	if hook != nil {
		fmt.Printf("  notify:    webhook at %s\n", hook.Status().Host)
	}
	if demoLab != nil {
		fmt.Printf("  demo lab:  ON. Administrators can inject sample failures into namespace %s from the console.\n", lab.Namespace)
	}

	errCh := make(chan error, 1)
	go func() { errCh <- httpSrv.Serve(ln) }()
	select {
	case err := <-errCh:
		if !errors.Is(err, http.ErrServerClosed) {
			return err
		}
	case <-ctx.Done():
	}
	shutdownCtx, stop := context.WithTimeout(context.Background(), 3*time.Second)
	defer stop()
	_ = httpSrv.Shutdown(shutdownCtx)
	// Let the engine write its final state before the process exits.
	select {
	case <-done:
	case <-time.After(5 * time.Second):
	}
	return nil
}

// buildLab returns the demo lab when a demo flag is set, or nil.
func buildLab(o serveOptions, clusters []*fleet.Cluster) (*lab.Lab, error) {
	if !o.demo && o.demoKubeconfig == "" {
		return nil, nil
	}
	kubeconfig := o.demoKubeconfig
	if kubeconfig == "" && kubeFlags.KubeConfig != nil {
		kubeconfig = *kubeFlags.KubeConfig
	}
	cfgs := map[string]*rest.Config{}
	var order []string
	for _, c := range clusters {
		var cfg *rest.Config
		var err error
		if o.inCluster && o.demoKubeconfig == "" {
			cfg, err = rest.InClusterConfig()
		} else {
			cfg, err = fleet.RESTConfigForContext(kubeconfig, c.Name)
		}
		if err != nil {
			return nil, fmt.Errorf("demo lab: %w", err)
		}
		cfgs[c.Name] = cfg
		order = append(order, c.Name)
	}
	return lab.New(order, cfgs)
}

func orNone(s string) string {
	if s == "" {
		return "none (in memory only)"
	}
	return s
}

func connectClusters(o serveOptions) ([]*fleet.Cluster, error) {
	if o.inCluster {
		cfg, err := rest.InClusterConfig()
		if err != nil {
			return nil, fmt.Errorf("--in-cluster: %w", err)
		}
		c, err := fleet.Connect(o.clusterName, cfg)
		if err != nil {
			return nil, err
		}
		return []*fleet.Cluster{c}, nil
	}

	kubeconfig := ""
	if kubeFlags.KubeConfig != nil {
		kubeconfig = *kubeFlags.KubeConfig
	}
	contexts := o.contexts
	if len(contexts) == 0 {
		if kubeFlags.Context != nil && *kubeFlags.Context != "" {
			contexts = []string{*kubeFlags.Context}
		} else {
			cur, err := fleet.CurrentContext(kubeconfig)
			if err != nil {
				return nil, fmt.Errorf("no --contexts given and no current context: %w", err)
			}
			contexts = []string{cur}
		}
	}
	var clusters []*fleet.Cluster
	seen := map[string]bool{}
	for _, name := range contexts {
		name = strings.TrimSpace(name)
		if name == "" || seen[name] {
			continue
		}
		seen[name] = true
		cfg, err := fleet.RESTConfigForContext(kubeconfig, name)
		if err != nil {
			return nil, err
		}
		c, err := fleet.Connect(name, cfg)
		if err != nil {
			return nil, err
		}
		clusters = append(clusters, c)
	}
	return clusters, nil
}

// buildExplainer resolves the active profile's AI provider. A missing
// key is not an error: the dashboard runs without explanations.
func buildExplainer(disabled bool) (*autofix.Explainer, string) {
	if disabled {
		return nil, "disabled (--no-ai)"
	}
	cfgPath, _ := config.DefaultPath()
	cfg, err := config.Load(cfgPath)
	if err != nil {
		return nil, "disabled (config: " + err.Error() + ")"
	}
	profile, err := cfg.Resolve(profileFlag)
	if err != nil {
		return nil, "disabled (" + err.Error() + ")"
	}
	provider, err := ai.NewEndpoint(ai.Endpoint{Provider: profile.Provider, BaseURL: profile.BaseURL, APIKeyEnv: profile.APIKeyEnv})
	if err != nil {
		return nil, fmt.Sprintf("disabled (%s: %v)", profile.Provider, err)
	}
	x := autofix.NewExplainer(provider, profile.Model)
	return x, x.Name() + " (explanations only; never chooses or runs a fix)"
}
