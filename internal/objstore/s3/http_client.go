package s3store

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/xml"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"time"

	skiffaws "github.com/s1liconcow/skiff/internal/aws"
)

type HTTPClientOptions struct {
	Region         string
	Endpoint       string
	ForcePathStyle bool
	Credentials    skiffaws.Credentials
	HTTPClient     *http.Client
	Logger         Logger
	Clock          func() time.Time
}

type HTTPClient struct {
	region         string
	endpoint       string
	forcePathStyle bool
	credentials    skiffaws.Credentials
	httpClient     *http.Client
	logger         Logger
	clock          func() time.Time
}

func NewHTTPClient(opts HTTPClientOptions) (*HTTPClient, error) {
	if strings.TrimSpace(opts.Region) == "" {
		return nil, errors.New("s3 http client region is required")
	}
	if err := opts.Credentials.Validate(); err != nil {
		return nil, fmt.Errorf("s3 http client credentials invalid: %w", err)
	}
	httpClient := opts.HTTPClient
	if httpClient == nil {
		httpClient = http.DefaultClient
	}
	clock := opts.Clock
	if clock == nil {
		clock = func() time.Time { return time.Now().UTC() }
	}
	return &HTTPClient{
		region:         opts.Region,
		endpoint:       opts.Endpoint,
		forcePathStyle: opts.ForcePathStyle,
		credentials:    opts.Credentials,
		httpClient:     httpClient,
		logger:         opts.Logger,
		clock:          clock,
	}, nil
}

func (c *HTTPClient) GetObject(ctx context.Context, input GetObjectInput) (*GetObjectOutput, error) {
	if err := validateBucketAndKey(input.Bucket, input.Key); err != nil {
		return nil, err
	}
	logRequest(c.logger, ctx, http.MethodGet, input.Bucket, input.Key)
	req, err := c.newObjectRequest(ctx, http.MethodGet, input.Bucket, input.Key, nil, nil)
	if err != nil {
		return nil, err
	}
	resp, err := c.do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		return nil, responseError(resp)
	}
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}
	return &GetObjectOutput{
		Body:          body,
		ETag:          resp.Header.Get("ETag"),
		VersionID:     resp.Header.Get("x-amz-version-id"),
		ContentType:   resp.Header.Get("Content-Type"),
		Metadata:      metadataFromHeaders(resp.Header),
		LastModified:  parseHTTPTime(resp.Header.Get("Last-Modified")),
		ContentLength: contentLength(resp.Header, len(body)),
	}, nil
}

func (c *HTTPClient) HeadObject(ctx context.Context, input HeadObjectInput) (*HeadObjectOutput, error) {
	if err := validateBucketAndKey(input.Bucket, input.Key); err != nil {
		return nil, err
	}
	logRequest(c.logger, ctx, http.MethodHead, input.Bucket, input.Key)
	req, err := c.newObjectRequest(ctx, http.MethodHead, input.Bucket, input.Key, nil, nil)
	if err != nil {
		return nil, err
	}
	resp, err := c.do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		return nil, responseError(resp)
	}
	return &HeadObjectOutput{
		ETag:          resp.Header.Get("ETag"),
		VersionID:     resp.Header.Get("x-amz-version-id"),
		ContentType:   resp.Header.Get("Content-Type"),
		Metadata:      metadataFromHeaders(resp.Header),
		LastModified:  parseHTTPTime(resp.Header.Get("Last-Modified")),
		ContentLength: contentLength(resp.Header, 0),
	}, nil
}

func (c *HTTPClient) PutObject(ctx context.Context, input PutObjectInput) (*PutObjectOutput, error) {
	if err := validateBucketAndKey(input.Bucket, input.Key); err != nil {
		return nil, err
	}
	logRequest(c.logger, ctx, http.MethodPut, input.Bucket, input.Key)
	headers := http.Header{}
	if input.ContentType != "" {
		headers.Set("Content-Type", input.ContentType)
	}
	if input.IfNoneMatch != "" {
		headers.Set("If-None-Match", input.IfNoneMatch)
	}
	if input.IfMatch != "" {
		headers.Set("If-Match", input.IfMatch)
	}
	if input.ServerSideEncryption != "" {
		headers.Set("x-amz-server-side-encryption", input.ServerSideEncryption)
	}
	if input.SSEKMSKeyID != "" {
		headers.Set("x-amz-server-side-encryption-aws-kms-key-id", input.SSEKMSKeyID)
	}
	for key, value := range input.Metadata {
		headers.Set("x-amz-meta-"+key, value)
	}

	req, err := c.newObjectRequest(ctx, http.MethodPut, input.Bucket, input.Key, input.Body, headers)
	if err != nil {
		return nil, err
	}
	resp, err := c.do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		return nil, responseError(resp)
	}
	return &PutObjectOutput{
		ETag:      resp.Header.Get("ETag"),
		VersionID: resp.Header.Get("x-amz-version-id"),
	}, nil
}

func (c *HTTPClient) ListObjectsV2(ctx context.Context, input ListObjectsV2Input) (*ListObjectsV2Output, error) {
	if strings.TrimSpace(input.Bucket) == "" {
		return nil, errors.New("s3 bucket is required")
	}
	logListRequest(c.logger, ctx, input.Bucket, input.Prefix)
	query := url.Values{}
	query.Set("list-type", "2")
	if input.Prefix != "" {
		query.Set("prefix", input.Prefix)
	}
	if input.MaxKeys > 0 {
		query.Set("max-keys", strconv.Itoa(int(input.MaxKeys)))
	}
	if input.ContinuationToken != "" {
		query.Set("continuation-token", input.ContinuationToken)
	}
	req, err := c.newBucketRequest(ctx, http.MethodGet, input.Bucket, query)
	if err != nil {
		return nil, err
	}
	resp, err := c.do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		return nil, responseError(resp)
	}

	var result listBucketResult
	if err := xml.NewDecoder(resp.Body).Decode(&result); err != nil {
		return nil, err
	}
	objects := make([]ObjectInfo, 0, len(result.Contents))
	for _, item := range result.Contents {
		objects = append(objects, ObjectInfo{
			Key:          item.Key,
			ETag:         item.ETag,
			LastModified: parseS3Time(item.LastModified),
			Size:         item.Size,
		})
	}
	sort.Slice(objects, func(i, j int) bool {
		return objects[i].Key < objects[j].Key
	})
	return &ListObjectsV2Output{
		Objects:               objects,
		IsTruncated:           result.IsTruncated,
		NextContinuationToken: result.NextContinuationToken,
	}, nil
}

func (c *HTTPClient) DeleteObject(ctx context.Context, bucket, key string) error {
	if err := validateBucketAndKey(bucket, key); err != nil {
		return err
	}
	logRequest(c.logger, ctx, http.MethodDelete, bucket, key)
	req, err := c.newObjectRequest(ctx, http.MethodDelete, bucket, key, nil, nil)
	if err != nil {
		return err
	}
	resp, err := c.do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		return responseError(resp)
	}
	return nil
}

func (c *HTTPClient) do(req *http.Request) (*http.Response, error) {
	return c.httpClient.Do(req)
}

func (c *HTTPClient) newObjectRequest(ctx context.Context, method, bucket, key string, body []byte, headers http.Header) (*http.Request, error) {
	u, err := c.objectURL(bucket, key)
	if err != nil {
		return nil, err
	}
	return c.newRequest(ctx, method, u, body, headers)
}

func (c *HTTPClient) newBucketRequest(ctx context.Context, method, bucket string, query url.Values) (*http.Request, error) {
	u, err := c.bucketURL(bucket)
	if err != nil {
		return nil, err
	}
	u.RawQuery = canonicalQueryString(query)
	return c.newRequest(ctx, method, u, nil, nil)
}

func (c *HTTPClient) newRequest(ctx context.Context, method string, u *url.URL, body []byte, headers http.Header) (*http.Request, error) {
	req, err := http.NewRequestWithContext(ctx, method, u.String(), bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	for key, values := range headers {
		for _, value := range values {
			req.Header.Add(key, value)
		}
	}
	hash := sha256.Sum256(body)
	payloadHash := hex.EncodeToString(hash[:])
	now := c.clock().UTC()
	req.Header.Set("x-amz-content-sha256", payloadHash)
	req.Header.Set("x-amz-date", now.Format(awsTimeFormat))
	if c.credentials.SessionToken != "" {
		req.Header.Set("x-amz-security-token", c.credentials.SessionToken)
	}
	if err := signRequest(req, c.credentials, c.region, "s3", payloadHash, now); err != nil {
		return nil, err
	}
	return req, nil
}

func (c *HTTPClient) objectURL(bucket, key string) (*url.URL, error) {
	u, err := c.bucketURL(bucket)
	if err != nil {
		return nil, err
	}
	u.Path = joinURLPath(u.Path, key)
	return u, nil
}

func (c *HTTPClient) bucketURL(bucket string) (*url.URL, error) {
	if c.endpoint == "" {
		if c.forcePathStyle {
			return &url.URL{
				Scheme: "https",
				Host:   "s3." + c.region + ".amazonaws.com",
				Path:   "/" + bucket,
			}, nil
		}
		return &url.URL{
			Scheme: "https",
			Host:   bucket + ".s3." + c.region + ".amazonaws.com",
			Path:   "/",
		}, nil
	}

	endpoint := c.endpoint
	if !strings.Contains(endpoint, "://") {
		endpoint = "https://" + endpoint
	}
	u, err := url.Parse(endpoint)
	if err != nil {
		return nil, err
	}
	if u.Scheme == "" || u.Host == "" {
		return nil, fmt.Errorf("invalid s3 endpoint %q", c.endpoint)
	}
	if c.forcePathStyle {
		u.Path = joinURLPath(u.Path, bucket)
		return u, nil
	}
	u.Host = bucket + "." + u.Host
	if u.Path == "" {
		u.Path = "/"
	}
	return u, nil
}

func joinURLPath(base, next string) string {
	base = strings.TrimRight(base, "/")
	if base == "" {
		return "/" + strings.TrimLeft(next, "/")
	}
	return base + "/" + strings.TrimLeft(next, "/")
}

func responseError(resp *http.Response) error {
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	apiErr := &APIError{StatusCode: resp.StatusCode}
	if len(body) > 0 {
		var parsed s3ErrorResponse
		if err := xml.Unmarshal(body, &parsed); err == nil {
			apiErr.Code = parsed.Code
			apiErr.Message = parsed.Message
		}
	}
	if apiErr.Code == "" {
		apiErr.Code = http.StatusText(resp.StatusCode)
	}
	if apiErr.Message == "" {
		apiErr.Message = strings.TrimSpace(string(body))
	}
	return apiErr
}

func parseHTTPTime(value string) time.Time {
	if value == "" {
		return time.Time{}
	}
	parsed, err := http.ParseTime(value)
	if err != nil {
		return time.Time{}
	}
	return parsed.UTC()
}

func parseS3Time(value string) time.Time {
	if value == "" {
		return time.Time{}
	}
	parsed, err := time.Parse(time.RFC3339, value)
	if err == nil {
		return parsed.UTC()
	}
	parsed, err = time.Parse("2006-01-02T15:04:05.000Z", value)
	if err == nil {
		return parsed.UTC()
	}
	return time.Time{}
}

func metadataFromHeaders(headers http.Header) map[string]string {
	metadata := map[string]string{}
	for key, values := range headers {
		lower := strings.ToLower(key)
		if !strings.HasPrefix(lower, "x-amz-meta-") {
			continue
		}
		name := strings.TrimPrefix(lower, "x-amz-meta-")
		if len(values) > 0 {
			metadata[name] = values[0]
		}
	}
	if len(metadata) == 0 {
		return nil
	}
	return metadata
}

func contentLength(headers http.Header, fallback int) int64 {
	value := headers.Get("Content-Length")
	if value == "" {
		return int64(fallback)
	}
	parsed, err := strconv.ParseInt(value, 10, 64)
	if err != nil {
		return int64(fallback)
	}
	return parsed
}

type s3ErrorResponse struct {
	Code    string `xml:"Code"`
	Message string `xml:"Message"`
}

type listBucketResult struct {
	Contents []struct {
		Key          string `xml:"Key"`
		LastModified string `xml:"LastModified"`
		ETag         string `xml:"ETag"`
		Size         int64  `xml:"Size"`
	} `xml:"Contents"`
	IsTruncated           bool   `xml:"IsTruncated"`
	NextContinuationToken string `xml:"NextContinuationToken"`
}
