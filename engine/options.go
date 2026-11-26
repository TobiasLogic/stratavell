package engine

import (
	"encoding/json"
	"fmt"
	"time"
)

const ConfigVersion = 2

type Layout string

const (
	LayoutStableTail Layout = "stable-tail"
	LayoutSpec       Layout = "spec"
)

type MaskMode string

const (
	MaskEpoch   MaskMode = "epoch"
	MaskRolling MaskMode = "rolling"
	MaskOff     MaskMode = "off"
)

type Thresholds struct {
	Soft       float64 `json:"soft"`
	Aggressive float64 `json:"aggressive"`
	Emergency  float64 `json:"emergency"`
	Target     float64 `json:"target"`
}

type Budget struct {
	ContextWindow       int     `json:"context_window"`
	MaxOutputTokens     int     `json:"max_output_tokens"`
	ReasoningTokens     int     `json:"reasoning_tokens"`
	SafetyTokens        int     `json:"safety_tokens"`
	SafetyFraction      float64 `json:"safety_fraction"`
	ProviderOverhead    int     `json:"provider_overhead"`
	RecentFraction      float64 `json:"recent_fraction"`
	RetrievalFraction   float64 `json:"retrieval_fraction"`
	TaskStateFraction   float64 `json:"task_state_fraction"`
	ProtectedFraction   float64 `json:"protected_fraction"`
	SummaryFraction     float64 `json:"summary_fraction"`
	MinRecentTurns      int     `json:"min_recent_turns"`
	MaxObservationToken int     `json:"max_observation_tokens"`
}

type Masking struct {
	Mode           MaskMode `json:"mode"`
	RollingKeep    int      `json:"rolling_keep"`
	Dedupe         bool     `json:"dedupe"`
	UnchangedReads bool     `json:"unchanged_reads"`
	Externalize    bool     `json:"externalize"`
	MaskInputs     bool     `json:"mask_tool_inputs"`
	InputMaskMin   int      `json:"input_mask_min_tokens"`
	PreviewHead    int      `json:"preview_head_lines"`
	PreviewTail    int      `json:"preview_tail_lines"`
	PreviewLineMax int      `json:"preview_line_max_bytes"`
	DigestErrors   int      `json:"digest_max_errors"`
}

type Compaction struct {
	Enabled           bool     `json:"enabled"`
	Async             bool     `json:"async"`
	Workers           int      `json:"workers"`
	MaxQueued         int      `json:"max_queued"`
	MaxInflightAgent  int      `json:"max_inflight_per_agent"`
	LeafChunkTokens   int      `json:"leaf_chunk_tokens"`
	MinChunkTokens    int      `json:"min_chunk_tokens"`
	SummarizerObsMax  int      `json:"summarizer_observation_max_tokens"`
	JobTimeout        Duration `json:"job_timeout"`
	MaxAttempts       int      `json:"max_attempts"`
	RetryBackoff      Duration `json:"retry_backoff"`
	MinReduction      float64  `json:"min_reduction"`
	CondenseFanin     int      `json:"condense_fanin"`
	CondenseMaxNodes  int      `json:"condense_max_nodes"`
	PredictSafety     float64  `json:"predict_safety"`
	EmergencyWait     Duration `json:"emergency_wait"`
	UpgradePointers   bool     `json:"upgrade_pointers"`
	PromptVersion     string   `json:"prompt_version"`
	SummaryMaxTokens  int      `json:"summary_max_tokens"`
	SummaryRatio      float64  `json:"summary_ratio"`
	RatePerMinute     int      `json:"rate_per_minute"`
	TokensPerMinute   int      `json:"tokens_per_minute"`
	ForegroundReserve float64  `json:"foreground_reserve"`
	Emergency         bool     `json:"emergency"`
}

type Retrieval struct {
	Enabled           bool `json:"enabled"`
	AutoInject        bool `json:"auto_inject"`
	AutoInjectHits    int  `json:"auto_inject_hits"`
	MaxCallsPerTurn   int  `json:"max_calls_per_turn"`
	DedupWindowTurns  int  `json:"dedup_window_turns"`
	ChunkBytes        int  `json:"chunk_bytes"`
	IndexMaxBytes     int  `json:"index_max_bytes"`
	MaxResultTokens   int  `json:"max_result_tokens"`
	DefaultLimit      int  `json:"default_limit"`
	MaxLimit          int  `json:"max_limit"`
	ArtifactMaxLength int  `json:"artifact_max_length"`
}

type Cache struct {
	Layout      Layout `json:"layout"`
	Breakpoints int    `json:"breakpoints"`
}

type Policy struct {
	Version      int        `json:"config_version"`
	Thresholds   Thresholds `json:"thresholds"`
	Budget       Budget     `json:"budget"`
	Masking      Masking    `json:"masking"`
	Compaction   Compaction `json:"compaction"`
	Retrieval    Retrieval  `json:"retrieval"`
	Cache        Cache      `json:"cache"`
	TaskState    bool       `json:"task_state"`
	PinDirective bool       `json:"pin_user_directives"`
	PinMaxTokens int        `json:"pin_max_tokens"`
}

func DefaultPolicy() Policy {
	return Policy{
		Version:    ConfigVersion,
		Thresholds: Thresholds{Soft: 0.55, Aggressive: 0.70, Emergency: 0.85, Target: 0.40},
		Budget: Budget{
			ContextWindow:       200000,
			MaxOutputTokens:     16000,
			SafetyTokens:        2000,
			SafetyFraction:      0.02,
			ProviderOverhead:    300,
			RecentFraction:      0.20,
			RetrievalFraction:   0.08,
			TaskStateFraction:   0.04,
			ProtectedFraction:   0.15,
			SummaryFraction:     0.25,
			MinRecentTurns:      2,
			MaxObservationToken: 6000,
		},
		Masking: Masking{
			Mode:           MaskEpoch,
			RollingKeep:    10,
			Dedupe:         true,
			UnchangedReads: true,
			Externalize:    true,
			MaskInputs:     true,
			InputMaskMin:   400,
			PreviewHead:    40,
			PreviewTail:    30,
			PreviewLineMax: 240,
			DigestErrors:   12,
		},
		Compaction: Compaction{
			Enabled:          true,
			Async:            true,
			Workers:          2,
			MaxQueued:        64,
			MaxInflightAgent: 2,
			LeafChunkTokens:  16000,
			MinChunkTokens:   1500,
			SummarizerObsMax: 1500,
			JobTimeout:       Duration(90 * time.Second),
			MaxAttempts:      3,
			RetryBackoff:     Duration(2 * time.Second),
			MinReduction:     0.5,
			CondenseFanin:    6,
			CondenseMaxNodes: 12,
			PredictSafety:    1.5,
			UpgradePointers:  true,
			PromptVersion:    "leaf-v1",
			SummaryMaxTokens: 1200,
			SummaryRatio:     0.15,
			RatePerMinute:    30,
			TokensPerMinute:  400000,
			Emergency:        true,
		},
		Retrieval: Retrieval{
			Enabled:           true,
			AutoInject:        true,
			AutoInjectHits:    3,
			MaxCallsPerTurn:   8,
			DedupWindowTurns:  6,
			ChunkBytes:        1600,
			IndexMaxBytes:     2 << 20,
			MaxResultTokens:   6000,
			DefaultLimit:      8,
			MaxLimit:          25,
			ArtifactMaxLength: 32768,
		},
		Cache:        Cache{Layout: LayoutStableTail, Breakpoints: 4},
		TaskState:    true,
		PinDirective: true,
		PinMaxTokens: 800,
	}
}

func (p Policy) Validate() error {
	t := p.Thresholds
	if !(0 < t.Target && t.Target < t.Soft && t.Soft <= t.Aggressive && t.Aggressive <= t.Emergency && t.Emergency < 1) {
		return fmt.Errorf("engine: thresholds must satisfy 0 < target < soft <= aggressive <= emergency < 1, got %+v", t)
	}
	if p.Budget.ContextWindow <= 0 {
		return fmt.Errorf("engine: context window must be positive")
	}
	if p.EffectiveCapacity() < 1024 {
		return fmt.Errorf("engine: effective capacity %d is too small", p.EffectiveCapacity())
	}
	if p.Compaction.Workers < 0 || p.Compaction.MaxQueued < 0 {
		return fmt.Errorf("engine: negative worker or queue size")
	}
	return nil
}

func (p Policy) EffectiveCapacity() int {
	b := p.Budget
	safety := b.SafetyTokens
	if f := int(b.SafetyFraction * float64(b.ContextWindow)); f > safety {
		safety = f
	}
	return b.ContextWindow - b.MaxOutputTokens - b.ReasoningTokens - safety - b.ProviderOverhead
}

type legacyPolicyV1 struct {
	SoftThreshold       float64 `json:"soft_threshold"`
	AggressiveThreshold float64 `json:"aggressive_threshold"`
	EmergencyThreshold  float64 `json:"emergency_threshold"`
	ContextWindow       int     `json:"context_window"`
	MaxOutputTokens     int     `json:"max_output_tokens"`
	Workers             int     `json:"workers"`
	KeepRecent          float64 `json:"keep_recent_fraction"`
}

func ParsePolicy(data []byte) (Policy, error) {
	var probe struct {
		Version int `json:"config_version"`
	}
	if err := json.Unmarshal(data, &probe); err != nil {
		return Policy{}, err
	}
	p := DefaultPolicy()
	switch {
	case probe.Version == 0 || probe.Version == 1:
		var l legacyPolicyV1
		if err := json.Unmarshal(data, &l); err != nil {
			return Policy{}, err
		}
		if l.SoftThreshold > 0 {
			p.Thresholds.Soft = l.SoftThreshold
		}
		if l.AggressiveThreshold > 0 {
			p.Thresholds.Aggressive = l.AggressiveThreshold
		}
		if l.EmergencyThreshold > 0 {
			p.Thresholds.Emergency = l.EmergencyThreshold
		}
		if l.ContextWindow > 0 {
			p.Budget.ContextWindow = l.ContextWindow
		}
		if l.MaxOutputTokens > 0 {
			p.Budget.MaxOutputTokens = l.MaxOutputTokens
		}
		if l.Workers > 0 {
			p.Compaction.Workers = l.Workers
		}
		if l.KeepRecent > 0 {
			p.Budget.RecentFraction = l.KeepRecent
		}
	case probe.Version == ConfigVersion:
		if err := json.Unmarshal(data, &p); err != nil {
			return Policy{}, err
		}
	default:
		return Policy{}, fmt.Errorf("engine: config_version %d is newer than supported %d", probe.Version, ConfigVersion)
	}
	p.Version = ConfigVersion
	return p, p.Validate()
}

type Duration time.Duration

func (d Duration) D() time.Duration { return time.Duration(d) }

func (d Duration) MarshalJSON() ([]byte, error) {
	return json.Marshal(time.Duration(d).String())
}

func (d *Duration) UnmarshalJSON(b []byte) error {
	var s string
	if json.Unmarshal(b, &s) == nil {
		v, err := time.ParseDuration(s)
		if err != nil {
			return fmt.Errorf("engine: invalid duration %q: %w", s, err)
		}
		*d = Duration(v)
		return nil
	}
	var n int64
	if err := json.Unmarshal(b, &n); err != nil {
		return fmt.Errorf("engine: duration must be a string such as \"90s\" or an integer of nanoseconds")
	}
	*d = Duration(n)
	return nil
}
