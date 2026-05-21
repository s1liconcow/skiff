package main

import "testing"

func TestDefaultStateElectsMemberZeroPrimary(t *testing.T) {
	primary := defaultState(config{Mode: "primary-replica", Member: 0, Members: 3, Generation: 1})
	if primary.Role != "primary" || primary.Leader != 0 || primary.Lag != 0 {
		t.Fatalf("member 0 default state = %+v", primary)
	}
	replica := defaultState(config{Mode: "primary-replica", Member: 1, Members: 3, Generation: 1})
	if replica.Role != "replica" || replica.Leader != 0 || replica.Lag != 0 {
		t.Fatalf("member 1 default state = %+v", replica)
	}
}

func TestPromoteRequiresCaughtUpHealthyMember(t *testing.T) {
	state := defaultState(config{Mode: "primary-replica", Member: 1, Members: 3, Generation: 1})
	state.Lag = 10
	if err := promote(&state); err == nil {
		t.Fatal("promote succeeded with replica lag")
	}
	state.Lag = 0
	state.Failures["health"] = "down"
	if err := promote(&state); err == nil {
		t.Fatal("promote succeeded with active failure")
	}
	delete(state.Failures, "health")
	if err := promote(&state); err != nil {
		t.Fatalf("promote healthy caught-up member: %v", err)
	}
	if state.Role != "primary" || state.Leader != 1 {
		t.Fatalf("promoted state = %+v", state)
	}
}

func TestCatchUpClearsReplicaLagFailure(t *testing.T) {
	state := defaultState(config{Mode: "primary-replica", Member: 1, Members: 3, Generation: 1})
	state.Lag = 1 << 30
	state.Failures["replica_lag"] = "replica-lag-too-high"
	if err := catchUp(&state); err != nil {
		t.Fatal(err)
	}
	if state.Lag != 0 || state.Failures["replica_lag"] != "" {
		t.Fatalf("catch-up did not clear lag: %+v", state)
	}
}
