package engine_test

import (
	"bufio"
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/TobiasLogic/palimpsest/engine"
	"github.com/TobiasLogic/palimpsest/summarize"
)

func TestCrashRecoveryChild(t *testing.T) {
	path := os.Getenv("PALIMPSEST_CRASH_DB")
	if path == "" {
		t.Skip("helper process")
	}
	pol := smallPolicy(16000)
	e, err := engine.Open(context.Background(), engine.Options{Path: path, Policy: pol, Summarizer: &summarize.Delayed{Inner: summarize.Extractive{}, Delay: 3 * time.Millisecond}})
	if err != nil {
		fmt.Println("ERR", err)
		os.Exit(3)
	}
	s, _ := e.OpenSession(context.Background(), "crash", engine.SessionConfig{})
	a, _ := s.Agent(context.Background(), "main")
	sm := newSim(99)
	out := bufio.NewWriter(os.Stdout)
	ctx := context.Background()
	for i := 0; ; i++ {
		tr := sm.turn(i)
		if tr.user != nil {
			r, err := a.Append(ctx, *tr.user)
			if err != nil {
				fmt.Println("ERR", err)
				os.Exit(3)
			}
			fmt.Fprintln(out, "ACK", r.ID)
			out.Flush()
		}
		if _, err := a.Assemble(ctx, engine.AssembleRequest{}); err != nil {
			fmt.Println("ERR", err)
			os.Exit(3)
		}
		for _, m := range append(append([]any{}, tr.assistant), anyMsgs(tr)...) {
			_ = m
		}
		r, err := a.Append(ctx, tr.assistant)
		if err != nil {
			fmt.Println("ERR", err)
			os.Exit(3)
		}
		fmt.Fprintln(out, "ACK", r.ID)
		out.Flush()
		for _, res := range tr.results {
			r, err := a.Append(ctx, res)
			if err != nil {
				fmt.Println("ERR", err)
				os.Exit(3)
			}
			fmt.Fprintln(out, "ACK", r.ID)
			out.Flush()
		}
	}
}

func anyMsgs(tr simTurn) []any { return nil }

func TestCrashRecovery(t *testing.T) {
	if testing.Short() {
		t.Skip("spawns a child process")
	}
	for round, kills := range []int{150, 400, 900} {
		path := filepath.Join(t.TempDir(), fmt.Sprintf("crash%d.db", round))
		cmd := exec.Command(os.Args[0], "-test.run=^TestCrashRecoveryChild$", "-test.count=1")
		cmd.Env = append(os.Environ(), "PALIMPSEST_CRASH_DB="+path)
		stdout, _ := cmd.StdoutPipe()
		if err := cmd.Start(); err != nil {
			t.Fatal(err)
		}
		sc := bufio.NewScanner(stdout)
		var acked []string
		for sc.Scan() {
			line := sc.Text()
			if strings.HasPrefix(line, "ERR") {
				t.Fatalf("child failed: %s", line)
			}
			if strings.HasPrefix(line, "ACK ") {
				acked = append(acked, strings.TrimPrefix(line, "ACK "))
				if len(acked) >= kills {
					break
				}
			}
		}
		_ = cmd.Process.Signal(syscall.SIGKILL)
		_ = cmd.Wait()
		e := openEngine(t, smallPolicy(16000), withPath(path))
		ctx := context.Background()
		for _, id := range acked {
			if err := e.VerifyEvent(ctx, id); err != nil {
				t.Fatalf("round %d: acknowledged event %s lost or damaged after kill: %v", round, id, err)
			}
		}
		rep, err := e.Verify(ctx, "crash")
		if err != nil {
			t.Fatal(err)
		}
		if len(rep.Problems) > 0 || rep.SQLiteCheck != "ok" {
			t.Fatalf("round %d: integrity after kill: %v %s", round, rep.Problems, rep.SQLiteCheck)
		}
		a := mustAgent(t, e, "crash", "main")
		if a.Head() < int64(len(acked)) {
			t.Fatalf("round %d: head %d < acked %d", round, a.Head(), len(acked))
		}
		mustAppendRecovery(t, a)
		t.Logf("round %d: %d acked events verified, head %d, %d summaries, %d views", round, len(acked), a.Head(), rep.Summaries, rep.Views)
	}
}

func mustAppendRecovery(t *testing.T, a *engine.Agent) {
	ctx := context.Background()
	c, err := a.Assemble(ctx, engine.AssembleRequest{})
	if err != nil {
		if strings.Contains(err.Error(), "waiting for results") {
			return
		}
		t.Fatalf("assemble after recovery: %v", err)
	}
	if c.Tokens > c.Capacity {
		t.Fatal("overflow after recovery")
	}
}
