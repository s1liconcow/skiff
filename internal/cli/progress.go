package cli

import (
	"fmt"
	"io"
)

type progressRenderer struct {
	w       io.Writer
	enabled bool
}

func newProgressRenderer(w io.Writer, format string, noColor bool) progressRenderer {
	_ = noColor
	return progressRenderer{w: w, enabled: format == "human" || format == "text"}
}

func (p progressRenderer) Step(format string, args ...any) {
	if !p.enabled || p.w == nil {
		return
	}
	fmt.Fprintf(p.w, format+"\n", args...)
}
