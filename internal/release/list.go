package release

import (
	"context"
	"fmt"
	"sort"
	"strings"

	"github.com/s1liconcow/skiff/internal/objstore"
	"github.com/s1liconcow/skiff/internal/state/canonical"
	"github.com/s1liconcow/skiff/internal/state/paths"
	"github.com/s1liconcow/skiff/internal/state/schema"
)

type ManifestDocument struct {
	Key      string                 `json:"key"`
	Meta     objstore.ObjectMeta    `json:"meta"`
	Manifest schema.ReleaseManifest `json:"manifest"`
}

type ManifestListOptions struct {
	Service string
	Limit   int
}

func (m Manager) ListManifests(ctx context.Context, opts ManifestListOptions) ([]ManifestDocument, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if m.Store == nil {
		return nil, candidateError("RELEASE_LIST_INVALID", "object store is required", nil)
	}
	prefix, err := paths.ServiceReleasesPrefix(opts.Service)
	if err != nil {
		return nil, candidateError("RELEASE_LIST_INVALID", err.Error(), err)
	}
	metas, err := m.Store.List(ctx, prefix, objstore.ListOptions{})
	if err != nil {
		return nil, candidateError("RELEASE_LIST_FAILED", "release manifest objects could not be listed", err)
	}
	out := make([]ManifestDocument, 0)
	for _, meta := range metas {
		releaseID, ok := parseReleaseManifestKey(prefix, meta.Key)
		if !ok {
			continue
		}
		object, err := m.Store.Get(ctx, meta.Key)
		if err != nil {
			return nil, candidateError("RELEASE_LIST_FAILED", "release manifest object could not be read", err)
		}
		var manifest schema.ReleaseManifest
		if err := canonical.UnmarshalStrict(object.Body, &manifest); err != nil {
			return nil, candidateError("INVALID_RELEASE_MANIFEST", "release manifest object is not valid", err)
		}
		if manifest.Service != opts.Service {
			return nil, candidateError("INVALID_RELEASE_MANIFEST", fmt.Sprintf("release manifest %s names service %q, expected %q", meta.Key, manifest.Service, opts.Service), nil)
		}
		if manifest.ReleaseID != releaseID {
			return nil, candidateError("INVALID_RELEASE_MANIFEST", fmt.Sprintf("release manifest %s names release %q", meta.Key, manifest.ReleaseID), nil)
		}
		out = append(out, ManifestDocument{
			Key:      meta.Key,
			Meta:     meta,
			Manifest: manifest,
		})
	}
	sort.Slice(out, func(i, j int) bool {
		left := releaseSortTime(out[i])
		right := releaseSortTime(out[j])
		if left == right {
			return out[i].Manifest.ReleaseID < out[j].Manifest.ReleaseID
		}
		return left > right
	})
	if opts.Limit > 0 && opts.Limit < len(out) {
		out = out[:opts.Limit]
	}
	return out, nil
}

func parseReleaseManifestKey(prefix, key string) (string, bool) {
	if !strings.HasPrefix(key, prefix) || !strings.HasSuffix(key, "/release.json") {
		return "", false
	}
	rest := strings.TrimPrefix(key, prefix)
	parts := strings.Split(rest, "/")
	if len(parts) != 2 || parts[1] != "release.json" {
		return "", false
	}
	if err := paths.ValidateID("release", parts[0]); err != nil {
		return "", false
	}
	return parts[0], true
}

func releaseSortTime(doc ManifestDocument) string {
	if doc.Manifest.CreatedAt != "" {
		return doc.Manifest.CreatedAt
	}
	if !doc.Meta.UpdatedAt.IsZero() {
		return canonical.Time(doc.Meta.UpdatedAt)
	}
	return canonical.Time(doc.Meta.CreatedAt)
}
