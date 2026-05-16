package aws_test

import (
	"reflect"
	"testing"

	"github.com/s1liconcow/skiff/internal/ir"
	"github.com/s1liconcow/skiff/internal/provider/aws"
)

func TestTagsFromMetaMergesAndSorts(t *testing.T) {
	tags := aws.TagsFromMeta(ir.ResourceMeta{
		Tags: map[string]string{
			ir.TagService: "payments-api",
			ir.TagManaged: "true",
		},
	}, map[string]string{
		aws.TagRelease: "rel_123",
		ir.TagEnv:      "prod",
	})

	want := []aws.Tag{
		{Key: ir.TagEnv, Value: "prod"},
		{Key: ir.TagManaged, Value: "true"},
		{Key: aws.TagRelease, Value: "rel_123"},
		{Key: ir.TagService, Value: "payments-api"},
	}
	if !reflect.DeepEqual(tags, want) {
		t.Fatalf("tags mismatch\nwant: %#v\n got: %#v", want, tags)
	}
}

func TestSkiffTagFiltersAreDeterministic(t *testing.T) {
	filters := aws.SkiffTagFilters("payments-api", "prod")
	want := []aws.TagFilter{
		{Key: ir.TagEnv, Value: "prod"},
		{Key: ir.TagGraph, Value: "service/prod/payments-api"},
		{Key: ir.TagManaged, Value: "true"},
		{Key: ir.TagService, Value: "payments-api"},
	}
	if !reflect.DeepEqual(filters, want) {
		t.Fatalf("filters mismatch\nwant: %#v\n got: %#v", want, filters)
	}
}
