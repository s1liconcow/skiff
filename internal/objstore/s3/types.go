package s3store

import (
	"context"
	"time"
)

const (
	serverSideEncryptionAWSKMS = "aws:kms"
)

type Client interface {
	GetObject(ctx context.Context, input GetObjectInput) (*GetObjectOutput, error)
	HeadObject(ctx context.Context, input HeadObjectInput) (*HeadObjectOutput, error)
	PutObject(ctx context.Context, input PutObjectInput) (*PutObjectOutput, error)
	ListObjectsV2(ctx context.Context, input ListObjectsV2Input) (*ListObjectsV2Output, error)
}

type Logger interface {
	Printf(format string, args ...any)
}

type PutObjectInput struct {
	Bucket               string
	Key                  string
	Body                 []byte
	ContentType          string
	Metadata             map[string]string
	IfNoneMatch          string
	IfMatch              string
	ServerSideEncryption string
	SSEKMSKeyID          string
}

type PutObjectOutput struct {
	ETag      string
	VersionID string
}

type GetObjectInput struct {
	Bucket string
	Key    string
}

type GetObjectOutput struct {
	Body          []byte
	ETag          string
	VersionID     string
	ContentType   string
	Metadata      map[string]string
	LastModified  time.Time
	ContentLength int64
}

type HeadObjectInput struct {
	Bucket string
	Key    string
}

type HeadObjectOutput struct {
	ETag          string
	VersionID     string
	ContentType   string
	Metadata      map[string]string
	LastModified  time.Time
	ContentLength int64
}

type ListObjectsV2Input struct {
	Bucket            string
	Prefix            string
	MaxKeys           int32
	ContinuationToken string
}

type ListObjectsV2Output struct {
	Objects               []ObjectInfo
	IsTruncated           bool
	NextContinuationToken string
}

type ObjectInfo struct {
	Key          string
	ETag         string
	VersionID    string
	LastModified time.Time
	Size         int64
}
