package engine

import (
	"github.com/TobiasLogic/stratavell/internal/textproc"
	"github.com/TobiasLogic/stratavell/msg"
)

const inlineMax = 2048

type storedPart struct {
	msg.Part
	Blob      string `json:"blob,omitempty"`
	BlobField string `json:"blob_field,omitempty"`
	BlobSize  int    `json:"blob_size,omitempty"`
}

type storedMeta struct {
	Host     msg.Meta            `json:"host"`
	Digest   *textproc.LogDigest `json:"digest,omitempty"`
	TextHash string              `json:"text_hash,omitempty"`
	Gen      int64               `json:"gen"`
	FileHash string              `json:"file_hash,omitempty"`
	Preview  string              `json:"preview,omitempty"`
	Lines    int                 `json:"lines,omitempty"`
	CmdKey   string              `json:"cmd_key,omitempty"`
	Oversize bool                `json:"oversize,omitempty"`
	Shape    string              `json:"shape,omitempty"`
}

type Event struct {
	ID          string          `json:"id"`
	SessionID   string          `json:"session_id"`
	AgentID     string          `json:"agent_id"`
	Seq         int64           `json:"seq"`
	ParentID    string          `json:"parent_id,omitempty"`
	CauseID     string          `json:"cause_id,omitempty"`
	TS          int64           `json:"ts"`
	Role        msg.Role        `json:"role"`
	ContentType msg.ContentType `json:"content_type"`
	ToolName    string          `json:"tool_name,omitempty"`
	ToolCallID  string          `json:"tool_call_id,omitempty"`
	GroupID     string          `json:"group_id,omitempty"`
	ExitCode    *int            `json:"exit_code,omitempty"`
	IsError     bool            `json:"is_error,omitempty"`
	ContentHash string          `json:"content_hash"`
	Tokens      int             `json:"tokens"`
	RawBytes    int             `json:"raw_bytes"`
	Scope       string          `json:"scope"`
	Paths       []string        `json:"paths,omitempty"`
	Symbols     []string        `json:"symbols,omitempty"`
	Protected   bool            `json:"protected,omitempty"`
	Purged      bool            `json:"purged,omitempty"`
}

type EventRef struct {
	ID     string `json:"id"`
	Seq    int64  `json:"seq"`
	Tokens int    `json:"tokens"`
}

type item struct {
	ev       Event
	parts    []msg.Part
	meta     storedMeta
	callIDs  []string
	readPath string
	primary  []string
	escape   bool
}

func (it *item) isObservation() bool { return it.ev.Role == msg.RoleTool }

func (it *item) isUserText() bool { return it.ev.Role == msg.RoleUser }

type nodeRef struct {
	ID    string `json:"id"`
	Level int    `json:"level"`
	Lo    int64  `json:"lo"`
	Hi    int64  `json:"hi"`
}

type SummaryNode struct {
	ID            string      `json:"id"`
	SessionID     string      `json:"session_id"`
	AgentID       string      `json:"agent_id"`
	Level         int         `json:"level"`
	Kind          string      `json:"kind"`
	Lo            int64       `json:"seq_lo"`
	Hi            int64       `json:"seq_hi"`
	Text          string      `json:"text"`
	Facts         []Fact      `json:"facts"`
	SrcTokens     int         `json:"src_tokens"`
	Tokens        int         `json:"tokens"`
	ContentHash   string      `json:"content_hash"`
	SourcesHash   string      `json:"sources_hash"`
	Model         string      `json:"model"`
	PromptVersion string      `json:"prompt_version"`
	CreatedAt     int64       `json:"created_at"`
	Validation    string      `json:"validation"`
	Note          string      `json:"validation_note,omitempty"`
	Sources       []SourceRef `json:"sources"`
	JobID         string      `json:"job_id,omitempty"`
	State         string      `json:"state"`
}

type SourceRef struct {
	Kind string `json:"kind"`
	ID   string `json:"id"`
}

type Fact struct {
	Kind string   `json:"kind"`
	Text string   `json:"text"`
	Refs []string `json:"refs,omitempty"`
}

const (
	KindSemantic = "semantic"
	KindPointer  = "pointer"
	KindDeleted  = "deleted"

	ValidValid         = "valid"
	ValidDeterministic = "deterministic"
	ValidRejected      = "rejected"
	ValidStale         = "stale"
	ValidPending       = "pending"
)
