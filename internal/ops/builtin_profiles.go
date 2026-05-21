package ops

import (
	"encoding/json"

	"github.com/s1liconcow/skiff/pkg/sagaapi"
)

func BuiltInProfileKinds() []sagaapi.ProfileKind {
	return []sagaapi.ProfileKind{
		sagaapi.ProfileOrderedInPlaceUpdate,
		sagaapi.ProfileReplaceMemberMoveVolume,
		sagaapi.ProfilePrimarySwitchoverUpdate,
		sagaapi.ProfilePartitionQuorumRollingUpdate,
		sagaapi.ProfileRaftGroupRollingUpdate,
		sagaapi.ProfileSlotAwareFailoverUpdate,
		sagaapi.ProfileShardAllocationRollingUpdate,
	}
}

func BuiltInProfiles() []sagaapi.OperationProfile {
	kinds := BuiltInProfileKinds()
	profiles := make([]sagaapi.OperationProfile, 0, len(kinds))
	for _, kind := range kinds {
		profile, _ := BuiltInProfile(kind)
		profiles = append(profiles, profile)
	}
	return profiles
}

func BuiltInProfile(kind sagaapi.ProfileKind) (sagaapi.OperationProfile, bool) {
	switch kind {
	case sagaapi.ProfileOrderedInPlaceUpdate:
		return orderedInPlaceUpdateProfile(), true
	case sagaapi.ProfileReplaceMemberMoveVolume:
		return replaceMemberMoveVolumeProfile(), true
	case sagaapi.ProfilePrimarySwitchoverUpdate:
		return primarySwitchoverUpdateProfile(), true
	case sagaapi.ProfilePartitionQuorumRollingUpdate:
		return partitionQuorumRollingUpdateProfile(), true
	case sagaapi.ProfileRaftGroupRollingUpdate:
		return raftGroupRollingUpdateProfile(), true
	case sagaapi.ProfileSlotAwareFailoverUpdate:
		return slotAwareFailoverUpdateProfile(), true
	case sagaapi.ProfileShardAllocationRollingUpdate:
		return shardAllocationRollingUpdateProfile(), true
	default:
		return sagaapi.OperationProfile{}, false
	}
}

func orderedInPlaceUpdateProfile() sagaapi.OperationProfile {
	kind := sagaapi.ProfileOrderedInPlaceUpdate
	return sagaapi.OperationProfile{
		SchemaVersion: sagaapi.ProfileSchemaVersion,
		Name:          "ordered-in-place-update",
		Kind:          kind,
		TargetKinds:   []string{"StatefulGroup"},
		Summary:       "update members in a declared order while preserving the existing allocation",
		Params: mergeParamSchemas(commonReleaseParams(), map[string]sagaapi.ParamSchema{
			"member_order": {
				Type:    sagaapi.ParamArray,
				Default: rawJSONProfile([]int{}),
				Summary: "ordered member ordinals; empty means package default order",
			},
			"max_unavailable": {
				Type:    sagaapi.ParamInteger,
				Default: rawJSONProfile(1),
				Summary: "maximum unavailable members during the update",
			},
		}),
		Risk:                 sagaapi.RiskMedium,
		Reversibility:        sagaapi.Compensatable,
		RequiredCapabilities: []string{"package.steps", "stateful.health"},
		GraphTemplate: sagaapi.GraphTemplate{Nodes: sequentialProfileNodes(kind, []profileNodeSpec{
			{id: "verify_group_healthy", step: "package.ordered_in_place.verify_group_healthy"},
			{id: "update_members_in_order", step: "package.ordered_in_place.update_members", compensate: "package.ordered_in_place.reduce_harm"},
			{id: "verify_final_health", step: "package.ordered_in_place.verify_final_health"},
		}, "member_order", "max_unavailable")},
	}
}

func replaceMemberMoveVolumeProfile() sagaapi.OperationProfile {
	kind := sagaapi.ProfileReplaceMemberMoveVolume
	return sagaapi.OperationProfile{
		SchemaVersion: sagaapi.ProfileSchemaVersion,
		Name:          "replace-member-move-volume",
		Kind:          kind,
		TargetKinds:   []string{"StatefulGroup"},
		Summary:       "replace one stateful member by moving or reattaching its durable volume",
		Params: mergeParamSchemas(commonReleaseParams(), map[string]sagaapi.ParamSchema{
			"member": {
				Type:     sagaapi.ParamInteger,
				Required: true,
				Summary:  "member ordinal to replace",
			},
			"replacement": {
				Type:     sagaapi.ParamString,
				Required: true,
				Summary:  "replacement member or instance selected by the package",
			},
		}),
		Risk:                 sagaapi.RiskHigh,
		Reversibility:        sagaapi.PartiallyReversible,
		RequiredCapabilities: []string{"package.steps", "stateful.volume", "stateful.health"},
		GraphTemplate: sagaapi.GraphTemplate{Nodes: sequentialProfileNodes(kind, []profileNodeSpec{
			{id: "verify_replacement_safe", step: "package.replace_member.verify_safe"},
			{id: "drain_member", step: "package.replace_member.drain", compensate: "package.replace_member.resume_old_member"},
			{id: "move_volume", step: "package.replace_member.move_volume", risk: sagaapi.RiskHigh, reversibility: sagaapi.PartiallyReversible},
			{id: "start_replacement", step: "package.replace_member.start_replacement", compensate: "package.replace_member.stop_replacement"},
			{id: "verify_replacement", step: "package.replace_member.verify_replacement"},
		}, "member", "replacement")},
	}
}

func primarySwitchoverUpdateProfile() sagaapi.OperationProfile {
	kind := sagaapi.ProfilePrimarySwitchoverUpdate
	return sagaapi.OperationProfile{
		SchemaVersion: sagaapi.ProfileSchemaVersion,
		Name:          "primary-switchover-update",
		Kind:          kind,
		TargetKinds:   []string{"StatefulGroup"},
		Summary:       "move primary role to a caught-up candidate, update the old primary, and verify topology",
		Params: mergeParamSchemas(commonReleaseParams(), map[string]sagaapi.ParamSchema{
			"candidate": {
				Type:     sagaapi.ParamString,
				Required: true,
				Summary:  "candidate member that should temporarily or permanently hold the primary role",
			},
			"return_primary": {
				Type:    sagaapi.ParamBoolean,
				Default: rawJSONProfile(false),
				Summary: "return the primary role to the original member after update",
			},
			"member_admin_urls": {
				Type:    sagaapi.ParamObject,
				Default: rawJSONProfile(map[string]string{}),
				Summary: "member ordinal to admin URL map; normally omitted when provider DNS is routable",
			},
			"admin_url_template": {
				Type:    sagaapi.ParamString,
				Default: rawJSONProfile(""),
				Summary: "admin URL template using {target}, {service}, {env}, and {member}",
			},
		}),
		Risk:                 sagaapi.RiskHigh,
		Reversibility:        sagaapi.PartiallyReversible,
		RequiredCapabilities: []string{"package.steps", "stateful.role", "stateful.health"},
		GraphTemplate: sagaapi.GraphTemplate{Nodes: sequentialProfileNodes(kind, []profileNodeSpec{
			{id: "verify_cluster_healthy", step: "package.primary_switchover.verify_cluster_healthy"},
			{id: "verify_candidate_caught_up", step: "package.primary_switchover.verify_candidate_caught_up"},
			{id: "move_primary_to_candidate", step: "package.primary_switchover.move_primary", compensate: "package.primary_switchover.reduce_harm"},
			{id: "update_old_primary", step: "package.primary_switchover.update_old_primary", compensate: "package.primary_switchover.resume_old_primary"},
			{id: "verify_old_primary_caught_up", step: "package.primary_switchover.verify_old_primary_caught_up"},
			{id: "optional_failback", step: "package.primary_switchover.optional_failback", risk: sagaapi.RiskMedium},
			{id: "update_candidate", step: "package.primary_switchover.update_candidate"},
			{id: "verify_final_topology", step: "package.primary_switchover.verify_final_topology"},
		}, "candidate", "return_primary", "member_admin_urls", "admin_url_template")},
	}
}

func partitionQuorumRollingUpdateProfile() sagaapi.OperationProfile {
	kind := sagaapi.ProfilePartitionQuorumRollingUpdate
	return sagaapi.OperationProfile{
		SchemaVersion: sagaapi.ProfileSchemaVersion,
		Name:          "partition-quorum-rolling-update",
		Kind:          kind,
		TargetKinds:   []string{"StatefulGroup"},
		Summary:       "update partitioned replicas while preserving in-sync quorum for each partition",
		Params: mergeParamSchemas(commonReleaseParams(), map[string]sagaapi.ParamSchema{
			"partition_selector": {
				Type:    sagaapi.ParamObject,
				Default: rawJSONProfile(map[string]any{}),
				Summary: "package-specific partition selector",
			},
			"min_in_sync": {
				Type:    sagaapi.ParamInteger,
				Default: rawJSONProfile(2),
				Summary: "minimum in-sync replicas required before each partition update",
			},
		}),
		Risk:                 sagaapi.RiskHigh,
		Reversibility:        sagaapi.PartiallyReversible,
		RequiredCapabilities: []string{"package.steps", "partition.quorum", "stateful.health"},
		GraphTemplate: sagaapi.GraphTemplate{Nodes: sequentialProfileNodes(kind, []profileNodeSpec{
			{id: "verify_partition_quorum", step: "package.partition_quorum.verify_quorum"},
			{id: "update_non_leader_replicas", step: "package.partition_quorum.update_non_leaders", compensate: "package.partition_quorum.reduce_harm"},
			{id: "verify_isr_after_update", step: "package.partition_quorum.verify_in_sync_replicas"},
			{id: "move_partition_leaders", step: "package.partition_quorum.move_leaders", risk: sagaapi.RiskHigh},
			{id: "update_previous_leaders", step: "package.partition_quorum.update_previous_leaders"},
			{id: "verify_partition_quorum_final", step: "package.partition_quorum.verify_final"},
		}, "partition_selector", "min_in_sync")},
	}
}

func raftGroupRollingUpdateProfile() sagaapi.OperationProfile {
	kind := sagaapi.ProfileRaftGroupRollingUpdate
	return sagaapi.OperationProfile{
		SchemaVersion: sagaapi.ProfileSchemaVersion,
		Name:          "raft-group-rolling-update",
		Kind:          kind,
		TargetKinds:   []string{"StatefulGroup"},
		Summary:       "update RAFT members while preserving quorum and leader safety",
		Params: mergeParamSchemas(commonReleaseParams(), map[string]sagaapi.ParamSchema{
			"group_selector": {
				Type:    sagaapi.ParamObject,
				Default: rawJSONProfile(map[string]any{}),
				Summary: "package-specific RAFT group selector",
			},
			"leader_policy": {
				Type:    sagaapi.ParamString,
				Default: rawJSONProfile("transfer"),
				Enum:    []string{"transfer", "package_default"},
				Summary: "how leadership is handled during the update",
			},
		}),
		Risk:                 sagaapi.RiskHigh,
		Reversibility:        sagaapi.PartiallyReversible,
		RequiredCapabilities: []string{"package.steps", "raft.quorum", "stateful.health"},
		GraphTemplate: sagaapi.GraphTemplate{Nodes: sequentialProfileNodes(kind, []profileNodeSpec{
			{id: "verify_raft_quorum", step: "package.raft_group.verify_quorum"},
			{id: "update_followers", step: "package.raft_group.update_followers", compensate: "package.raft_group.reduce_harm"},
			{id: "verify_followers_caught_up", step: "package.raft_group.verify_followers_caught_up"},
			{id: "transfer_raft_leader", step: "package.raft_group.transfer_leader", risk: sagaapi.RiskHigh},
			{id: "update_previous_leader", step: "package.raft_group.update_previous_leader"},
			{id: "verify_final_quorum", step: "package.raft_group.verify_final_quorum"},
		}, "group_selector", "leader_policy")},
	}
}

func slotAwareFailoverUpdateProfile() sagaapi.OperationProfile {
	kind := sagaapi.ProfileSlotAwareFailoverUpdate
	return sagaapi.OperationProfile{
		SchemaVersion: sagaapi.ProfileSchemaVersion,
		Name:          "slot-aware-failover-update",
		Kind:          kind,
		TargetKinds:   []string{"StatefulGroup"},
		Summary:       "fail over and update members without dropping slot coverage",
		Params: mergeParamSchemas(commonReleaseParams(), map[string]sagaapi.ParamSchema{
			"slot_selector": {
				Type:    sagaapi.ParamObject,
				Default: rawJSONProfile(map[string]any{}),
				Summary: "package-specific slot selector",
			},
		}),
		Risk:                 sagaapi.RiskHigh,
		Reversibility:        sagaapi.PartiallyReversible,
		RequiredCapabilities: []string{"package.steps", "slot.coverage", "stateful.health"},
		GraphTemplate: sagaapi.GraphTemplate{Nodes: sequentialProfileNodes(kind, []profileNodeSpec{
			{id: "verify_slot_coverage", step: "package.slot_aware.verify_coverage"},
			{id: "failover_one_replica_per_slot", step: "package.slot_aware.failover_replicas", compensate: "package.slot_aware.reduce_harm"},
			{id: "verify_slot_health", step: "package.slot_aware.verify_slot_health"},
			{id: "update_remaining_slot_members", step: "package.slot_aware.update_remaining_members"},
			{id: "verify_slot_coverage_final", step: "package.slot_aware.verify_final_coverage"},
		}, "slot_selector")},
	}
}

func shardAllocationRollingUpdateProfile() sagaapi.OperationProfile {
	kind := sagaapi.ProfileShardAllocationRollingUpdate
	return sagaapi.OperationProfile{
		SchemaVersion: sagaapi.ProfileSchemaVersion,
		Name:          "shard-allocation-rolling-update",
		Kind:          kind,
		TargetKinds:   []string{"StatefulGroup"},
		Summary:       "update shard holders while keeping primary allocation and rebalance state visible",
		Params: mergeParamSchemas(commonReleaseParams(), map[string]sagaapi.ParamSchema{
			"shard_selector": {
				Type:    sagaapi.ParamObject,
				Default: rawJSONProfile(map[string]any{}),
				Summary: "package-specific shard selector",
			},
			"rebalance_timeout": {
				Type:    sagaapi.ParamString,
				Default: rawJSONProfile("30m"),
				Summary: "time budget for package-level shard rebalancing",
			},
		}),
		Risk:                 sagaapi.RiskHigh,
		Reversibility:        sagaapi.PartiallyReversible,
		RequiredCapabilities: []string{"package.steps", "shard.allocation", "stateful.health"},
		GraphTemplate: sagaapi.GraphTemplate{Nodes: sequentialProfileNodes(kind, []profileNodeSpec{
			{id: "verify_shard_allocation", step: "package.shard_allocation.verify_allocation"},
			{id: "relocate_primaries_or_hot_shards", step: "package.shard_allocation.relocate_primaries", risk: sagaapi.RiskHigh},
			{id: "update_shard_holders", step: "package.shard_allocation.update_holders", compensate: "package.shard_allocation.reduce_harm"},
			{id: "wait_for_rebalance", step: "package.shard_allocation.wait_for_rebalance"},
			{id: "verify_shard_allocation_final", step: "package.shard_allocation.verify_final"},
		}, "shard_selector", "rebalance_timeout")},
	}
}

type profileNodeSpec struct {
	id            string
	step          string
	risk          sagaapi.Risk
	reversibility sagaapi.Reversibility
	compensate    string
}

func commonReleaseParams() map[string]sagaapi.ParamSchema {
	return map[string]sagaapi.ParamSchema{
		"release_id": {
			Type:     sagaapi.ParamString,
			Required: true,
			Summary:  "release ID to apply through the operation profile",
		},
	}
}

func mergeParamSchemas(left, right map[string]sagaapi.ParamSchema) map[string]sagaapi.ParamSchema {
	out := make(map[string]sagaapi.ParamSchema, len(left)+len(right))
	for key, value := range left {
		out[key] = value
	}
	for key, value := range right {
		out[key] = value
	}
	return out
}

func sequentialProfileNodes(kind sagaapi.ProfileKind, specs []profileNodeSpec, extraParams ...string) []sagaapi.NodeTemplate {
	nodes := make([]sagaapi.NodeTemplate, 0, len(specs))
	for i, spec := range specs {
		params := profileStepParams(kind, extraParams...)
		node := sagaapi.NodeTemplate{
			ID:            spec.id,
			Kind:          spec.step,
			Params:        params,
			Risk:          spec.risk,
			Reversibility: spec.reversibility,
		}
		if i > 0 {
			node.Requires = []string{specs[i-1].id}
		}
		if spec.compensate != "" {
			node.Compensate = &sagaapi.CompensationTemplate{Kind: spec.compensate, Params: params}
		}
		nodes = append(nodes, node)
	}
	return nodes
}

func profileStepParams(kind sagaapi.ProfileKind, extraParams ...string) json.RawMessage {
	values := map[string]any{
		"profile_kind": string(kind),
		"release_id":   "${params.release_id}",
	}
	for _, name := range extraParams {
		values[name] = "${params." + name + "}"
	}
	return rawJSONProfile(values)
}

func rawJSONProfile(value any) json.RawMessage {
	body, err := json.Marshal(value)
	if err != nil {
		panic(err)
	}
	return body
}
