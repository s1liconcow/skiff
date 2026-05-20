package sagaapi

import (
	"encoding/json"
)

const ProfileSchemaVersion = "skiff.operation-profile/v1alpha1"

type ProfileKind string

const (
	ProfileOrderedInPlaceUpdate         ProfileKind = "ordered_in_place_update"
	ProfileReplaceMemberMoveVolume      ProfileKind = "replace_member_move_volume"
	ProfilePrimarySwitchoverUpdate      ProfileKind = "primary_switchover_update"
	ProfilePartitionQuorumRollingUpdate ProfileKind = "partition_quorum_rolling_update"
	ProfileRaftGroupRollingUpdate       ProfileKind = "raft_group_rolling_update"
	ProfileSlotAwareFailoverUpdate      ProfileKind = "slot_aware_failover_update"
	ProfileShardAllocationRollingUpdate ProfileKind = "shard_allocation_rolling_update"
)

type ParamType string

const (
	ParamString  ParamType = "string"
	ParamBoolean ParamType = "boolean"
	ParamInteger ParamType = "integer"
	ParamNumber  ParamType = "number"
	ParamObject  ParamType = "object"
	ParamArray   ParamType = "array"
)

type Risk string

const (
	RiskLow      Risk = "low"
	RiskMedium   Risk = "medium"
	RiskHigh     Risk = "high"
	RiskCritical Risk = "critical"
)

type Reversibility string

const (
	Reversible          Reversibility = "reversible"
	Compensatable       Reversibility = "compensatable"
	PartiallyReversible Reversibility = "partially_reversible"
	Irreversible        Reversibility = "irreversible"
)

type OperationProfile struct {
	SchemaVersion        string                     `json:"schema_version"`
	Name                 string                     `json:"name"`
	Kind                 ProfileKind                `json:"kind"`
	TargetKinds          []string                   `json:"target_kinds"`
	Summary              string                     `json:"summary"`
	Params               map[string]ParamSchema     `json:"params,omitempty"`
	Defaults             map[string]json.RawMessage `json:"defaults,omitempty"`
	Risk                 Risk                       `json:"risk"`
	Reversibility        Reversibility              `json:"reversibility"`
	RequiredCapabilities []string                   `json:"required_capabilities,omitempty"`
	GraphTemplate        GraphTemplate              `json:"graph_template"`
}

type ParamSchema struct {
	Type       ParamType              `json:"type"`
	Required   bool                   `json:"required,omitempty"`
	Default    json.RawMessage        `json:"default,omitempty"`
	Enum       []string               `json:"enum,omitempty"`
	Summary    string                 `json:"summary,omitempty"`
	Secret     bool                   `json:"secret,omitempty"`
	Properties map[string]ParamSchema `json:"properties,omitempty"`
	Items      *ParamSchema           `json:"items,omitempty"`
}

type GraphTemplate struct {
	Nodes []NodeTemplate `json:"nodes"`
	Edges []EdgeTemplate `json:"edges,omitempty"`
}

type NodeTemplate struct {
	ID            string                `json:"id"`
	Kind          string                `json:"kind"`
	Requires      []string              `json:"requires,omitempty"`
	Params        json.RawMessage       `json:"params,omitempty"`
	Retry         *RetryPolicy          `json:"retry,omitempty"`
	Compensate    *CompensationTemplate `json:"compensate,omitempty"`
	Risk          Risk                  `json:"risk,omitempty"`
	Reversibility Reversibility         `json:"reversibility,omitempty"`
}

type RetryPolicy struct {
	MaxAttempts int    `json:"max_attempts,omitempty"`
	Backoff     string `json:"backoff,omitempty"`
}

type CompensationTemplate struct {
	Kind   string          `json:"kind"`
	Params json.RawMessage `json:"params,omitempty"`
}

type EdgeTemplate struct {
	From string `json:"from"`
	To   string `json:"to"`
}
