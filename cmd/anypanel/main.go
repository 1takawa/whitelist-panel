// Command anypanel is the Whitelist Panel server.
//
//	anypanel serve                run the panel (default)
//	anypanel init-admin USERNAME  create or reset the administrator (password is read from stdin)
//	anypanel import-legacy FILE|-  import users exported from the previous panel (see README)
//	anypanel open-ports SERVER    open the ports of a node installed from the panel in its ufw (SERVER is its name or id)
//	anypanel setup-reality SERVER DOMAIN [--force]  build the nginx-SNI scheme (nginx, certificate, decoy site, REALITY) on a node
//	anypanel agent                run the agent of an extra server (see README)
//	anypanel version
package main

import (
	"context"
	"crypto/tls"
	"errors"
	"flag"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"anypanel2/internal/agent"
	"anypanel2/internal/auth"
	"anypanel2/internal/config"
	"anypanel2/internal/front"
	"anypanel2/internal/geoip"
	"anypanel2/internal/legacy"
	"anypanel2/internal/panel"
	"anypanel2/internal/routing"
	"anypanel2/internal/server"
	"anypanel2/internal/store"
	"anypanel2/internal/store/memstore"
	"anypanel2/internal/store/mongostore"
	"anypanel2/internal/sysinfo"
	"anypanel2/internal/update"
	"anypanel2/internal/webui"
	"anypanel2/internal/xray"
)

var version = "dev"

func main() {
	log := slog.New(slog.NewTextHandler(os.Stderr, nil))
	cmd, args := "serve", os.Args[1:]
	if len(args) > 0 && !strings.HasPrefix(args[0], "-") {
		cmd, args = args[0], args[1:]
	}
	var err error
	switch cmd {
	case "serve":
		err = cmdServe(log, args)
	case "init-admin":
		err = cmdInitAdmin(args)
	case "set-domains":
		err = cmdSetDomains(args)
	case "import-legacy":
		err = cmdImportLegacy(args)
	case "setup-reality":
		err = cmdSetupReality(args)
	case "open-ports":
		err = cmdOpenPorts(args)
	case "agent":
		err = cmdAgent(log, args)
	case "version":
		fmt.Println(version)
	default:
		err = fmt.Errorf("unknown command %q (serve | init-admin | set-domains | import-legacy | open-ports | setup-reality | agent | version)", cmd)
	}
	if err != nil {
		log.Error(err.Error())
		os.Exit(1)
	}
}

func cmdServe(log *slog.Logger, args []string) error {
	fs := flag.NewFlagSet("serve", flag.ContinueOnError)
	memory := fs.Bool("memory", false, "DEV ONLY: keep data in memory (seed admin from ANYPANEL_DEV_ADMIN=user:password)")
	if err := fs.Parse(args); err != nil {
		return err
	}
	cfg := config.FromEnv()
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	var st store.Store
	if *memory {
		log.Warn("running with in-memory store: all data is lost on exit")
		ms := memstore.New()
		user, pass := "admin", "dev-password-123"
		if v := os.Getenv("ANYPANEL_DEV_ADMIN"); v != "" {
			if u, p, ok := strings.Cut(v, ":"); ok {
				user, pass = u, p
			}
		}
		if err := putAdmin(ctx, ms, user, pass); err != nil {
			return err
		}
		st = ms
	} else {
		ms, err := mongostore.Open(ctx, cfg.MongoURI, cfg.MongoDatabase)
		if err != nil {
			return err
		}
		st = ms
	}
	defer func() {
		c, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = st.Close(c)
	}()

	am, err := auth.NewManager(st, cfg.SessionTTL)
	if err != nil {
		return err
	}

	xr := xray.NewManager(cfg.XrayBin, filepath.Join(cfg.StateDir, "xray", "config.json"),
		fmt.Sprintf("127.0.0.1:%d", cfg.XrayAPIPort), log)
	defer xr.Stop()
	if !xr.Installed() {
		log.Warn("xray binary not found: inbounds can be edited but nothing is served", "path", cfg.XrayBin)
	}
	svc := panel.New(st, xr,
		xray.Options{APIPort: cfg.XrayAPIPort, BlockBittorrent: cfg.BlockBittorrent},
		[]int{cfg.ListenPort(), cfg.XrayAPIPort}, log)
	svc.GeoCountry = geoip.Country
	if cfg.CaddyRoutesFile != "" {
		svc.Front = front.NewCaddy(cfg.CaddyRoutesFile, strings.Fields(cfg.CaddyReloadCmd))
	}
	if cfg.NginxSNIDir != "" {
		svc.SNI = front.NewSNI(cfg.NginxConf, cfg.Caddyfile, cfg.NginxSNIDir, cfg.CaddySitesDir, cfg.DecoyDir, strings.Fields(cfg.NginxReloadCmd), strings.Fields(cfg.CaddyReloadCmd))
		go func() {
			if err := svc.SyncSNI(ctx); err != nil {
				log.Warn("cascade domains: nginx and Caddy files were not restored", "err", err)
			}
		}()
	}
	go func() {
		if err := svc.RepairCascades(ctx); err != nil {
			log.Warn("cascades: the check for interrupted creations failed", "err", err)
		}
	}()
	svc.Version = version
	svc.PublicIP = panel.LookupPublicIP // the domain checks compare with this machine's real address
	svc.Update = &update.Updater{Repo: os.Getenv("WHITELIST_REPO"), Dir: filepath.Join(cfg.StateDir, "update")}
	svc.RegRuBase = os.Getenv("REGRU_API_URL") // empty = the real REG.RU API; set to a stand-in only for testing
	sampler := sysinfo.NewSampler()
	svc.Sampler = sampler
	go sampler.Run(ctx, 5*time.Second)
	go svc.RunBackups(ctx)
	svc.Routing = routing.NewGitHub()
	svc.StartTelegram(ctx)
	go svc.Run(ctx, 10*time.Second)

	srv := &http.Server{
		Addr:              cfg.Listen,
		Handler:           server.New(cfg, st, am, svc, webui.FS(), log).Handler(),
		ReadHeaderTimeout: 10 * time.Second,
		ReadTimeout:       30 * time.Second,
		WriteTimeout:      60 * time.Second,
		IdleTimeout:       120 * time.Second,
	}
	errc := make(chan error, 1)
	go func() {
		log.Info("Whitelist Panel listening", "addr", cfg.Listen, "version", version)
		errc <- srv.ListenAndServe()
	}()
	select {
	case err := <-errc:
		return err
	case <-ctx.Done():
		log.Info("shutting down")
		sctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		return srv.Shutdown(sctx)
	}
}

func cmdInitAdmin(args []string) error {
	if len(args) != 1 {
		return errors.New("usage: anypanel init-admin USERNAME (password from stdin)")
	}
	user := args[0]
	raw, err := io.ReadAll(io.LimitReader(os.Stdin, 1024))
	if err != nil {
		return err
	}
	pass := strings.TrimRight(string(raw), "\r\n")

	cfg := config.FromEnv()
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	st, err := mongostore.Open(ctx, cfg.MongoURI, cfg.MongoDatabase)
	if err != nil {
		return err
	}
	defer st.Close(ctx)
	return putAdmin(ctx, st, user, pass)
}

// cmdSetDomains stores the panel and REALITY domains used in client links (used by the installer).
func cmdSetDomains(args []string) error {
	if len(args) != 2 {
		return errors.New("usage: anypanel set-domains PANEL_DOMAIN REALITY_DOMAIN")
	}
	cfg := config.FromEnv()
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	st, err := mongostore.Open(ctx, cfg.MongoURI, cfg.MongoDatabase)
	if err != nil {
		return err
	}
	defer st.Close(ctx)
	svc := &panel.Service{Store: st}
	cur, err := svc.GetSettings(ctx) // read-modify-write: keep subscription settings
	if err != nil {
		return err
	}
	cur.PanelDomain, cur.RealityDomain = args[0], args[1]
	_, err = svc.PutSettings(ctx, *cur)
	return err
}

func putAdmin(ctx context.Context, st store.Store, user, pass string) error {
	if !auth.ValidUsername(user) {
		return errors.New("admin username may contain only letters, numbers and underscores")
	}
	if len(pass) < auth.MinPasswordLength {
		return fmt.Errorf("password must be at least %d characters", auth.MinPasswordLength)
	}
	hash, err := auth.HashPassword(pass)
	if err != nil {
		return err
	}
	return st.PutAdmin(ctx, store.Admin{Username: user, PasswordHash: hash, CreatedAt: time.Now().UTC()})
}

// cmdImportLegacy imports users exported from the previous panel:
//
//	mongoexport --db <БАЗА_ПРЕЖНЕЙ_ПАНЕЛИ> --collection users --quiet --jsonArray | anypanel import-legacy - [--dry-run]
func cmdImportLegacy(args []string) error {
	fs := flag.NewFlagSet("import-legacy", flag.ContinueOnError)
	dry := fs.Bool("dry-run", false, "show what would happen without saving")
	from := fs.String("expiry-from", "creation", "count expiration_days from: creation | now")
	inbounds := fs.String("inbounds", "", "comma-separated inbound IDs to attach users to (default: all enabled)")
	if len(args) == 0 {
		return errors.New("usage: anypanel import-legacy FILE|- [--dry-run] [--expiry-from creation|now] [--inbounds ID,ID]")
	}
	file := args[0]
	if err := fs.Parse(args[1:]); err != nil {
		return err
	}
	var in io.Reader = os.Stdin
	if file != "-" {
		f, err := os.Open(file)
		if err != nil {
			return err
		}
		defer f.Close()
		in = f
	}
	users, err := legacy.Parse(in)
	if err != nil {
		return err
	}
	cfg := config.FromEnv()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()
	st, err := mongostore.Open(ctx, cfg.MongoURI, cfg.MongoDatabase)
	if err != nil {
		return err
	}
	defer st.Close(ctx)
	// No Xray here: the running panel notices the new clients on its next 10-second sync.
	svc := &panel.Service{Store: st, Now: time.Now}
	opts := panel.LegacyOptions{ExpiryFrom: *from, DryRun: *dry}
	for _, id := range strings.Split(*inbounds, ",") {
		if id = strings.TrimSpace(id); id != "" {
			opts.InboundIDs = append(opts.InboundIDs, id)
		}
	}
	rep, err := svc.ImportLegacy(ctx, users, opts)
	if err != nil {
		return err
	}
	for _, it := range rep.Items {
		line := fmt.Sprintf("%-8s %s", it.Action, it.Name)
		if it.Reason != "" {
			line += " — " + it.Reason
		}
		if len(it.Notes) > 0 {
			line += " (" + strings.Join(it.Notes, "; ") + ")"
		}
		fmt.Println(line)
	}
	verb := "создано"
	if rep.DryRun {
		verb = "будет создано (проверка, ничего не сохранено)"
	}
	fmt.Printf("\n%s: %d, пропущено: %d\n", verb, rep.Created, rep.Skipped)
	for k, n := range rep.NotMigrated {
		fmt.Printf("не перенесено: %s — %d\n", k, n)
	}
	return nil
}

// cmdAgent runs the agent that lets a panel drive Xray on this machine (see internal/agent). Settings come from the
// environment: AGENT_TOKEN (required, 32+ characters), AGENT_LISTEN (default 0.0.0.0:8765), AGENT_HOSTS (names or
// addresses added to the certificate), STATE_DIR and XRAY_BIN as for the panel.
func cmdAgent(log *slog.Logger, args []string) error {
	fs := flag.NewFlagSet("agent", flag.ContinueOnError)
	printFP := fs.Bool("print-fingerprint", false, "create the certificate if needed, print its SHA-256 fingerprint and exit")
	if err := fs.Parse(args); err != nil {
		return err
	}
	cfg := config.FromEnv()
	certDir := filepath.Join(cfg.StateDir, "agent")
	var hosts []string
	for _, h := range strings.FieldsFunc(os.Getenv("AGENT_HOSTS"), func(r rune) bool { return r == ',' || r == ' ' }) {
		hosts = append(hosts, h)
	}
	cert, err := agent.LoadOrCreateCert(certDir, hosts...)
	if err != nil {
		return fmt.Errorf("agent certificate: %w", err)
	}
	if *printFP {
		fmt.Println(agent.CertFingerprint(cert))
		return nil
	}

	token := strings.TrimSpace(os.Getenv("AGENT_TOKEN"))
	if len(token) < 32 {
		return errors.New("AGENT_TOKEN must be set to a secret of at least 32 characters (the panel shows it when the server is added)")
	}
	listen := os.Getenv("AGENT_LISTEN")
	if listen == "" {
		listen = fmt.Sprintf("0.0.0.0:%d", agent.DefaultPort)
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	xr := xray.NewManager(cfg.XrayBin, filepath.Join(cfg.StateDir, "xray", "config.json"),
		fmt.Sprintf("127.0.0.1:%d", agent.APIPort), log)
	defer xr.Stop()
	if !xr.Installed() {
		log.Warn("xray binary not found: the panel will be told that this server cannot serve traffic", "path", cfg.XrayBin)
	} else if err := xr.Restart(ctx); err != nil {
		// No saved config yet is normal on a fresh server; the panel pushes one as soon as it connects.
		log.Info("xray not started yet", "reason", err.Error())
	}
	sampler := sysinfo.NewSampler()
	go sampler.Run(ctx, 5*time.Second)

	a := &agent.Server{Token: token, Xray: xr, Sampler: sampler, Version: version, Log: log}
	srv := &http.Server{
		Addr:              listen,
		Handler:           a.Handler(),
		TLSConfig:         &tls.Config{Certificates: []tls.Certificate{cert}, MinVersion: tls.VersionTLS12},
		ReadHeaderTimeout: 10 * time.Second,
		ReadTimeout:       30 * time.Second,
		WriteTimeout:      60 * time.Second,
		IdleTimeout:       60 * time.Second,
	}
	errc := make(chan error, 1)
	go func() {
		log.Info("Whitelist Panel agent listening", "addr", listen, "version", version, "fingerprint", agent.CertFingerprint(cert))
		errc <- srv.ListenAndServeTLS("", "")
	}()
	select {
	case err := <-errc:
		return err
	case <-ctx.Done():
		sctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		return srv.Shutdown(sctx)
	}
}

// cmdOpenPorts logs in to a node the panel installed (with the SSH key the panel keeps for it) and opens the agent
// and connection ports in ufw; the same action as the "Открыть порты" button. Run it on the panel's server.
func cmdOpenPorts(args []string) error {
	if len(args) != 1 {
		return errors.New("usage: anypanel open-ports SERVER_NAME_OR_ID")
	}
	return runNodeJob(args[0], func(ctx context.Context, svc *panel.Service, id string) (*panel.JobView, error) {
		return svc.StartNodeFirewall(ctx, id)
	})
}

// cmdSetupReality builds the nginx-SNI scheme on an installed node: nginx, a certificate and a decoy site for DOMAIN
// (an A record pointing at the node), and a new REALITY connection; the same action as the button on the servers page.
func cmdSetupReality(args []string) error {
	force := false
	var rest []string
	for _, a := range args {
		if a == "--force" {
			force = true
		} else {
			rest = append(rest, a)
		}
	}
	if len(rest) != 2 {
		return errors.New("usage: anypanel setup-reality SERVER_NAME_OR_ID DOMAIN [--force]   (--force stops whatever holds ports 80 and 443)")
	}
	return runNodeJob(rest[0], func(ctx context.Context, svc *panel.Service, id string) (*panel.JobView, error) {
		return svc.StartNodeReality(ctx, id, panel.NodeRealityRequest{Domain: rest[1], ForcePort: force})
	})
}

// runNodeJob starts a background node job for the named server and prints its log until it ends.
func runNodeJob(server string, start func(context.Context, *panel.Service, string) (*panel.JobView, error)) error {
	cfg := config.FromEnv()
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Minute)
	defer cancel()
	st, err := mongostore.Open(ctx, cfg.MongoURI, cfg.MongoDatabase)
	if err != nil {
		return err
	}
	defer st.Close(ctx)
	srvs, err := st.ListServers(ctx)
	if err != nil {
		return err
	}
	var id string
	for _, s := range srvs {
		if s.ID == server || strings.EqualFold(s.Name, server) {
			id = s.ID
		}
	}
	if id == "" {
		return fmt.Errorf("server %q not found", server)
	}
	svc := &panel.Service{Store: st, Now: time.Now}
	j, err := start(ctx, svc, id)
	if err != nil {
		return err
	}
	printed := 0
	for {
		v, ok := svc.NodeJob(j.ID)
		if !ok {
			return errors.New("the job disappeared")
		}
		for ; printed < len(v.Log); printed++ {
			fmt.Println(v.Log[printed])
		}
		if v.Status != "running" {
			if v.Status == "failed" {
				return errors.New(v.Error)
			}
			return nil
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(500 * time.Millisecond):
		}
	}
}
