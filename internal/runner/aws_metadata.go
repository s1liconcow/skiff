package runner

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

const defaultEC2MetadataEndpoint = "http://169.254.169.254"

type EC2MetadataProvider struct {
	Endpoint string
	Client   *http.Client
}

type ec2IdentityDocument struct {
	AccountID        string `json:"accountId"`
	AvailabilityZone string `json:"availabilityZone"`
	InstanceID       string `json:"instanceId"`
	PrivateIP        string `json:"privateIp"`
	Region           string `json:"region"`
}

func (p EC2MetadataProvider) Identity(ctx context.Context) (Identity, error) {
	endpoint := strings.TrimRight(p.Endpoint, "/")
	if endpoint == "" {
		endpoint = defaultEC2MetadataEndpoint
	}
	client := p.Client
	if client == nil {
		client = &http.Client{Timeout: 2 * time.Second}
	}

	token, _ := fetchEC2MetadataToken(ctx, client, endpoint)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint+"/latest/dynamic/instance-identity/document", nil)
	if err != nil {
		return Identity{}, err
	}
	if token != "" {
		req.Header.Set("X-aws-ec2-metadata-token", token)
	}
	resp, err := client.Do(req)
	if err != nil {
		return Identity{}, err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return Identity{}, fmt.Errorf("ec2 instance identity document status %s", resp.Status)
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return Identity{}, err
	}
	var doc ec2IdentityDocument
	if err := json.Unmarshal(body, &doc); err != nil {
		return Identity{}, fmt.Errorf("parse ec2 instance identity document: %w", err)
	}
	return Identity{
		Provider:   "aws",
		Region:     doc.Region,
		InstanceID: doc.InstanceID,
		AccountID:  doc.AccountID,
		Zone:       doc.AvailabilityZone,
		PrivateIP:  doc.PrivateIP,
	}, nil
}

func fetchEC2MetadataToken(ctx context.Context, client *http.Client, endpoint string) (string, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodPut, endpoint+"/latest/api/token", nil)
	if err != nil {
		return "", err
	}
	req.Header.Set("X-aws-ec2-metadata-token-ttl-seconds", "21600")
	resp, err := client.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return "", fmt.Errorf("ec2 metadata token status %s", resp.Status)
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, 8192))
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(string(body)), nil
}
