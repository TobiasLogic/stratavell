package store

type migration struct {
	version int
	name    string
	sql     string
}

var migrations = []migration{
	{1, "initial", `
CREATE TABLE meta(
	key TEXT PRIMARY KEY,
	value TEXT NOT NULL
);

CREATE TABLE sessions(
	id TEXT PRIMARY KEY,
	project TEXT NOT NULL DEFAULT '',
	created_at INTEGER NOT NULL,
	config TEXT NOT NULL DEFAULT '{}',
	config_version INTEGER NOT NULL DEFAULT 1
);

CREATE TABLE agents(
	session_id TEXT NOT NULL REFERENCES sessions(id) ON DELETE CASCADE,
	id TEXT NOT NULL,
	parent_id TEXT,
	role TEXT NOT NULL DEFAULT '',
	created_at INTEGER NOT NULL,
	head_seq INTEGER NOT NULL DEFAULT 0,
	state TEXT NOT NULL DEFAULT 'active',
	PRIMARY KEY(session_id, id)
);

CREATE TABLE blobs(
	hash TEXT PRIMARY KEY,
	size INTEGER NOT NULL,
	codec TEXT NOT NULL,
	stored_size INTEGER NOT NULL,
	data BLOB NOT NULL,
	created_at INTEGER NOT NULL
);

CREATE TABLE events(
	id TEXT PRIMARY KEY,
	session_id TEXT NOT NULL,
	agent_id TEXT NOT NULL,
	seq INTEGER NOT NULL,
	parent_id TEXT,
	cause_id TEXT,
	ts INTEGER NOT NULL,
	role TEXT NOT NULL,
	content_type TEXT NOT NULL,
	tool_name TEXT,
	tool_call_id TEXT,
	group_id TEXT,
	exit_code INTEGER,
	is_error INTEGER NOT NULL DEFAULT 0,
	body BLOB,
	content_hash TEXT NOT NULL,
	tokens INTEGER NOT NULL,
	raw_bytes INTEGER NOT NULL,
	scope TEXT NOT NULL DEFAULT 'private',
	paths TEXT NOT NULL DEFAULT '[]',
	symbols TEXT NOT NULL DEFAULT '[]',
	meta TEXT NOT NULL DEFAULT '{}',
	protected INTEGER NOT NULL DEFAULT 0,
	indexed INTEGER NOT NULL DEFAULT 0,
	purged INTEGER NOT NULL DEFAULT 0,
	FOREIGN KEY(session_id, agent_id) REFERENCES agents(session_id, id) ON DELETE CASCADE,
	UNIQUE(session_id, agent_id, seq)
);
CREATE INDEX events_unindexed ON events(indexed) WHERE indexed = 0;
CREATE INDEX events_session_ts ON events(session_id, ts);
CREATE INDEX events_tool_call ON events(session_id, tool_call_id);

CREATE TABLE blob_refs(
	blob_hash TEXT NOT NULL REFERENCES blobs(hash),
	session_id TEXT NOT NULL,
	agent_id TEXT NOT NULL,
	event_id TEXT NOT NULL REFERENCES events(id) ON DELETE CASCADE,
	PRIMARY KEY(blob_hash, event_id)
);
CREATE INDEX blob_refs_session ON blob_refs(session_id, blob_hash);
CREATE INDEX blob_refs_event ON blob_refs(event_id);

CREATE TABLE summaries(
	id TEXT PRIMARY KEY,
	session_id TEXT NOT NULL,
	agent_id TEXT NOT NULL,
	level INTEGER NOT NULL,
	kind TEXT NOT NULL,
	seq_lo INTEGER NOT NULL,
	seq_hi INTEGER NOT NULL,
	text TEXT NOT NULL,
	facts TEXT NOT NULL DEFAULT '[]',
	src_tokens INTEGER NOT NULL,
	tokens INTEGER NOT NULL,
	content_hash TEXT NOT NULL,
	sources_hash TEXT NOT NULL,
	model TEXT NOT NULL DEFAULT '',
	prompt_version TEXT NOT NULL DEFAULT '',
	created_at INTEGER NOT NULL,
	validation TEXT NOT NULL,
	validation_note TEXT NOT NULL DEFAULT '',
	state TEXT NOT NULL DEFAULT 'pending',
	job_id TEXT,
	FOREIGN KEY(session_id, agent_id) REFERENCES agents(session_id, id) ON DELETE CASCADE
);
CREATE INDEX summaries_agent ON summaries(session_id, agent_id, seq_lo);

CREATE TABLE summary_sources(
	summary_id TEXT NOT NULL REFERENCES summaries(id) ON DELETE CASCADE,
	ord INTEGER NOT NULL,
	source_kind TEXT NOT NULL,
	source_id TEXT NOT NULL,
	PRIMARY KEY(summary_id, ord)
);
CREATE INDEX summary_sources_src ON summary_sources(source_id);

CREATE TABLE views(
	session_id TEXT NOT NULL,
	agent_id TEXT NOT NULL,
	generation INTEGER NOT NULL,
	created_at INTEGER NOT NULL,
	reason TEXT NOT NULL,
	frontier TEXT NOT NULL,
	boundary_seq INTEGER NOT NULL,
	mask_seq INTEGER NOT NULL,
	policy_version INTEGER NOT NULL,
	FOREIGN KEY(session_id, agent_id) REFERENCES agents(session_id, id) ON DELETE CASCADE,
	PRIMARY KEY(session_id, agent_id, generation)
);

CREATE TABLE jobs(
	id TEXT PRIMARY KEY,
	session_id TEXT NOT NULL,
	agent_id TEXT NOT NULL,
	kind TEXT NOT NULL,
	level INTEGER NOT NULL,
	base_generation INTEGER NOT NULL,
	seq_lo INTEGER NOT NULL,
	seq_hi INTEGER NOT NULL,
	sources TEXT NOT NULL,
	state TEXT NOT NULL,
	attempts INTEGER NOT NULL DEFAULT 0,
	created_at INTEGER NOT NULL,
	started_at INTEGER,
	finished_at INTEGER,
	error TEXT NOT NULL DEFAULT '',
	summary_id TEXT,
	FOREIGN KEY(session_id, agent_id) REFERENCES agents(session_id, id) ON DELETE CASCADE
);
CREATE INDEX jobs_state ON jobs(state);
CREATE UNIQUE INDEX jobs_dedupe ON jobs(session_id, agent_id, kind, level, seq_lo, seq_hi) WHERE state IN ('queued','running');

CREATE TABLE task_items(
	id TEXT PRIMARY KEY,
	session_id TEXT NOT NULL REFERENCES sessions(id) ON DELETE CASCADE,
	owner_agent TEXT NOT NULL,
	scope TEXT NOT NULL,
	kind TEXT NOT NULL,
	key TEXT NOT NULL,
	text TEXT NOT NULL,
	status TEXT NOT NULL,
	provenance TEXT NOT NULL,
	source_event TEXT,
	source_refs TEXT NOT NULL DEFAULT '[]',
	version INTEGER NOT NULL,
	supersedes TEXT,
	superseded_by TEXT,
	protected INTEGER NOT NULL DEFAULT 0,
	created_at INTEGER NOT NULL
);
CREATE INDEX task_items_live ON task_items(session_id, scope, owner_agent) WHERE superseded_by IS NULL;
CREATE INDEX task_items_key ON task_items(session_id, kind, key);
CREATE INDEX task_items_src ON task_items(source_event);

CREATE TABLE grants(
	session_id TEXT NOT NULL,
	agent_id TEXT NOT NULL,
	ref_id TEXT NOT NULL,
	granted_by TEXT NOT NULL,
	created_at INTEGER NOT NULL,
	PRIMARY KEY(session_id, agent_id, ref_id)
);

CREATE TABLE paths(
	session_id TEXT NOT NULL,
	agent_id TEXT NOT NULL,
	path TEXT NOT NULL,
	event_id TEXT NOT NULL REFERENCES events(id) ON DELETE CASCADE,
	seq INTEGER NOT NULL,
	op TEXT NOT NULL,
	content_hash TEXT NOT NULL DEFAULT ''
);
CREATE INDEX paths_lookup ON paths(session_id, path, seq);
CREATE INDEX paths_event ON paths(event_id);

CREATE TABLE symbols(
	session_id TEXT NOT NULL,
	agent_id TEXT NOT NULL,
	name TEXT NOT NULL,
	lname TEXT NOT NULL,
	event_id TEXT NOT NULL REFERENCES events(id) ON DELETE CASCADE,
	seq INTEGER NOT NULL
);
CREATE INDEX symbols_lookup ON symbols(session_id, lname);
CREATE INDEX symbols_event ON symbols(event_id);

CREATE TABLE docs(
	id INTEGER PRIMARY KEY,
	session_id TEXT NOT NULL,
	agent_id TEXT NOT NULL,
	scope TEXT NOT NULL,
	ref_kind TEXT NOT NULL,
	ref_id TEXT NOT NULL,
	chunk INTEGER NOT NULL,
	byte_off INTEGER NOT NULL,
	seq INTEGER NOT NULL,
	ts INTEGER NOT NULL,
	tool TEXT NOT NULL DEFAULT '',
	reliability INTEGER NOT NULL DEFAULT 0
);
CREATE INDEX docs_ref ON docs(ref_id);
CREATE INDEX docs_session ON docs(session_id, agent_id);

CREATE VIRTUAL TABLE docs_fts USING fts5(body, aux, sid, tokenize = 'porter unicode61 remove_diacritics 2');

CREATE TABLE vectors(
	doc_id INTEGER PRIMARY KEY REFERENCES docs(id) ON DELETE CASCADE,
	session_id TEXT NOT NULL,
	dim INTEGER NOT NULL,
	vec BLOB NOT NULL
);
CREATE INDEX vectors_session ON vectors(session_id);

CREATE TABLE metrics(
	session_id TEXT NOT NULL,
	agent_id TEXT NOT NULL,
	name TEXT NOT NULL,
	value REAL NOT NULL,
	updated_at INTEGER NOT NULL,
	PRIMARY KEY(session_id, agent_id, name)
);

CREATE TABLE deletions(
	id INTEGER PRIMARY KEY,
	session_id TEXT NOT NULL,
	scope TEXT NOT NULL,
	detail TEXT NOT NULL,
	created_at INTEGER NOT NULL
);
`},
}
