package compiler

import (
	"context"
	"fmt"

	"github.com/s1liconcow/skiff/internal/ir"
	"github.com/s1liconcow/skiff/internal/spec"
)

type Options struct{}

func Compile(ctx context.Context, doc spec.Document, opts Options) (*ir.Graph, error) {
	select {
	case <-ctx.Done():
		return nil, ctx.Err()
	default:
	}

	spec.ApplyDefaults(&doc)
	result := spec.Validate(doc)
	if !result.OK {
		return nil, spec.ValidationError{Diagnostics: result.Diagnostics}
	}

	switch doc.Kind {
	case spec.KindService:
		return compileService(doc, opts), nil
	case spec.KindStack:
		return compileStack(doc, opts)
	default:
		return nil, fmt.Errorf("compile kind %q is not supported yet; Service and Stack specs compile to IR", doc.Kind)
	}
}
