import collections
import json
import re
import sqlite3
import sys

from stratavell import Client, Session, StratavellError, tool_result

ID = re.compile(r"\b(?:ev|sm)_[0-9A-Z]{26}\b")
TOKEN = re.compile(r"\b(?=[A-Za-z0-9]*[0-9])(?=[A-Za-z0-9]*[A-Za-z])[A-Za-z0-9]{8,40}\b")


def owner_map(db):
    m = {}
    for t in ("events", "summaries"):
        for i, s in db.execute(f"select id, session_id from {t}"):
            m[i] = s
    return m


class Caller:
    def __init__(self, agent, closed):
        self.agent = agent
        self.closed = closed
        self.calls = 0
        self.first = None

    def new_turn(self):
        a = assemble(self.agent, self.closed)
        if self.first is None:
            self.first = a
        self.calls = 0

    def tool(self, name, args):
        if self.first is None or self.calls >= 6:
            self.new_turn()
        self.calls += 1
        return self.agent.tool(name, args)


def unique_terms(db, caller, session, names, want=3):
    found = []
    seen = set()
    for (ev,) in db.execute("select id from events where session_id=? order by seq desc", (session,)):
        text = caller.tool("memory_get_event", {"id": ev}).get("text", "")
        body = text.split("\n", 1)[1] if "\n" in text else ""
        for tok in TOKEN.findall(body):
            if tok in seen or ID.search(tok) or any(tok.lower() in n for n in names):
                continue
            seen.add(tok)
            rows = db.execute("select distinct d.session_id from docs_fts join docs d on d.id = docs_fts.rowid where docs_fts match ?", ('"' + tok + '"',)).fetchall()
            if [r[0] for r in rows] == [session]:
                found.append(tok)
                if len(found) == want:
                    return found
    return found


def hits_only(text):
    return text.split("\n", 1)[1] if text.startswith("[memory_search") and "\n" in text else text


def leaks(text, me, owners, terms):
    foreign_ids = [i for i in ID.findall(text) if owners.get(i) not in (None, me)]
    foreign_terms = [t for t in terms if t.lower() in text.lower()]
    return foreign_ids, foreign_terms


def assemble(agent, closed):
    for _ in range(3):
        try:
            return agent.assemble(max_tokens=16384)
        except StratavellError as e:
            pending = re.findall(r"call[-_][A-Za-z0-9-]+", str(e)) if e.status == 409 else []
            if not pending:
                raise
            agent.append(*[tool_result(cid, "", "(no result was returned for this call)", exit_code=1) for cid in pending])
            closed.append(len(pending))
    return agent.assemble(max_tokens=16384)


def db_checks(db):
    q = {
        "summary sources in another session": """select count(*) from summary_sources ss join summaries s on s.id = ss.summary_id
            left join events e on ss.source_kind = 'event' and e.id = ss.source_id
            left join summaries s2 on ss.source_kind = 'summary' and s2.id = ss.source_id
            where coalesce(e.session_id, s2.session_id) is not s.session_id""",
        "jobs whose summary is in another session": "select count(*) from jobs j join summaries s on s.id = j.summary_id where s.session_id != j.session_id",
        "summaries whose job is in another session": "select count(*) from summaries s join jobs j on j.id = s.job_id where s.session_id != j.session_id",
        "blob references whose event is in another session": "select count(*) from blob_refs b join events e on e.id = b.event_id where e.session_id != b.session_id",
        "search documents whose event is in another session": "select count(*) from docs d join events e on d.ref_kind = 'event' and e.id = d.ref_id where e.session_id != d.session_id",
        "search documents whose summary is in another session": "select count(*) from docs d join summaries s on d.ref_kind = 'summary' and s.id = d.ref_id where s.session_id != d.session_id",
        "path records whose event is in another session": "select count(*) from paths p join events e on e.id = p.event_id where e.session_id != p.session_id",
        "symbol records whose event is in another session": "select count(*) from symbols y join events e on e.id = y.event_id where e.session_id != y.session_id",
        "task items whose source event is in another session": "select count(*) from task_items t join events e on e.id = t.source_event where e.session_id != t.session_id",
        "grants": "select count(*) from grants",
        "events not private": "select count(*) from events where scope != 'private'",
    }
    return {k: db.execute(v).fetchone()[0] for k, v in q.items()}


def main():
    db_path, url, out = sys.argv[1:4]
    db = sqlite3.connect(f"file:{db_path}?mode=ro", uri=True)
    c = Client(url, timeout=300)
    owners = owner_map(db)
    sessions = [r[0] for r in db.execute("select id from sessions order by created_at")]
    closed = []
    agents = {s: Caller(Session(c, s).agent("main"), closed) for s in sessions}
    probe = {}
    for s in sessions:
        ev = db.execute("select id from events where session_id=? order by seq desc limit 1", (s,)).fetchone()
        sm = db.execute("select id from summaries where session_id=? order by created_at desc limit 1", (s,)).fetchone()
        blob = db.execute("select blob_hash from blob_refs where session_id=? and blob_hash not in (select blob_hash from blob_refs where session_id != ?) limit 1", (s, s)).fetchone()
        probe[s] = {"event": ev and ev[0], "summary": sm and sm[0], "blob": blob and blob[0], "terms": unique_terms(db, agents[s], s, [x.lower() for x in sessions])}
    counts = collections.Counter()
    failures = []
    controls = collections.Counter()

    def expect_refused(me, other, kind, name, args):
        r = agents[me].tool(name, args)
        text = r.get("text", "")
        counts[kind] += 1
        if text.startswith(("retrieval budget", "this exact")):
            counts["inconclusive: budget or repeat notice"] += 1
        elif not (r.get("is_error") and "not found" in text):
            failures.append({"from": me, "target": other, "probe": kind})

    def expect_clean(me, kind, text, terms):
        fi, ft = leaks(text, me, owners, terms)
        counts[kind] += 1
        if fi or ft:
            failures.append({"from": me, "probe": kind, "foreign_ids": len(fi), "foreign_terms": len(ft)})

    for me in sessions:
        p = probe[me]
        if p["event"]:
            r = agents[me].tool("memory_get_event", {"id": p["event"], "offset": 0})
            controls["own event read"] += 1
            if not r.get("text", "").startswith("[memory_get_event"):
                failures.append({"from": me, "probe": "control: own event read refused"})
        if p["summary"]:
            r = agents[me].tool("memory_expand", {"id": p["summary"]})
            controls["own summary expanded"] += 1
            if not r.get("text", "").startswith("[memory_expand"):
                failures.append({"from": me, "probe": "control: own summary expand refused"})
        for t in p["terms"]:
            r = agents[me].tool("memory_search", {"query": t, "limit": 25})
            controls["own unique term found by search"] += 1
            if t.lower() not in hits_only(r.get("text", "")).lower():
                controls["own unique term not in search reply"] += 1
        others = [s for s in sessions if s != me]
        other_terms = [t for s in others for t in probe[s]["terms"]]
        for o in others:
            p = probe[o]
            if p["event"]:
                expect_refused(me, o, "memory_get_event with another session's event id", "memory_get_event", {"id": p["event"]})
                expect_refused(me, o, "memory_get_artifact with another session's event id", "memory_get_artifact", {"id": p["event"]})
                expect_refused(me, o, "memory_expand with another session's event id", "memory_expand", {"id": p["event"]})
                r = agents[me].tool("memory_search", {"query": p["event"]})
                expect_clean(me, "memory_search for another session's event id", hits_only(r.get("text", "")), [])
            if p["summary"]:
                expect_refused(me, o, "memory_expand with another session's summary id", "memory_expand", {"id": p["summary"]})
                expect_refused(me, o, "memory_get_summary_lineage with another session's summary id", "memory_get_summary_lineage", {"id": p["summary"]})
            if p["blob"]:
                expect_refused(me, o, "memory_get_artifact with a hash only another session holds", "memory_get_artifact", {"id": p["blob"]})
            for t in p["terms"]:
                r = agents[me].tool("memory_search", {"query": t, "limit": 25})
                expect_clean(me, "memory_search for a term only another session contains", hits_only(r.get("text", "")), [t])
        expect_clean(me, "assembled context", json.dumps(agents[me].first), other_terms)
        expect_clean(me, "memory_get_task_state", agents[me].tool("memory_get_task_state", {}).get("text", ""), other_terms)
        expect_clean(me, "memory_stats", agents[me].tool("memory_stats", {}).get("text", ""), other_terms)
    res = {
        "sessions": len(sessions),
        "sessions_with_unique_terms": sum(1 for s in sessions if probe[s]["terms"]),
        "unique_terms": sum(len(probe[s]["terms"]) for s in sessions),
        "sessions_with_summaries": sum(1 for s in sessions if probe[s]["summary"]),
        "sessions_with_unshared_blobs": sum(1 for s in sessions if probe[s]["blob"]),
        "sessions_with_a_pending_call_closed_before_assembly": len(closed),
        "controls": dict(controls),
        "probes": dict(counts),
        "probes_total": sum(counts.values()),
        "failures": failures,
        "database_checks": db_checks(db),
    }
    json.dump(res, open(out, "w"), indent=1)
    print(json.dumps({k: v for k, v in res.items() if k != "failures"}, indent=1), len(failures), "failures")


if __name__ == "__main__":
    main()
