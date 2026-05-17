package status

import (
	"errors"
	"sort"

	"github.com/s1liconcow/skiff/internal/ir"
)

type MultiRegionStatusRequest struct {
	Graph        *ir.Graph
	Observations []RegionObservation
}

type RegionObservation struct {
	Region         string `json:"region"`
	ServiceHealth  string `json:"service_health,omitempty"`
	ReplicationLag string `json:"replication_lag,omitempty"`
	FreshAt        string `json:"fresh_at,omitempty"`
}

type MultiRegionStatus struct {
	Service       string               `json:"service"`
	Env           string               `json:"env,omitempty"`
	PrimaryRegion string               `json:"primary_region,omitempty"`
	TrafficHost   string               `json:"traffic_host,omitempty"`
	Regions       []MultiRegionRegion  `json:"regions"`
	Findings      []MultiRegionFinding `json:"findings,omitempty"`
}

type MultiRegionRegion struct {
	Region         string `json:"region"`
	ServiceHealth  string `json:"service_health"`
	Database       string `json:"database,omitempty"`
	DatabaseRole   string `json:"database_role"`
	ReplicationLag string `json:"replication_lag"`
	TrafficWeight  int    `json:"traffic_weight"`
	FreshAt        string `json:"fresh_at,omitempty"`
}

type MultiRegionFinding struct {
	Code    string `json:"code"`
	Region  string `json:"region,omitempty"`
	Summary string `json:"summary"`
}

func BuildMultiRegionStatus(req MultiRegionStatusRequest) (*MultiRegionStatus, error) {
	if req.Graph == nil {
		return nil, errors.New("graph is required")
	}
	status := &MultiRegionStatus{Service: req.Graph.Service, Env: req.Graph.Env}
	observed := map[string]RegionObservation{}
	for _, item := range req.Observations {
		if item.Region != "" {
			observed[item.Region] = item
		}
	}
	regions := map[string]*MultiRegionRegion{}
	ensure := func(region string) *MultiRegionRegion {
		if region == "" {
			region = "unknown"
		}
		if current := regions[region]; current != nil {
			return current
		}
		item := &MultiRegionRegion{
			Region:         region,
			ServiceHealth:  "unknown",
			DatabaseRole:   "unknown",
			ReplicationLag: "unknown",
		}
		regions[region] = item
		return item
	}
	for _, policy := range req.Graph.Resources.GlobalTraffic {
		status.PrimaryRegion = firstNonEmpty(status.PrimaryRegion, policy.PrimaryRegion)
		status.TrafficHost = firstNonEmpty(status.TrafficHost, policy.Host)
		for _, region := range policy.Regions {
			item := ensure(region.Region)
			item.TrafficWeight = region.Weight
		}
	}
	for _, asg := range req.Graph.Resources.AutoscalingGroups {
		region := asg.Meta.Tags[ir.TagRegion]
		item := ensure(region)
		if item.ServiceHealth == "unknown" {
			item.ServiceHealth = "configured"
		}
	}
	for _, db := range req.Graph.Resources.ManagedDatabases {
		region := firstNonEmpty(db.Region, db.Meta.Tags[ir.TagRegion])
		item := ensure(region)
		item.Database = firstNonEmpty(item.Database, db.Meta.LogicalID, db.Meta.Name)
		item.DatabaseRole = firstNonEmpty(db.Role, item.DatabaseRole)
		if db.Role == "primary" && item.ReplicationLag == "unknown" {
			item.ReplicationLag = "0s"
		}
	}
	for region, observation := range observed {
		item := ensure(region)
		item.ServiceHealth = firstNonEmpty(observation.ServiceHealth, item.ServiceHealth)
		item.ReplicationLag = firstNonEmpty(observation.ReplicationLag, item.ReplicationLag)
		item.FreshAt = observation.FreshAt
	}
	for _, item := range regions {
		if item.ServiceHealth == "unknown" {
			status.Findings = append(status.Findings, MultiRegionFinding{Code: "REGION_SERVICE_HEALTH_UNKNOWN", Region: item.Region, Summary: "service health has not been refreshed for region " + item.Region})
		}
		if item.DatabaseRole == "replica" && item.ReplicationLag == "unknown" {
			status.Findings = append(status.Findings, MultiRegionFinding{Code: "REPLICA_LAG_UNKNOWN", Region: item.Region, Summary: "replica lag has not been refreshed for region " + item.Region})
		}
		status.Regions = append(status.Regions, *item)
	}
	sort.Slice(status.Regions, func(i, j int) bool {
		if status.Regions[i].Region == status.PrimaryRegion {
			return true
		}
		if status.Regions[j].Region == status.PrimaryRegion {
			return false
		}
		return status.Regions[i].Region < status.Regions[j].Region
	})
	sort.Slice(status.Findings, func(i, j int) bool {
		if status.Findings[i].Code == status.Findings[j].Code {
			return status.Findings[i].Region < status.Findings[j].Region
		}
		return status.Findings[i].Code < status.Findings[j].Code
	})
	return status, nil
}
