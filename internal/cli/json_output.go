package cli

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"strconv"
	"strings"
)

const (
	formatJSON       = "json"
	formatJSONPretty = "json-pretty"
)

type prettyJSONWriter struct {
	dst   io.Writer
	color bool
	buf   []byte
}

func IsJSONFormat(format string) bool {
	return isJSONFormat(format)
}

func WriteJSONOutput(w io.Writer, format string, value any) error {
	return writeJSON(w, format, value)
}

func PrepareJSONPrettyOutput(args []string, defaultFormat string, defaultNoColor bool, stdout io.Writer) ([]string, string, io.Writer) {
	return prepareJSONPrettyOutput(args, defaultFormat, defaultNoColor, stdout)
}

func FlushJSONPrettyOutput(stdout io.Writer) {
	flushJSONPrettyOutput(stdout)
}

func isJSONFormat(format string) bool {
	return format == formatJSON || format == formatJSONPretty
}

func writeJSON(w io.Writer, format string, value any) error {
	switch format {
	case formatJSON:
		return json.NewEncoder(w).Encode(value)
	case formatJSONPretty:
		body, err := json.MarshalIndent(value, "", "  ")
		if err != nil {
			return err
		}
		_, err = fmt.Fprintln(w, highlightJSON(body))
		return err
	default:
		return fmt.Errorf("unsupported JSON output format %q", format)
	}
}

func prepareJSONPrettyOutput(args []string, defaultFormat string, defaultNoColor bool, stdout io.Writer) ([]string, string, io.Writer) {
	rewritten := append([]string(nil), args...)
	effectiveFormat := defaultFormat
	effectiveNoColor := defaultNoColor

	if defaultFormat == formatJSONPretty {
		defaultFormat = formatJSON
	}
	for i := 0; i < len(rewritten); i++ {
		arg := rewritten[i]
		if arg == "--" {
			break
		}
		if !strings.HasPrefix(arg, "-") || arg == "-" {
			continue
		}
		name, value, hasValue := splitFlag(arg)
		switch name {
		case "format":
			if !hasValue {
				if i+1 >= len(rewritten) {
					continue
				}
				i++
				value = rewritten[i]
				if value == formatJSONPretty {
					rewritten[i] = formatJSON
				}
			} else if value == formatJSONPretty {
				rewritten[i] = "--" + name + "=" + formatJSON
			}
			effectiveFormat = value
		case "no-color":
			enabled := true
			if hasValue {
				parsed, err := strconv.ParseBool(value)
				if err != nil {
					continue
				}
				enabled = parsed
			}
			effectiveNoColor = enabled
		}
	}
	if effectiveFormat == formatJSONPretty {
		stdout = &prettyJSONWriter{dst: stdout, color: !effectiveNoColor}
	}
	return rewritten, defaultFormat, stdout
}

func flushJSONPrettyOutput(stdout io.Writer) {
	if pretty, ok := stdout.(*prettyJSONWriter); ok {
		_ = pretty.Flush()
	}
}

func (w *prettyJSONWriter) Write(p []byte) (int, error) {
	w.buf = append(w.buf, p...)
	for {
		idx := bytes.IndexByte(w.buf, '\n')
		if idx < 0 {
			break
		}
		line := append([]byte(nil), w.buf[:idx]...)
		w.buf = w.buf[idx+1:]
		if err := w.writeLine(line, true); err != nil {
			return 0, err
		}
	}
	return len(p), nil
}

func (w *prettyJSONWriter) Flush() error {
	if len(w.buf) == 0 {
		return nil
	}
	line := append([]byte(nil), w.buf...)
	w.buf = nil
	return w.writeLine(line, false)
}

func (w *prettyJSONWriter) writeLine(line []byte, newline bool) error {
	var pretty bytes.Buffer
	trimmed := bytes.TrimSpace(line)
	if len(trimmed) > 0 && json.Indent(&pretty, trimmed, "", "  ") == nil {
		if w.color {
			_, err := fmt.Fprintln(w.dst, highlightJSON(pretty.Bytes()))
			return err
		}
		if _, err := w.dst.Write(pretty.Bytes()); err != nil {
			return err
		}
		if newline {
			_, err := fmt.Fprintln(w.dst)
			return err
		}
		return nil
	}
	if _, err := w.dst.Write(line); err != nil {
		return err
	}
	if newline {
		_, err := fmt.Fprintln(w.dst)
		return err
	}
	return nil
}

func highlightJSON(body []byte) string {
	const (
		reset       = "\x1b[0m"
		keyColor    = "\x1b[36m"
		stringColor = "\x1b[32m"
		numberColor = "\x1b[33m"
		boolColor   = "\x1b[35m"
		nullColor   = "\x1b[90m"
		punctColor  = "\x1b[2m"
	)

	var out strings.Builder
	out.Grow(len(body) + len(body)/5)
	for i := 0; i < len(body); {
		switch c := body[i]; {
		case c == '"':
			start := i
			i++
			escaped := false
			for i < len(body) {
				if escaped {
					escaped = false
					i++
					continue
				}
				if body[i] == '\\' {
					escaped = true
					i++
					continue
				}
				if body[i] == '"' {
					i++
					break
				}
				i++
			}
			color := stringColor
			if nextNonSpace(body, i) == ':' {
				color = keyColor
			}
			out.WriteString(color)
			out.Write(body[start:i])
			out.WriteString(reset)
		case c == '-' || (c >= '0' && c <= '9'):
			start := i
			i++
			for i < len(body) && strings.ContainsRune("0123456789.eE+-", rune(body[i])) {
				i++
			}
			out.WriteString(numberColor)
			out.Write(body[start:i])
			out.WriteString(reset)
		case bytes.HasPrefix(body[i:], []byte("true")):
			out.WriteString(boolColor)
			out.WriteString("true")
			out.WriteString(reset)
			i += len("true")
		case bytes.HasPrefix(body[i:], []byte("false")):
			out.WriteString(boolColor)
			out.WriteString("false")
			out.WriteString(reset)
			i += len("false")
		case bytes.HasPrefix(body[i:], []byte("null")):
			out.WriteString(nullColor)
			out.WriteString("null")
			out.WriteString(reset)
			i += len("null")
		case strings.ContainsRune("{}[]:,", rune(c)):
			out.WriteString(punctColor)
			out.WriteByte(c)
			out.WriteString(reset)
			i++
		default:
			out.WriteByte(c)
			i++
		}
	}
	return out.String()
}

func nextNonSpace(body []byte, start int) byte {
	for i := start; i < len(body); i++ {
		switch body[i] {
		case ' ', '\n', '\r', '\t':
			continue
		default:
			return body[i]
		}
	}
	return 0
}
