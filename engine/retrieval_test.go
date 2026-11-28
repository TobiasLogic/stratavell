package engine_test

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/TobiasLogic/stratavell/engine"
	"github.com/TobiasLogic/stratavell/msg"
)

func callAndResult(t testing.TB, a *engine.Agent, id, tool, input, out string) engine.EventRef {
	t.Helper()
	mustAppend(t, a, msg.Message{Role: msg.RoleAssistant, Parts: []msg.Part{{Type: msg.PartToolCall, ToolCallID: id, ToolName: tool, Input: json.RawMessage(input)}}})
	return mustAppend(t, a, msg.ToolResult(id, tool, out, false, msg.Meta{}))
}

func memoryCall(t testing.TB, a *engine.Agent, id, tool string, args any) engine.ToolOutput {
	t.Helper()
	in, _ := json.Marshal(args)
	mustAppend(t, a, msg.Message{Role: msg.RoleAssistant, Parts: []msg.Part{{Type: msg.PartToolCall, ToolCallID: id, ToolName: tool, Input: in}}})
	out := a.HandleTool(context.Background(), tool, in)
	mustAppend(t, a, msg.ToolResult(id, tool, out.Text, out.IsError, msg.Meta{}))
	return out
}

func sheetJSON(rows int) string {
	vals := [][]string{{"Date", "Visitor", "Score", "Home", "Score.1", "Status"}}
	teams := []string{"Calgary Flames", "Utah Hockey Club", "Chicago Blackhawks", "Carolina Hurricanes", "Boston Bruins"}
	for i := 0; i < rows; i++ {
		vals = append(vals, []string{fmt.Sprintf("2024-10-%02d", 8+i%20), teams[i%5], fmt.Sprint(i % 6), teams[(i+2)%5], fmt.Sprint((i * 7) % 5), "Regulation"})
	}
	b, _ := json.MarshalIndent(map[string]any{"spreadsheetId": "s1", "valueRanges": []any{map[string]any{"range": "schedule", "values": vals}}}, "", "  ")
	return string(b)
}

func productsJSON(n int) string {
	var ps []map[string]any
	for i := 0; i < n; i++ {
		ps = append(ps, map[string]any{"id": 100 + i, "name": fmt.Sprintf("Product %d", i), "sku": fmt.Sprintf("SKU-%04d", i), "stock_quantity": (i * 13) % 70, "price": fmt.Sprintf("%.2f", 5+float64(i)*1.5), "status": "publish", "description": strings.Repeat("long text ", 20), "meta_data": []any{map[string]any{"key": "supplier", "value": []string{"Acme", "FurniturePro Ltd."}[i%2]}}})
	}
	b, _ := json.MarshalIndent(ps, "", "  ")
	return string(b)
}

func excelOutput(rows int) string {
	var cells []map[string]any
	for r := 2; r < 2+rows; r++ {
		for c, col := range []string{"B", "C", "D"} {
			cells = append(cells, map[string]any{"address": fmt.Sprintf("%s%d", col, r), "value": float64(r*100 + c), "row": r, "column": c + 2, "validation": map[string]any{"has_validation": false}})
		}
	}
	b, _ := json.MarshalIndent(map[string]any{"range": "B2:D9", "sheet_name": "RawData", "cells": cells}, "", "  ")
	esc := strings.NewReplacer(`\`, `\\`, "\n", `\n`, "'", `\'`).Replace(string(b))
	return "read_data_from_excelOutput(result='" + esc + "')"
}

func TestQueryStructuredOutputs(t *testing.T) {
	e := openEngine(t, engine.DefaultPolicy(), manual())
	a := mustAgent(t, e, "s", "main")
	ctx := context.Background()
	sheet := callAndResult(t, a, "c1", "google_sheet_get_sheet_data", `{"sheet":"schedule"}`, sheetJSON(300))
	prods := callAndResult(t, a, "c2", "woo_products_list", `{}`, productsJSON(120))
	xl := callAndResult(t, a, "c3", "excel_read_data_from_excel", `{}`, excelOutput(40))

	out, err := a.Query(ctx, engine.QueryRequest{ID: sheet.ID, Where: map[string]any{"Visitor": "Boston Bruins", "Score": ">=4"}, Fields: []string{"Date", "Score", "Home"}})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "table at valueRanges.0.values | 300 records") || !strings.Contains(out, "csv: #,Date,Score,Home\n") {
		t.Fatalf("sheet header not detected:\n%s", out)
	}
	for _, rec := range csvRows(t, out) {
		if len(rec) != 4 || (rec[2] != "4" && rec[2] != "5") {
			t.Fatalf("row does not satisfy Score>=4 or was not projected: %v", rec)
		}
	}

	out, err = a.Query(ctx, engine.QueryRequest{ID: prods.ID, Where: map[string]any{"stock_quantity": "<5", "meta_data.value": "~furniture"}, Fields: []string{"id", "sku", "stock_quantity"}, Sort: "-stock_quantity"})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "json | 120 records") || strings.Contains(out, "long text") {
		t.Fatalf("product query:\n%s", out)
	}
	var prev = 1000
	rows := csvRows(t, out)
	if len(rows) == 0 {
		t.Fatalf("no product rows:\n%s", out)
	}
	for _, rec := range rows {
		var q int
		fmt.Sscanf(rec[3], "%d", &q)
		if q >= 5 || q > prev {
			t.Fatalf("filter or descending sort broken at %v", rec)
		}
		prev = q
	}

	out, err = a.Query(ctx, engine.QueryRequest{ID: xl.ID, Where: map[string]any{"row": 7}, Fields: []string{"address", "value"}})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "3 match") || !strings.Contains(out, ",B7,700\n") {
		t.Fatalf("python-wrapped excel output not parsed:\n%s", out)
	}

	out, err = a.Query(ctx, engine.QueryRequest{ID: prods.ID})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "fields: description, id, meta_data, name, price, sku, status, stock_quantity") {
		t.Fatalf("bare query should list fields:\n%s", out)
	}
	if _, err := a.Query(ctx, engine.QueryRequest{ID: prods.ID, Path: "data.items"}); err == nil || !strings.Contains(err.Error(), "path") {
		t.Fatalf("a missing path should be reported, got %v", err)
	}
}

func TestQueryTextAndTables(t *testing.T) {
	e := openEngine(t, engine.DefaultPolicy(), manual())
	a := mustAgent(t, e, "s", "main")
	ctx := context.Background()
	var csv strings.Builder
	csv.WriteString("name,region,sales\n")
	for i := 0; i < 50; i++ {
		fmt.Fprintf(&csv, "item%d,%s,%d\n", i, []string{"north", "south"}[i%2], i*10)
	}
	c := callAndResult(t, a, "c1", "read_file", `{"path":"sales.csv"}`, csv.String())
	out, err := a.Query(ctx, engine.QueryRequest{ID: c.ID, Where: map[string]any{"region": "south", "sales": ">400"}})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "table | 50 records, 5 match") || !strings.Contains(out, "\n49,item49,south,490\n") {
		t.Fatalf("csv query:\n%s", out)
	}
	md := "| a | b |\n|---|---|\n| 1 | x |\n| 2 | y |\n| 3 | x |\n"
	m := callAndResult(t, a, "c2", "bash", `{"command":"report"}`, md)
	if out, _ := a.Query(ctx, engine.QueryRequest{ID: m.ID, Where: map[string]any{"b": "x"}}); !strings.Contains(out, "2 match") {
		t.Fatalf("markdown table query:\n%s", out)
	}
	var log strings.Builder
	for i := 1; i <= 200; i++ {
		if i == 120 {
			log.WriteString("ERROR: connection refused to db:5432\n")
			continue
		}
		fmt.Fprintf(&log, "step %d ok\n", i)
	}
	l := callAndResult(t, a, "c3", "bash", `{"command":"run"}`, log.String())
	out, err = a.Query(ctx, engine.QueryRequest{ID: l.ID, Grep: "connection refused", Context: 1})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "L119: step 119 ok") || !strings.Contains(out, "L120: ERROR") || !strings.Contains(out, "L121: step 121 ok") || strings.Contains(out, "L118") {
		t.Fatalf("grep with context:\n%s", out)
	}
	if _, err := a.Query(ctx, engine.QueryRequest{ID: l.ID, Where: map[string]any{"x": "1"}}); err == nil {
		t.Fatal("where on plain text should be refused")
	}
}

func TestQueryPagesWithinBudget(t *testing.T) {
	p := engine.DefaultPolicy()
	p.Retrieval.MaxResultTokens = 1500
	e := openEngine(t, p, manual())
	a := mustAgent(t, e, "s", "main")
	ref := callAndResult(t, a, "c1", "woo_products_list", `{}`, productsJSON(400))
	out := a.HandleTool(context.Background(), "memory_query", json.RawMessage(fmt.Sprintf(`{"id":%q,"fields":["id","sku"],"limit":1000}`, ref.ID)))
	if out.IsError || !strings.Contains(out.Text, "more matching records; call again with offset") {
		t.Fatalf("expected a bounded first page:\n%s", out.Text)
	}
	i := strings.Index(out.Text, "offset ")
	var next int
	fmt.Sscanf(out.Text[i+len("offset "):], "%d", &next)
	out2 := a.HandleTool(context.Background(), "memory_query", json.RawMessage(fmt.Sprintf(`{"id":%q,"fields":["id","sku"],"offset":%d}`, ref.ID, next)))
	if !strings.Contains(out2.Text, fmt.Sprintf("\n%d,", next)) {
		t.Fatalf("second page should start at record %d:\n%s", next, out2.Text[:200])
	}
}

func TestArtifactHashPrefixes(t *testing.T) {
	e := openEngine(t, engine.DefaultPolicy(), manual())
	a := mustAgent(t, e, "s", "main")
	other := mustAgent(t, e, "s2", "main")
	ctx := context.Background()
	big := productsJSON(60)
	ref := callAndResult(t, a, "c1", "woo_products_list", `{}`, big)
	page, err := a.ReadEvent(ctx, ref.ID, 0)
	if err != nil {
		t.Fatal(err)
	}
	i := strings.Index(page, "sha256 ")
	prefix := page[i+7 : i+19]
	for _, id := range []string{prefix, "sha256:" + prefix, strings.ToUpper(prefix)} {
		out := a.HandleTool(ctx, "memory_get_artifact", json.RawMessage(fmt.Sprintf(`{"id":%q,"length":1000}`, id)))
		if out.IsError || !strings.Contains(out.Text, "Product 0") {
			t.Fatalf("hash prefix %q from the event header did not resolve: %s", id, out.Text)
		}
	}
	callAndResult(t, a, "c2", "woo_products_list", `{"page":2}`, productsJSON(8))
	callAndResult(t, a, "c3", "woo_products_list", `{"page":2}`, productsJSON(8))
	view, err := a.Assemble(ctx, engine.AssembleRequest{})
	if err != nil {
		t.Fatal(err)
	}
	text := contextText(view)
	j := strings.Index(text, "(sha256 ")
	if j < 0 {
		t.Fatalf("expected a duplicate notice with a hash:\n%s", text)
	}
	dupPrefix := text[j+8 : j+20]
	out := a.HandleTool(ctx, "memory_get_artifact", json.RawMessage(fmt.Sprintf(`{"id":%q,"length":100}`, dupPrefix)))
	if out.IsError {
		t.Fatalf("identical outputs should resolve to one item: %s", out.Text)
	}
	if out := a.HandleTool(ctx, "memory_get_artifact", json.RawMessage(`{"id":"abc"}`)); !out.IsError || !strings.Contains(out.Text, "prefix of at least 8") {
		t.Fatalf("a short id should explain the accepted forms: %s", out.Text)
	}
	if out := other.HandleTool(ctx, "memory_get_artifact", json.RawMessage(fmt.Sprintf(`{"id":%q}`, prefix))); !out.IsError || !strings.Contains(out.Text, "not found") {
		t.Fatalf("another session must not resolve this prefix: %s", out.Text)
	}
	if out := other.HandleTool(ctx, "memory_query", json.RawMessage(fmt.Sprintf(`{"id":%q}`, prefix))); !out.IsError {
		t.Fatalf("another session must not query this output: %s", out.Text)
	}
}

func TestRetrievalPagesStayVisible(t *testing.T) {
	p := engine.DefaultPolicy()
	p.Budget.ContextWindow = 64000
	p.Budget.MaxOutputTokens = 4000
	e := openEngine(t, p, manual())
	a := mustAgent(t, e, "s", "main")
	ctx := context.Background()
	mustAppend(t, a, msg.Text(msg.RoleUser, "check the products"))
	ref := callAndResult(t, a, "c1", "woo_products_list", `{}`, productsJSON(500))
	if _, err := a.Assemble(ctx, engine.AssembleRequest{IncludeMemoryTools: true}); err != nil {
		t.Fatal(err)
	}
	out := memoryCall(t, a, "m1", "memory_get_event", map[string]any{"id": ref.ID})
	if out.IsError {
		t.Fatal(out.Text)
	}
	if n := e.Policy().Retrieval.MaxResultTokens; estimate(out.Text) > n+50 {
		t.Fatalf("page of %d tokens exceeds the %d-token page size", estimate(out.Text), n)
	}
	view, err := a.Assemble(ctx, engine.AssembleRequest{IncludeMemoryTools: true})
	if err != nil {
		t.Fatal(err)
	}
	text := contextText(view)
	if strings.Contains(text, "tool=memory_get_event") {
		t.Fatal("the page the agent just asked for was externalized")
	}
	if !strings.Contains(text, out.Text[len(out.Text)-200:]) {
		t.Fatal("the retrieved page is not in the next context in full")
	}
	big := memoryCall(t, a, "m2", "memory_get_event", map[string]any{"id": ref.ID, "offset": 30000, "max_tokens": 100000})
	if !strings.Contains(big.Text, "page limited to") {
		t.Fatalf("an oversized max_tokens should be capped by the budget: %s", big.Text[len(big.Text)-120:])
	}
}

func estimate(s string) int { return len(s) / 3 }

func csvRows(t *testing.T, out string) [][]string {
	t.Helper()
	var rows [][]string
	in := false
	for _, l := range strings.Split(out, "\n") {
		if strings.HasPrefix(l, "csv: ") {
			in = true
			continue
		}
		if !in || l == "" || strings.HasPrefix(l, "[") {
			continue
		}
		rows = append(rows, strings.Split(l, ","))
	}
	return rows
}

func TestQueryArgumentsAsStrings(t *testing.T) {
	e := openEngine(t, engine.DefaultPolicy(), manual())
	a := mustAgent(t, e, "s", "main")
	ref := callAndResult(t, a, "c1", "woo_products_list", `{}`, productsJSON(40))
	out := a.HandleTool(context.Background(), "memory_query", json.RawMessage(fmt.Sprintf(`{"id":%q,"fields":"id, sku","limit":"5","offset":"2"}`, ref.ID)))
	if out.IsError || !strings.Contains(out.Text, "csv: #,id,sku\n") || !strings.Contains(out.Text, "showing 3-7") {
		t.Fatalf("numbers and fields sent as strings should work: %s", out.Text)
	}
	page := a.HandleTool(context.Background(), "memory_get_event", json.RawMessage(fmt.Sprintf(`{"id":%q,"max_tokens":"300"}`, ref.ID)))
	if page.IsError || estimate(page.Text) > 600 {
		t.Fatalf("max_tokens as a string should apply: %d", estimate(page.Text))
	}
}

func TestRepeatAnsweredAfterMasking(t *testing.T) {
	p := smallPolicy(16000)
	e := openEngine(t, p, manual())
	a := mustAgent(t, e, "s", "main")
	ctx := context.Background()
	mustAppend(t, a, msg.Text(msg.RoleUser, "go"))
	ref := callAndResult(t, a, "c0", "bash", `{"command":"inventory"}`, strings.Repeat("widget 42 in bin A7\n", 30))
	if _, err := a.Assemble(ctx, engine.AssembleRequest{IncludeMemoryTools: true}); err != nil {
		t.Fatal(err)
	}
	args := map[string]any{"id": ref.ID, "grep": "widget"}
	if out := memoryCall(t, a, "m1", "memory_query", args); out.IsError {
		t.Fatal(out.Text)
	}
	masked, turns := false, 0
	for i := 0; i < 40 && !masked; i++ {
		turns++
		for j := 0; j < 2; j++ {
			callAndResult(t, a, fmt.Sprintf("f%d-%d", i, j), "bash", `{"command":"step"}`, strings.Repeat(fmt.Sprintf("filler %d.%d output line\n", i, j), 150))
		}
		mustAppend(t, a, msg.Text(msg.RoleAssistant, "next"))
		view, err := a.Assemble(ctx, engine.AssembleRequest{IncludeMemoryTools: true})
		if err != nil {
			t.Fatal(err)
		}
		masked = strings.Contains(strings.Join(view.Report.Actions, ","), "mask_epoch") && !strings.Contains(contextText(view), "L1: widget 42 in bin A7")
	}
	if !masked || turns >= e.Policy().Retrieval.DedupWindowTurns {
		t.Fatalf("setup: the earlier query result must be masked within the repeat window (masked %v after %d turns)", masked, turns)
	}
	in, _ := json.Marshal(args)
	out := a.HandleTool(ctx, "memory_query", in)
	if strings.Contains(out.Text, "already answered") || !strings.Contains(out.Text, "widget 42") {
		t.Fatalf("a repeat whose earlier result was masked should be answered: %s", out.Text)
	}
}

func TestFreshOutputKeptOnce(t *testing.T) {
	p := engine.DefaultPolicy()
	p.Budget.ContextWindow = 64000
	p.Budget.MaxOutputTokens = 4000
	p.Budget.MaxObservationToken = 2000
	p.Masking.FreshFull = 0.2
	e := openEngine(t, p, manual())
	a := mustAgent(t, e, "s", "main")
	ctx := context.Background()
	mustAppend(t, a, msg.Text(msg.RoleUser, "list products"))
	callAndResult(t, a, "c1", "woo_products_list", `{}`, productsJSON(25))
	view, err := a.Assemble(ctx, engine.AssembleRequest{})
	if err != nil {
		t.Fatal(err)
	}
	if view.Report.FreshFull != 1 || !strings.Contains(contextText(view), `"sku": "SKU-0012"`) {
		t.Fatalf("a new oversize output that fits should be shown in full once (fresh %d)", view.Report.FreshFull)
	}
	mustAppend(t, a, msg.Text(msg.RoleAssistant, "noted"))
	view, err = a.Assemble(ctx, engine.AssembleRequest{})
	if err != nil {
		t.Fatal(err)
	}
	text := contextText(view)
	if strings.Contains(text, `"sku": "SKU-0012"`) || !strings.Contains(text, "structure: JSON, 25 records") || !strings.Contains(text, "memory_query(") {
		t.Fatalf("after the agent's reply the output should be a preview with its structure:\n%s", text)
	}

	q := engine.DefaultPolicy()
	q.Budget.ContextWindow = 64000
	q.Budget.MaxOutputTokens = 4000
	q.Budget.MaxObservationToken = 2000
	if q.Masking.FreshFull != 0 {
		t.Fatal("fresh_full_fraction should default to 0")
	}
	e2 := openEngine(t, q, manual())
	b := mustAgent(t, e2, "s", "main")
	mustAppend(t, b, msg.Text(msg.RoleUser, "list products"))
	callAndResult(t, b, "c1", "woo_products_list", `{}`, productsJSON(25))
	view, err = b.Assemble(ctx, engine.AssembleRequest{})
	if err != nil {
		t.Fatal(err)
	}
	if view.Report.FreshFull != 0 || strings.Contains(contextText(view), `"sku": "SKU-0012"`) {
		t.Fatal("fresh_full_fraction 0, the default, should externalize at once")
	}
}

func TestFreshOutputDroppedUnderPressure(t *testing.T) {
	p := engine.DefaultPolicy()
	p.Budget.ContextWindow = 16000
	p.Budget.MaxOutputTokens = 1000
	p.Budget.MaxObservationToken = 1500
	p.Masking.FreshFull = 0.6
	e := openEngine(t, p, manual())
	a := mustAgent(t, e, "s", "main")
	mustAppend(t, a, msg.Text(msg.RoleUser, "go"))
	for i := 0; i < 4; i++ {
		callAndResult(t, a, fmt.Sprintf("s%d", i), "bash", `{"command":"step"}`, strings.Repeat(fmt.Sprintf("step %d output line\n", i), 220))
		mustAppend(t, a, msg.Text(msg.RoleAssistant, "next"))
	}
	callAndResult(t, a, "c1", "woo_products_list", `{}`, productsJSON(42))
	view, err := a.Assemble(context.Background(), engine.AssembleRequest{})
	if err != nil {
		t.Fatal(err)
	}
	if view.Report.FreshFull != 0 || !strings.Contains(strings.Join(view.Report.Actions, ","), "fresh_output_previewed") {
		t.Fatalf("an output that would cross the aggressive threshold must be previewed (report %+v)", view.Report)
	}
}

func TestRepeatedReadsNormalizedAndInContext(t *testing.T) {
	e := openEngine(t, engine.DefaultPolicy(), manual())
	a := mustAgent(t, e, "s", "main")
	ctx := context.Background()
	mustAppend(t, a, msg.Text(msg.RoleUser, "go"))
	small := callAndResult(t, a, "c1", "bash", `{"command":"ls"}`, "a.go\nb.go\n")
	if _, err := a.Assemble(ctx, engine.AssembleRequest{}); err != nil {
		t.Fatal(err)
	}
	out := a.HandleTool(ctx, "memory_get_event", json.RawMessage(fmt.Sprintf(`{"id":%q}`, small.ID)))
	if !strings.Contains(out.Text, "already in your context in full") {
		t.Fatalf("reading an output that is in context in full should say so: %s", out.Text)
	}
	big := callAndResult(t, a, "c2", "woo_products_list", `{}`, productsJSON(300))
	mustAppend(t, a, msg.Text(msg.RoleAssistant, "ok"))
	if _, err := a.Assemble(ctx, engine.AssembleRequest{}); err != nil {
		t.Fatal(err)
	}
	first := memoryCall(t, a, "m1", "memory_get_event", map[string]any{"id": big.ID})
	if first.IsError || strings.Contains(first.Text, "already answered") {
		t.Fatalf("first read: %s", first.Text[:120])
	}
	if _, err := a.Assemble(ctx, engine.AssembleRequest{}); err != nil {
		t.Fatal(err)
	}
	again := a.HandleTool(ctx, "memory_get_event", json.RawMessage(fmt.Sprintf(`{"offset":0,"id":%q}`, big.ID)))
	if !strings.Contains(again.Text, "already answered") {
		t.Fatalf("the same request with default arguments spelled out should be recognized: %s", again.Text[:min(200, len(again.Text))])
	}
}

func TestPassiveAgentsDoNotSummarize(t *testing.T) {
	p := smallPolicy(8000)
	e := openEngine(t, p)
	a := mustAgent(t, e, "s", "passive")
	for i := 0; i < 60; i++ {
		callAndResult(t, a, fmt.Sprintf("c%d", i), "bash", `{"command":"step"}`, strings.Repeat(fmt.Sprintf("line %d of output\n", i), 40))
	}
	time.Sleep(200 * time.Millisecond)
	var n int
	if err := e.DB().R.QueryRow("SELECT COUNT(*) FROM jobs WHERE session_id='s'").Scan(&n); err != nil {
		t.Fatal(err)
	}
	if n != 0 {
		t.Fatalf("an agent that never assembled scheduled %d summary jobs", n)
	}
	p.Compaction.PassiveSummaries = true
	e2 := openEngine(t, p)
	b := mustAgent(t, e2, "s", "passive")
	for i := 0; i < 60; i++ {
		callAndResult(t, b, fmt.Sprintf("c%d", i), "bash", `{"command":"step"}`, strings.Repeat(fmt.Sprintf("line %d of output\n", i), 40))
	}
	if err := e2.DB().R.QueryRow("SELECT COUNT(*) FROM jobs WHERE session_id='s'").Scan(&n); err != nil {
		t.Fatal(err)
	}
	if n == 0 {
		t.Fatal("passive_summaries should restore background summaries for passive agents")
	}
}

type blockingSummarizer struct {
	mu      sync.Mutex
	started chan struct{}
	tags    []engine.JobTag
	inputs  []engine.SummarizeInput
}

func (b *blockingSummarizer) Summarize(ctx context.Context, in engine.SummarizeInput) (engine.SummarizeOutput, error) {
	tag, _ := engine.JobTagFrom(ctx)
	b.mu.Lock()
	b.tags = append(b.tags, tag)
	b.inputs = append(b.inputs, in)
	b.mu.Unlock()
	select {
	case b.started <- struct{}{}:
	default:
	}
	<-ctx.Done()
	return engine.SummarizeOutput{}, ctx.Err()
}

func TestCloseSessionCancelsJobs(t *testing.T) {
	p := smallPolicy(8000)
	p.Compaction.Workers = 1
	p.Compaction.MaxInflightAgent = 1
	p.Compaction.JobTimeout = engine.Duration(time.Minute)
	bs := &blockingSummarizer{started: make(chan struct{}, 1)}
	e := openEngine(t, p, withSummarizer(bs))
	a := mustAgent(t, e, "s", "main")
	ctx := context.Background()
	for i := 0; i < 80; i++ {
		callAndResult(t, a, fmt.Sprintf("c%d", i), "bash", `{"command":"step"}`, strings.Repeat(fmt.Sprintf("line %d of output\n", i), 30))
		if _, err := a.Assemble(ctx, engine.AssembleRequest{}); err != nil {
			t.Fatal(err)
		}
	}
	select {
	case <-bs.started:
	case <-time.After(5 * time.Second):
		t.Fatal("no summary job started")
	}
	n, err := e.CloseSession(ctx, "s")
	if err != nil {
		t.Fatal(err)
	}
	if n == 0 {
		t.Fatal("closing the session cancelled no jobs")
	}
	deadline := time.Now().Add(5 * time.Second)
	for {
		var open, cancelled int
		_ = e.DB().R.QueryRow("SELECT COUNT(*) FROM jobs WHERE session_id='s' AND state IN ('queued','running')").Scan(&open)
		_ = e.DB().R.QueryRow("SELECT COUNT(*) FROM jobs WHERE session_id='s' AND state='cancelled'").Scan(&cancelled)
		if open == 0 && cancelled > 0 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("jobs still open after close: %d open, %d cancelled", open, cancelled)
		}
		time.Sleep(20 * time.Millisecond)
	}
	bs.mu.Lock()
	defer bs.mu.Unlock()
	if len(bs.tags) == 0 || bs.tags[0].ID == "" || bs.tags[0].Attempt != 1 || bs.inputs[0].JobID != bs.tags[0].ID {
		t.Fatalf("summary request not tagged with its job and attempt: %+v", bs.tags)
	}
	if len(bs.tags) != 1 {
		t.Fatalf("a cancelled job was retried: %d attempts", len(bs.tags))
	}
}

func TestCoreToolsAndSummaryUse(t *testing.T) {
	e := openEngine(t, engine.DefaultPolicy(), manual())
	a := mustAgent(t, e, "s", "main")
	ctx := context.Background()
	mustAppend(t, a, msg.Text(msg.RoleUser, "hi"))
	names := func(c *engine.Context) string {
		var ns []string
		for _, t := range c.Tools {
			ns = append(ns, t.Name)
		}
		return strings.Join(ns, ",")
	}
	core, err := a.Assemble(ctx, engine.AssembleRequest{IncludeMemoryTools: true})
	if err != nil {
		t.Fatal(err)
	}
	if got := names(core); got != "memory_search,memory_query,memory_get_event,memory_get_artifact,memory_expand,memory_update_task" {
		t.Fatalf("core tool set: %s", got)
	}
	all, err := a.Assemble(ctx, engine.AssembleRequest{IncludeMemoryTools: true, AllMemoryTools: true})
	if err != nil {
		t.Fatal(err)
	}
	if len(all.Tools) != len(engine.MemoryTools()) {
		t.Fatalf("all tools: %s", names(all))
	}
	for _, n := range []string{"memory_stats", "memory_get_task_state", "memory_get_summary_lineage"} {
		if out := a.HandleTool(ctx, n, json.RawMessage(`{"id":"sm_X"}`)); strings.Contains(out.Text, "unknown memory tool") {
			t.Fatalf("%s must still be answered: %s", n, out.Text)
		}
	}
	st, err := a.Stats(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if st.SummariesShown != 0 || st.SummariesExpanded != 0 {
		t.Fatalf("no summaries exist yet: %+v", st)
	}
}
