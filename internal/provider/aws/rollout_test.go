package aws_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/s1liconcow/skiff/internal/config"
	"github.com/s1liconcow/skiff/internal/provider"
	"github.com/s1liconcow/skiff/internal/provider/aws"
)

func TestStartRolloutStartsASGInstanceRefresh(t *testing.T) {
	client := &fakeRolloutClient{start: &aws.InstanceRefresh{ID: "ir-123", Status: "Pending", StartedAt: rolloutTime}}
	p := newRolloutProvider(t, client)
	rollout, err := p.StartRollout(context.Background(), provider.RolloutRequest{
		Service:              "payments-api",
		Env:                  "prod",
		ReleaseID:            "rel_01J",
		OperationID:          "op_01J",
		MinHealthyPercentage: 95,
		InstanceWarmup:       120,
	})
	if err != nil {
		t.Fatalf("start rollout: %v", err)
	}
	if rollout.ProviderID != "ir-123" || rollout.ID != "op_01J" {
		t.Fatalf("unexpected rollout: %+v", rollout)
	}
	if client.startReq.AutoScalingGroupName != "skiff-prod-payments-api-asg" || client.startReq.MinHealthyPercentage != 95 || client.startReq.InstanceWarmup != 120 {
		t.Fatalf("unexpected start request: %+v", client.startReq)
	}
}

func TestWatchRolloutMapsInstanceRefreshStatuses(t *testing.T) {
	cases := map[string]string{
		"Pending":            "starting",
		"InProgress":         "rolling_out",
		"Successful":         "succeeded",
		"Failed":             "failed",
		"Cancelling":         "cancelled",
		"RollbackInProgress": "rolling_back",
	}
	for awsStatus, want := range cases {
		t.Run(awsStatus, func(t *testing.T) {
			client := &fakeRolloutClient{describe: &aws.InstanceRefresh{ID: "ir-123", Status: awsStatus, UpdatedAt: rolloutTime}}
			p := newRolloutProvider(t, client)
			status, err := p.WatchRollout(context.Background(), provider.WatchRolloutRequest{
				Service:    "payments-api",
				Env:        "prod",
				RolloutID:  "rollout-1",
				ProviderID: "ir-123",
			})
			if err != nil {
				t.Fatalf("watch rollout: %v", err)
			}
			if status.Status != want || status.ProviderID != "ir-123" {
				t.Fatalf("status = %+v, want %s", status, want)
			}
		})
	}
}

func TestRolloutRetriesThrottledClient(t *testing.T) {
	client := &fakeRolloutClient{throttleOnce: true, start: &aws.InstanceRefresh{ID: "ir-123", Status: "Pending"}}
	p := newRolloutProvider(t, client)
	if _, err := p.StartRollout(context.Background(), provider.RolloutRequest{Service: "payments-api", Env: "prod"}); err != nil {
		t.Fatalf("start should retry throttling: %v", err)
	}
	if client.startCalls < 2 {
		t.Fatalf("start calls = %d, want retry", client.startCalls)
	}
}

func newRolloutProvider(t *testing.T, client *fakeRolloutClient) *aws.Provider {
	t.Helper()
	p, err := aws.NewFromConfig(config.Config{Region: "us-west-2"}, aws.WithClients(aws.Clients{Rollouts: client}))
	if err != nil {
		t.Fatal(err)
	}
	return p
}

type fakeRolloutClient struct {
	start        *aws.InstanceRefresh
	describe     *aws.InstanceRefresh
	startReq     aws.StartInstanceRefreshRequest
	describeReq  aws.DescribeInstanceRefreshRequest
	throttleOnce bool
	startCalls   int
}

func (c *fakeRolloutClient) StartInstanceRefresh(ctx context.Context, req aws.StartInstanceRefreshRequest) (*aws.InstanceRefresh, error) {
	c.startCalls++
	c.startReq = req
	if c.throttleOnce {
		c.throttleOnce = false
		return nil, errors.New("Throttling: rate exceeded")
	}
	return c.start, nil
}

func (c *fakeRolloutClient) DescribeInstanceRefresh(ctx context.Context, req aws.DescribeInstanceRefreshRequest) (*aws.InstanceRefresh, error) {
	c.describeReq = req
	return c.describe, nil
}

var rolloutTime = time.Date(2026, 5, 16, 23, 0, 0, 0, time.UTC)
