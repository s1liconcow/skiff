package compiler

import (
	"context"
	"fmt"

	"github.com/s1liconcow/skiff/internal/ir"
	internalpackages "github.com/s1liconcow/skiff/internal/packages"
	"github.com/s1liconcow/skiff/internal/spec"
)

type Options struct {
	PackageLock                *internalpackages.LockFile
	PackageLockDigest          string
	PackageManifests           map[string]internalpackages.Manifest
	AllowUnsignedLocalPackages bool
}

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
	if doc.Kind == spec.KindStack && doc.Stack != nil && len(doc.Stack.Dependencies) > 0 {
		if diagnostics := internalpackages.ValidateStackLock(doc, opts.PackageLock, internalpackages.ValidationOptions{
			AllowUnsignedLocal: opts.AllowUnsignedLocalPackages,
		}); len(diagnostics) > 0 {
			return nil, spec.ValidationError{Diagnostics: diagnostics}
		}
	}

	switch doc.Kind {
	case spec.KindService:
		return compileService(doc, opts), nil
	case spec.KindStatefulGroup:
		return compileStatefulGroup(doc, opts), nil
	case spec.KindStack:
		return compileStack(doc, opts)
	case spec.KindMultiRegionStack:
		return compileMultiRegionStack(doc, opts)
	default:
		return nil, fmt.Errorf("compile kind %q is not supported yet; Service, StatefulGroup, Stack, and MultiRegionStack specs compile to IR", doc.Kind)
	}
}
