package aws

import (
	"sort"

	"github.com/s1liconcow/skiff/internal/ir"
)

const TagRelease = "skiff.dev/release"

type Tag struct {
	Key   string `json:"key"`
	Value string `json:"value"`
}

type TagFilter struct {
	Key   string `json:"key"`
	Value string `json:"value"`
}

func TagsFromMeta(meta ir.ResourceMeta, extra map[string]string) []Tag {
	return TagsFromMap(TagsMap(meta, extra))
}

func TagsMap(meta ir.ResourceMeta, extra map[string]string) map[string]string {
	out := make(map[string]string, len(meta.Tags)+len(extra))
	for key, value := range meta.Tags {
		out[key] = value
	}
	for key, value := range extra {
		out[key] = value
	}
	return out
}

func TagsFromMap(tags map[string]string) []Tag {
	if len(tags) == 0 {
		return nil
	}
	keys := make([]string, 0, len(tags))
	for key := range tags {
		keys = append(keys, key)
	}
	sort.Strings(keys)

	out := make([]Tag, 0, len(keys))
	for _, key := range keys {
		out = append(out, Tag{Key: key, Value: tags[key]})
	}
	return out
}

func SkiffTagFilters(service, env string) []TagFilter {
	return TagFiltersFromMap(ir.RequiredTags(service, env))
}

func TagFiltersFromMap(tags map[string]string) []TagFilter {
	if len(tags) == 0 {
		return nil
	}
	keys := make([]string, 0, len(tags))
	for key := range tags {
		keys = append(keys, key)
	}
	sort.Strings(keys)

	out := make([]TagFilter, 0, len(keys))
	for _, key := range keys {
		out = append(out, TagFilter{Key: key, Value: tags[key]})
	}
	return out
}
