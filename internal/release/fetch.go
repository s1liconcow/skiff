package release

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/s1liconcow/skiff/internal/objstore"
	"github.com/s1liconcow/skiff/internal/security/signing"
	"github.com/s1liconcow/skiff/internal/state/canonical"
	"github.com/s1liconcow/skiff/internal/state/paths"
	"github.com/s1liconcow/skiff/internal/state/schema"
)

const (
	CodeReleaseFetchInvalid       = "RELEASE_FETCH_INVALID"
	CodeReleaseNotFound           = "RELEASE_NOT_FOUND"
	CodeRuntimeManifestNotFound   = "RUNTIME_MANIFEST_NOT_FOUND"
	CodeInvalidReleaseManifest    = "INVALID_RELEASE_MANIFEST"
	CodeInvalidRuntimeManifest    = "INVALID_RUNTIME_MANIFEST"
	CodeRuntimeManifestKeyMissing = "RUNTIME_MANIFEST_KEY_REQUIRED"
	CodeReleaseVerifyFailed       = "RELEASE_VERIFY_FAILED"
)

type FetchOptions struct {
	Service               string
	Env                   string
	ReleaseID             string
	ReleaseKey            string
	Verifier              signing.Verifier
	Now                   time.Time
	RequireArtifactDigest bool
}

type FetchedRelease struct {
	ReleaseManifest       schema.ReleaseManifest `json:"release_manifest"`
	RuntimeManifest       schema.RuntimeManifest `json:"runtime_manifest"`
	ReleaseKey            string                 `json:"release_key"`
	RuntimeManifestKey    string                 `json:"runtime_manifest_key"`
	ReleaseObject         objstore.ObjectMeta    `json:"release_object"`
	RuntimeManifestObject objstore.ObjectMeta    `json:"runtime_manifest_object"`
	Verification          VerificationResult     `json:"verification"`
}

type FetchError struct {
	Code         string
	Summary      string
	Key          string
	Err          error
	Verification *VerificationResult
}

func (e *FetchError) Error() string {
	if e == nil {
		return "<nil>"
	}
	if e.Key != "" {
		return fmt.Sprintf("%s %q: %s", e.Code, e.Key, e.Summary)
	}
	return fmt.Sprintf("%s: %s", e.Code, e.Summary)
}

func (e *FetchError) Unwrap() error {
	if e == nil {
		return nil
	}
	return e.Err
}

func Fetch(ctx context.Context, store objstore.ObjectStore, opts FetchOptions) (*FetchedRelease, error) {
	if store == nil {
		return nil, &FetchError{Code: CodeReleaseFetchInvalid, Summary: "object store is required"}
	}
	releaseKey, err := releaseKeyFor(opts)
	if err != nil {
		return nil, &FetchError{Code: CodeReleaseFetchInvalid, Summary: err.Error(), Err: err}
	}

	manifest, releaseMeta, err := readReleaseObject(ctx, store, releaseKey)
	if err != nil {
		return nil, err
	}
	if manifest.RuntimeManifestKey == "" {
		return nil, &FetchError{
			Code:    CodeRuntimeManifestKeyMissing,
			Summary: "release manifest does not name a runtime manifest object",
			Key:     releaseKey,
		}
	}

	runtimeManifest, runtimeMeta, err := readRuntimeObject(ctx, store, manifest.RuntimeManifestKey)
	if err != nil {
		return nil, err
	}

	result := VerifyManifest(ctx, manifest, VerifyOptions{
		Service:               opts.Service,
		Env:                   opts.Env,
		ReleaseID:             opts.ReleaseID,
		RuntimeManifest:       &runtimeManifest,
		Verifier:              opts.Verifier,
		Now:                   opts.Now,
		RequireArtifactDigest: opts.RequireArtifactDigest,
	})
	if !result.OK {
		return nil, &FetchError{
			Code:         CodeReleaseVerifyFailed,
			Summary:      fmt.Sprintf("release %q failed verification", manifest.ReleaseID),
			Key:          releaseKey,
			Verification: &result,
		}
	}

	return &FetchedRelease{
		ReleaseManifest:       manifest,
		RuntimeManifest:       runtimeManifest,
		ReleaseKey:            releaseKey,
		RuntimeManifestKey:    manifest.RuntimeManifestKey,
		ReleaseObject:         releaseMeta,
		RuntimeManifestObject: runtimeMeta,
		Verification:          result,
	}, nil
}

func releaseKeyFor(opts FetchOptions) (string, error) {
	if opts.ReleaseKey != "" {
		return opts.ReleaseKey, nil
	}
	if opts.ReleaseID == "" {
		return "", errors.New("release ID is required when release key is not provided")
	}
	return paths.ReleaseManifest(opts.Service, opts.ReleaseID)
}

func readReleaseObject(ctx context.Context, store objstore.ObjectStore, key string) (schema.ReleaseManifest, objstore.ObjectMeta, error) {
	object, err := store.Get(ctx, key)
	if err != nil {
		code := CodeInvalidReleaseManifest
		summary := err.Error()
		if errors.Is(err, objstore.ErrNotFound) {
			code = CodeReleaseNotFound
			summary = "release manifest object was not found"
		}
		return schema.ReleaseManifest{}, objstore.ObjectMeta{}, &FetchError{Code: code, Summary: summary, Key: key, Err: err}
	}
	var manifest schema.ReleaseManifest
	if err := canonical.UnmarshalStrict(object.Body, &manifest); err != nil {
		return schema.ReleaseManifest{}, objstore.ObjectMeta{}, &FetchError{
			Code:    CodeInvalidReleaseManifest,
			Summary: err.Error(),
			Key:     key,
			Err:     err,
		}
	}
	return manifest, metaFromObject(object), nil
}

func readRuntimeObject(ctx context.Context, store objstore.ObjectStore, key string) (schema.RuntimeManifest, objstore.ObjectMeta, error) {
	object, err := store.Get(ctx, key)
	if err != nil {
		code := CodeInvalidRuntimeManifest
		summary := err.Error()
		if errors.Is(err, objstore.ErrNotFound) {
			code = CodeRuntimeManifestNotFound
			summary = "runtime manifest object was not found"
		}
		return schema.RuntimeManifest{}, objstore.ObjectMeta{}, &FetchError{Code: code, Summary: summary, Key: key, Err: err}
	}
	var manifest schema.RuntimeManifest
	if err := canonical.UnmarshalStrict(object.Body, &manifest); err != nil {
		return schema.RuntimeManifest{}, objstore.ObjectMeta{}, &FetchError{
			Code:    CodeInvalidRuntimeManifest,
			Summary: err.Error(),
			Key:     key,
			Err:     err,
		}
	}
	return manifest, metaFromObject(object), nil
}

func metaFromObject(object *objstore.Object) objstore.ObjectMeta {
	if object == nil {
		return objstore.ObjectMeta{}
	}
	return objstore.ObjectMeta{
		Key:         object.Key,
		ETag:        object.ETag,
		VersionID:   object.VersionID,
		Size:        object.Size,
		ContentType: object.ContentType,
		Metadata:    cloneMetadata(object.Metadata),
		CreatedAt:   object.CreatedAt,
		UpdatedAt:   object.UpdatedAt,
	}
}

func cloneMetadata(metadata map[string]string) map[string]string {
	if metadata == nil {
		return nil
	}
	out := make(map[string]string, len(metadata))
	for key, value := range metadata {
		out[key] = value
	}
	return out
}
