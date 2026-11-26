package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"path/filepath"
	"runtime/debug"
	"strings"
	"syscall"

	"github.com/TobiasLogic/stratavell/adapters/anthropic"
	"github.com/TobiasLogic/stratavell/adapters/ollama"
	"github.com/TobiasLogic/stratavell/adapters/openai"
	"github.com/TobiasLogic/stratavell/engine"
	"github.com/TobiasLogic/stratavell/internal/store"
	"github.com/TobiasLogic/stratavell/mcp"
	"github.com/TobiasLogic/stratavell/server"
	"github.com/TobiasLogic/stratavell/summarize"
)

const usage = `stratavell: context management for long-running LLM agents (losslessly retrievable history, asynchronous compaction)

usage: stratavell <command> [flags]

commands:
  serve           run the HTTP API (TCP and/or Unix socket)
  mcp             run the MCP server on stdio
  stats           print engine or agent statistics
  inspect         print an agent's current view (frontier and counters)
  search          search an agent's archive
  show            print one archived event exactly
  verify          check journal hashes, summary lineage and SQLite integrity
  delete-session  delete a session and everything derived from it
  vacuum          checkpoint the WAL and reclaim free pages
  migrate         copy a database from a pre-0.1.0 (palimpsest) location to a new path
  config          print the default policy as JSON
  version         print version information

run "stratavell <command> -h" for flags.
`

func main() {
	if len(os.Args) < 2 {
		fmt.Fprint(os.Stderr, usage)
		os.Exit(2)
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	var err error
	switch os.Args[1] {
	case "serve":
		err = cmdServe(ctx, os.Args[2:])
	case "mcp":
		err = cmdMCP(ctx, os.Args[2:])
	case "stats":
		err = cmdStats(ctx, os.Args[2:])
	case "inspect":
		err = cmdInspect(ctx, os.Args[2:])
	case "search":
		err = cmdSearch(ctx, os.Args[2:])
	case "show":
		err = cmdShow(ctx, os.Args[2:])
	case "verify":
		err = cmdVerify(ctx, os.Args[2:])
	case "delete-session":
		err = cmdDelete(ctx, os.Args[2:])
	case "vacuum":
		err = cmdVacuum(ctx, os.Args[2:])
	case "migrate":
		err = cmdMigrate(ctx, os.Args[2:])
	case "config":
		err = printJSON(engine.DefaultPolicy())
	case "version":
		err = printJSON(versionInfo())
	case "-h", "--help", "help":
		fmt.Print(usage)
	default:
		fmt.Fprintf(os.Stderr, "unknown command %q\n\n%s", os.Args[1], usage)
		os.Exit(2)
	}
	if err != nil && !errors.Is(err, context.Canceled) {
		fmt.Fprintln(os.Stderr, "stratavell:", err)
		os.Exit(1)
	}
}

const (
	defaultDB = "stratavell.db"
	legacyDB  = "palimpsest.db"
)

type common struct {
	db         string
	config     string
	summarizer string
	sumURL     string
	sumModel   string
	artifacts  string
	existing   bool
}

func (c *common) bind(fs *flag.FlagSet) {
	fs.StringVar(&c.db, "db", "", "SQLite database path (default "+defaultDB+", env STRATAVELL_DB)")
	fs.StringVar(&c.config, "config", "", "policy JSON file (see 'stratavell config')")
	fs.StringVar(&c.summarizer, "summarizer", envOr("SUMMARIZER", "extractive"), "extractive, ollama, openai, anthropic or none (env STRATAVELL_SUMMARIZER)")
	fs.StringVar(&c.sumURL, "summarizer-url", env("SUMMARIZER_URL"), "base URL for an OpenAI-compatible summarizer, for example http://127.0.0.1:11434/v1 (env STRATAVELL_SUMMARIZER_URL)")
	fs.StringVar(&c.sumModel, "summarizer-model", env("SUMMARIZER_MODEL"), "summarizer model name (env STRATAVELL_SUMMARIZER_MODEL)")
	fs.StringVar(&c.artifacts, "artifacts", "", "directory for an external artifact store (default: inside the database)")
}

var warned = map[string]bool{}

func warnOnce(key, text string) {
	if warned[key] {
		return
	}
	warned[key] = true
	fmt.Fprintln(os.Stderr, "stratavell: warning:", text)
}

func env(name string) string {
	if v := os.Getenv("STRATAVELL_" + name); v != "" {
		return v
	}
	if v := os.Getenv("PALIMPSEST_" + name); v != "" {
		warnOnce("env-"+name, fmt.Sprintf("PALIMPSEST_%s is deprecated and will be removed in a later release; set STRATAVELL_%s instead", name, name))
		return v
	}
	return ""
}

func envOr(name, d string) string {
	if v := env(name); v != "" {
		return v
	}
	return d
}

func fileExists(p string) bool {
	_, err := os.Stat(p)
	return err == nil
}

func (c *common) resolveDB() string {
	if c.db != "" {
		return c.db
	}
	if v := env("DB"); v != "" {
		return v
	}
	if !fileExists(defaultDB) && fileExists(legacyDB) {
		warnOnce("legacy-db", "using "+legacyDB+" from a pre-0.1.0 install; run 'stratavell migrate' to copy it to "+defaultDB+", or pass --db")
		return legacyDB
	}
	return defaultDB
}

func (c *common) open(ctx context.Context) (*engine.Engine, error) {
	c.db = c.resolveDB()
	if c.existing && !fileExists(c.db) {
		return nil, fmt.Errorf("database %s does not exist (pass --db or set STRATAVELL_DB)", c.db)
	}
	pol := engine.DefaultPolicy()
	if c.config != "" {
		b, err := os.ReadFile(c.config)
		if err != nil {
			return nil, err
		}
		if pol, err = engine.ParsePolicy(b); err != nil {
			return nil, err
		}
	}
	o := engine.Options{Path: c.db, Policy: pol}
	switch c.summarizer {
	case "extractive", "":
		o.Summarizer = summarize.Extractive{}
	case "none":
	case "openai":
		cl := openai.NewClient(c.sumURL, c.sumModel)
		if k := env("SUMMARIZER_KEY"); k != "" {
			cl.APIKey = k
		}
		o.Summarizer = summarize.LLM{Model: cl}
	case "ollama":
		o.Summarizer = summarize.LLM{Model: ollama.New(c.sumURL, c.sumModel, 16384), MaxInput: 10000}
	case "anthropic":
		cl := anthropic.NewClient(c.sumModel)
		if k := env("SUMMARIZER_KEY"); k != "" {
			cl.APIKey = k
		}
		if c.sumURL != "" {
			cl.BaseURL = c.sumURL
		}
		o.Summarizer = summarize.LLM{Model: cl}
	default:
		return nil, fmt.Errorf("unknown summarizer %q", c.summarizer)
	}
	if c.artifacts != "" {
		st, err := engine.NewFileArtifactStore(c.artifacts)
		if err != nil {
			return nil, err
		}
		o.Artifacts = st
	}
	return engine.Open(ctx, o)
}

func printJSON(v any) error {
	enc := json.NewEncoder(os.Stdout)
	enc.SetIndent("", "  ")
	enc.SetEscapeHTML(false)
	return enc.Encode(v)
}

func cmdServe(ctx context.Context, args []string) error {
	fs := flag.NewFlagSet("serve", flag.ExitOnError)
	var c common
	c.bind(fs)
	addr := fs.String("addr", "127.0.0.1:7077", "TCP listen address, empty to disable")
	sock := fs.String("socket", "", "Unix domain socket path (created with mode 0600)")
	tokenFile := fs.String("token-file", "", "file holding a bearer token required on every request (env STRATAVELL_TOKEN)")
	fs.Parse(args)
	token := env("TOKEN")
	if *tokenFile != "" {
		b, err := os.ReadFile(*tokenFile)
		if err != nil {
			return err
		}
		token = strings.TrimSpace(string(b))
	}
	if *addr != "" && !strings.HasPrefix(*addr, "127.0.0.1:") && !strings.HasPrefix(*addr, "localhost:") && !strings.HasPrefix(*addr, "[::1]:") && token == "" {
		return errors.New("refusing to listen on a non-loopback address without a bearer token (set STRATAVELL_TOKEN or --token-file)")
	}
	e, err := c.open(ctx)
	if err != nil {
		return err
	}
	defer e.Close()
	fmt.Fprintf(os.Stderr, "stratavell %s serving api %s on tcp=%q socket=%q db=%s\n", server.Version, server.APIVersion, *addr, *sock, c.db)
	return server.New(e, token).Serve(ctx, server.Listeners{Addr: *addr, Socket: *sock})
}

func cmdMCP(ctx context.Context, args []string) error {
	fs := flag.NewFlagSet("mcp", flag.ExitOnError)
	var c common
	c.bind(fs)
	session := fs.String("session", envOr("SESSION", "default"), "session id (env STRATAVELL_SESSION)")
	agent := fs.String("agent", envOr("AGENT", "mcp"), "agent id (env STRATAVELL_AGENT)")
	passive := fs.Bool("passive", true, "passive memory mode: expose memory_archive for hosts that cannot hand their context to the engine")
	fs.Parse(args)
	e, err := c.open(ctx)
	if err != nil {
		return err
	}
	defer e.Close()
	s := &mcp.Server{E: e, Session: *session, Agent: *agent, Version: server.Version, Passive: *passive}
	return s.Serve(ctx, os.Stdin, os.Stdout)
}

func agentFlags(fs *flag.FlagSet) (*string, *string) {
	return fs.String("session", "default", "session id"), fs.String("agent", "main", "agent id")
}

func openAgent(ctx context.Context, e *engine.Engine, session, agent string) (*engine.Session, *engine.Agent, error) {
	s, err := e.LookupSession(ctx, session)
	if err != nil {
		return nil, nil, err
	}
	a, err := s.LookupAgent(ctx, agent)
	if err != nil {
		return nil, nil, err
	}
	return s, a, nil
}

func cmdStats(ctx context.Context, args []string) error {
	fs := flag.NewFlagSet("stats", flag.ExitOnError)
	var c common
	c.bind(fs)
	session, agent := agentFlags(fs)
	all := fs.Bool("engine", false, "print engine-wide statistics instead of one agent")
	fs.Parse(args)
	c.summarizer = "none"
	c.existing = true
	e, err := c.open(ctx)
	if err != nil {
		return err
	}
	defer e.Close()
	if *all {
		st, err := e.Stats(ctx)
		if err != nil {
			return err
		}
		return printJSON(st)
	}
	_, a, err := openAgent(ctx, e, *session, *agent)
	if err != nil {
		return err
	}
	st, err := a.Stats(ctx)
	if err != nil {
		return err
	}
	return printJSON(st)
}

func cmdInspect(ctx context.Context, args []string) error {
	fs := flag.NewFlagSet("inspect", flag.ExitOnError)
	var c common
	c.bind(fs)
	session, agent := agentFlags(fs)
	fs.Parse(args)
	c.summarizer = "none"
	c.existing = true
	e, err := c.open(ctx)
	if err != nil {
		return err
	}
	defer e.Close()
	sessions, _ := e.Sessions(ctx)
	s, a, err := openAgent(ctx, e, *session, *agent)
	if err != nil {
		return err
	}
	st, _ := a.Stats(ctx)
	agents, _ := s.Agents(ctx)
	items, _ := a.TaskState(ctx)
	return printJSON(map[string]any{"sessions": sessions, "agents": agents, "stats": st, "frontier": a.Frontier(), "task_state": items})
}

func cmdSearch(ctx context.Context, args []string) error {
	fs := flag.NewFlagSet("search", flag.ExitOnError)
	var c common
	c.bind(fs)
	session, agent := agentFlags(fs)
	limit := fs.Int("limit", 10, "maximum hits")
	fs.Parse(args)
	c.summarizer = "none"
	c.existing = true
	e, err := c.open(ctx)
	if err != nil {
		return err
	}
	defer e.Close()
	_, a, err := openAgent(ctx, e, *session, *agent)
	if err != nil {
		return err
	}
	hits, err := a.Search(ctx, strings.Join(fs.Args(), " "), engine.SearchFilters{}, *limit)
	if err != nil {
		return err
	}
	return printJSON(hits)
}

func cmdShow(ctx context.Context, args []string) error {
	fs := flag.NewFlagSet("show", flag.ExitOnError)
	var c common
	c.bind(fs)
	session, agent := agentFlags(fs)
	fs.Parse(args)
	if fs.NArg() != 1 {
		return errors.New("usage: stratavell show [flags] <event-id>")
	}
	c.summarizer = "none"
	c.existing = true
	e, err := c.open(ctx)
	if err != nil {
		return err
	}
	defer e.Close()
	_, a, err := openAgent(ctx, e, *session, *agent)
	if err != nil {
		return err
	}
	ev, m, err := a.GetEvent(ctx, fs.Arg(0))
	if err != nil {
		return err
	}
	return printJSON(map[string]any{"event": ev, "message": m})
}

func cmdVerify(ctx context.Context, args []string) error {
	fs := flag.NewFlagSet("verify", flag.ExitOnError)
	var c common
	c.bind(fs)
	session := fs.String("session", "", "session id (default: all sessions)")
	fs.Parse(args)
	c.summarizer = "none"
	c.existing = true
	e, err := c.open(ctx)
	if err != nil {
		return err
	}
	defer e.Close()
	ids := []string{*session}
	if *session == "" {
		if ids, err = e.Sessions(ctx); err != nil {
			return err
		}
	}
	bad := false
	out := map[string]engine.VerifyReport{}
	for _, id := range ids {
		rep, err := e.Verify(ctx, id)
		if err != nil {
			return err
		}
		out[id] = rep
		if len(rep.Problems) > 0 || rep.SQLiteCheck != "ok" {
			bad = true
		}
	}
	if err := printJSON(out); err != nil {
		return err
	}
	if bad {
		return errors.New("verification found problems")
	}
	return nil
}

func cmdDelete(ctx context.Context, args []string) error {
	fs := flag.NewFlagSet("delete-session", flag.ExitOnError)
	var c common
	c.bind(fs)
	session := fs.String("session", "", "session id to delete")
	vacuum := fs.Bool("vacuum", true, "vacuum afterwards so deleted bytes leave the file")
	fs.Parse(args)
	if *session == "" {
		return errors.New("--session is required")
	}
	c.summarizer = "none"
	c.existing = true
	e, err := c.open(ctx)
	if err != nil {
		return err
	}
	defer e.Close()
	rep, err := e.DeleteSession(ctx, *session)
	if err != nil {
		return err
	}
	if *vacuum {
		if err := e.Checkpoint(ctx); err != nil {
			return err
		}
		if err := e.Vacuum(ctx); err != nil {
			return err
		}
	}
	return printJSON(rep)
}

func cmdVacuum(ctx context.Context, args []string) error {
	fs := flag.NewFlagSet("vacuum", flag.ExitOnError)
	var c common
	c.bind(fs)
	fs.Parse(args)
	c.summarizer = "none"
	c.existing = true
	e, err := c.open(ctx)
	if err != nil {
		return err
	}
	defer e.Close()
	if err := e.Checkpoint(ctx); err != nil {
		return err
	}
	return e.Vacuum(ctx)
}

func versionInfo() map[string]any {
	out := map[string]any{"version": server.Version, "api": server.APIVersion, "config_version": engine.ConfigVersion}
	if bi, ok := debug.ReadBuildInfo(); ok {
		out["go"] = bi.GoVersion
		for _, kv := range bi.Settings {
			switch kv.Key {
			case "vcs.revision":
				out["commit"] = kv.Value
			case "vcs.modified":
				out["modified"] = kv.Value == "true"
			}
		}
	}
	return out
}

func cmdMigrate(ctx context.Context, args []string) error {
	fs := flag.NewFlagSet("migrate", flag.ExitOnError)
	from := fs.String("from", legacyDB, "existing database to copy")
	to := fs.String("to", defaultDB, "new database path; must not exist yet")
	dry := fs.Bool("dry-run", false, "report what would happen and change nothing")
	fs.Parse(args)
	if !fileExists(*from) {
		return fmt.Errorf("source database %s does not exist", *from)
	}
	for _, p := range []string{*to, *to + "-wal", *to + "-shm"} {
		if fileExists(p) {
			return fmt.Errorf("refusing to overwrite %s; move it away or choose another --to", p)
		}
	}
	if *dry {
		return printJSON(map[string]any{"from": *from, "to": *to, "action": "copy (source is left in place)", "dry_run": true})
	}
	if dir := filepath.Dir(*to); dir != "." {
		if err := os.MkdirAll(dir, 0o700); err != nil {
			return err
		}
	}
	src, err := store.Open(ctx, *from, store.Options{})
	if err != nil {
		return fmt.Errorf("open %s: %w", *from, err)
	}
	var srcEvents int64
	serr := src.R.QueryRowContext(ctx, "SELECT COUNT(*) FROM events").Scan(&srcEvents)
	if serr == nil {
		serr = src.VacuumInto(ctx, *to)
	}
	src.Close()
	if serr != nil {
		return fmt.Errorf("copy to %s: %w", *to, serr)
	}
	dst, err := engine.Open(ctx, engine.Options{Path: *to})
	if err != nil {
		return fmt.Errorf("open copy %s: %w", *to, err)
	}
	defer dst.Close()
	dstStats, err := dst.Stats(ctx)
	if err != nil {
		return err
	}
	ids, err := dst.Sessions(ctx)
	if err != nil {
		return err
	}
	problems := 0
	for _, id := range ids {
		rep, err := dst.Verify(ctx, id)
		if err != nil {
			return err
		}
		problems += len(rep.Problems)
		if rep.SQLiteCheck != "ok" {
			problems++
		}
	}
	out := map[string]any{"from": *from, "to": *to, "sessions": len(ids), "events_source": srcEvents, "events_copy": dstStats.Events, "verify_problems": problems, "source": "left in place; delete it yourself once the copy is confirmed"}
	if err := printJSON(out); err != nil {
		return err
	}
	if problems > 0 || srcEvents != dstStats.Events {
		return errors.New("the copy did not verify; the source database is unchanged")
	}
	return nil
}
