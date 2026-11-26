package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"strings"
	"syscall"

	"github.com/TobiasLogic/palimpsest/adapters/anthropic"
	"github.com/TobiasLogic/palimpsest/adapters/ollama"
	"github.com/TobiasLogic/palimpsest/adapters/openai"
	"github.com/TobiasLogic/palimpsest/engine"
	"github.com/TobiasLogic/palimpsest/mcp"
	"github.com/TobiasLogic/palimpsest/server"
	"github.com/TobiasLogic/palimpsest/summarize"
)

const usage = `palimpsest: lossless, asynchronous context management for LLM agents

usage: palimpsest <command> [flags]

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
  config          print the default policy as JSON
  version         print version information

run "palimpsest <command> -h" for flags.
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
	case "config":
		err = printJSON(engine.DefaultPolicy())
	case "version":
		err = printJSON(map[string]any{"version": server.Version, "api": server.APIVersion, "config_version": engine.ConfigVersion})
	case "-h", "--help", "help":
		fmt.Print(usage)
	default:
		fmt.Fprintf(os.Stderr, "unknown command %q\n\n%s", os.Args[1], usage)
		os.Exit(2)
	}
	if err != nil && !errors.Is(err, context.Canceled) {
		fmt.Fprintln(os.Stderr, "palimpsest:", err)
		os.Exit(1)
	}
}

type common struct {
	db         string
	config     string
	summarizer string
	sumURL     string
	sumModel   string
	artifacts  string
}

func (c *common) bind(fs *flag.FlagSet) {
	def := os.Getenv("PALIMPSEST_DB")
	if def == "" {
		def = "palimpsest.db"
	}
	fs.StringVar(&c.db, "db", def, "SQLite database path (env PALIMPSEST_DB)")
	fs.StringVar(&c.config, "config", "", "policy JSON file (see 'palimpsest config')")
	fs.StringVar(&c.summarizer, "summarizer", envOr("PALIMPSEST_SUMMARIZER", "extractive"), "extractive, ollama, openai, anthropic or none")
	fs.StringVar(&c.sumURL, "summarizer-url", os.Getenv("PALIMPSEST_SUMMARIZER_URL"), "base URL for an OpenAI-compatible summarizer (for example http://127.0.0.1:11434/v1)")
	fs.StringVar(&c.sumModel, "summarizer-model", os.Getenv("PALIMPSEST_SUMMARIZER_MODEL"), "summarizer model name")
	fs.StringVar(&c.artifacts, "artifacts", "", "directory for an external artifact store (default: inside the database)")
}

func envOr(k, d string) string {
	if v := os.Getenv(k); v != "" {
		return v
	}
	return d
}

func (c *common) open(ctx context.Context) (*engine.Engine, error) {
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
		if k := os.Getenv("PALIMPSEST_SUMMARIZER_KEY"); k != "" {
			cl.APIKey = k
		}
		o.Summarizer = summarize.LLM{Model: cl}
	case "ollama":
		o.Summarizer = summarize.LLM{Model: ollama.New(c.sumURL, c.sumModel, 16384), MaxInput: 10000}
	case "anthropic":
		cl := anthropic.NewClient(c.sumModel)
		if k := os.Getenv("PALIMPSEST_SUMMARIZER_KEY"); k != "" {
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
	tokenFile := fs.String("token-file", "", "file holding a bearer token required on every request (env PALIMPSEST_TOKEN)")
	fs.Parse(args)
	token := os.Getenv("PALIMPSEST_TOKEN")
	if *tokenFile != "" {
		b, err := os.ReadFile(*tokenFile)
		if err != nil {
			return err
		}
		token = strings.TrimSpace(string(b))
	}
	if *addr != "" && !strings.HasPrefix(*addr, "127.0.0.1:") && !strings.HasPrefix(*addr, "localhost:") && !strings.HasPrefix(*addr, "[::1]:") && token == "" {
		return errors.New("refusing to listen on a non-loopback address without a bearer token (set PALIMPSEST_TOKEN or --token-file)")
	}
	e, err := c.open(ctx)
	if err != nil {
		return err
	}
	defer e.Close()
	fmt.Fprintf(os.Stderr, "palimpsest %s serving api %s on tcp=%q socket=%q db=%s\n", server.Version, server.APIVersion, *addr, *sock, c.db)
	return server.New(e, token).Serve(ctx, server.Listeners{Addr: *addr, Socket: *sock})
}

func cmdMCP(ctx context.Context, args []string) error {
	fs := flag.NewFlagSet("mcp", flag.ExitOnError)
	var c common
	c.bind(fs)
	session := fs.String("session", envOr("PALIMPSEST_SESSION", "default"), "session id")
	agent := fs.String("agent", envOr("PALIMPSEST_AGENT", "mcp"), "agent id")
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

func openAgent(ctx context.Context, e *engine.Engine, session, agent string) (*engine.Agent, error) {
	s, err := e.OpenSession(ctx, session, engine.SessionConfig{})
	if err != nil {
		return nil, err
	}
	return s.Agent(ctx, agent)
}

func cmdStats(ctx context.Context, args []string) error {
	fs := flag.NewFlagSet("stats", flag.ExitOnError)
	var c common
	c.bind(fs)
	session, agent := agentFlags(fs)
	all := fs.Bool("engine", false, "print engine-wide statistics instead of one agent")
	fs.Parse(args)
	c.summarizer = "none"
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
	a, err := openAgent(ctx, e, *session, *agent)
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
	e, err := c.open(ctx)
	if err != nil {
		return err
	}
	defer e.Close()
	sessions, _ := e.Sessions(ctx)
	a, err := openAgent(ctx, e, *session, *agent)
	if err != nil {
		return err
	}
	st, _ := a.Stats(ctx)
	s, _ := e.OpenSession(ctx, *session, engine.SessionConfig{})
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
	e, err := c.open(ctx)
	if err != nil {
		return err
	}
	defer e.Close()
	a, err := openAgent(ctx, e, *session, *agent)
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
		return errors.New("usage: palimpsest show [flags] <event-id>")
	}
	c.summarizer = "none"
	e, err := c.open(ctx)
	if err != nil {
		return err
	}
	defer e.Close()
	a, err := openAgent(ctx, e, *session, *agent)
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
		if err := e.DB().Checkpoint(ctx); err != nil {
			return err
		}
		if err := e.DB().Vacuum(ctx); err != nil {
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
	e, err := c.open(ctx)
	if err != nil {
		return err
	}
	defer e.Close()
	if err := e.DB().Checkpoint(ctx); err != nil {
		return err
	}
	return e.DB().Vacuum(ctx)
}
