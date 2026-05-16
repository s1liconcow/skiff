package spec

import (
	"encoding/json"
	"fmt"
	"sort"
	"strconv"
	"strings"
)

type yamlLine struct {
	indent int
	text   string
	line   int
}

func parseYAMLSubset(body []byte) (any, error) {
	lines, err := yamlLines(string(body))
	if err != nil {
		return nil, err
	}
	if len(lines) == 0 {
		return nil, fmt.Errorf("spec is empty")
	}
	value, next, err := parseYAMLBlock(lines, 0, lines[0].indent)
	if err != nil {
		return nil, err
	}
	if next != len(lines) {
		return nil, fmt.Errorf("line %d: unexpected content", lines[next].line)
	}
	return value, nil
}

func yamlLines(input string) ([]yamlLine, error) {
	rawLines := strings.Split(input, "\n")
	out := make([]yamlLine, 0, len(rawLines))
	for i, raw := range rawLines {
		if strings.TrimSpace(raw) == "" {
			continue
		}
		if strings.HasPrefix(raw, "\t") {
			return nil, fmt.Errorf("line %d: tabs are not supported in Skiff YAML specs", i+1)
		}
		trimmedLeft := strings.TrimLeft(raw, " ")
		indent := len(raw) - len(trimmedLeft)
		text := strings.TrimSpace(stripYAMLComment(trimmedLeft))
		if text == "" {
			continue
		}
		out = append(out, yamlLine{indent: indent, text: text, line: i + 1})
	}
	return out, nil
}

func parseYAMLBlock(lines []yamlLine, index, indent int) (any, int, error) {
	if index >= len(lines) {
		return map[string]any{}, index, nil
	}
	if lines[index].indent != indent {
		return nil, index, fmt.Errorf("line %d: expected indent %d, got %d", lines[index].line, indent, lines[index].indent)
	}
	if strings.HasPrefix(lines[index].text, "- ") || lines[index].text == "-" {
		return parseYAMLSeq(lines, index, indent)
	}
	return parseYAMLMap(lines, index, indent)
}

func parseYAMLMap(lines []yamlLine, index, indent int) (map[string]any, int, error) {
	out := make(map[string]any)
	for index < len(lines) {
		line := lines[index]
		if line.indent < indent {
			break
		}
		if line.indent > indent {
			return nil, index, fmt.Errorf("line %d: unexpected indent", line.line)
		}
		if strings.HasPrefix(line.text, "- ") || line.text == "-" {
			break
		}
		key, rest, ok := strings.Cut(line.text, ":")
		if !ok || strings.TrimSpace(key) == "" {
			return nil, index, fmt.Errorf("line %d: expected key: value", line.line)
		}
		key = strings.TrimSpace(key)
		rest = strings.TrimSpace(rest)
		if rest == "" {
			if index+1 < len(lines) && lines[index+1].indent > indent {
				child, next, err := parseYAMLBlock(lines, index+1, lines[index+1].indent)
				if err != nil {
					return nil, index, err
				}
				out[key] = child
				index = next
				continue
			}
			out[key] = map[string]any{}
			index++
			continue
		}
		out[key] = parseYAMLScalar(rest)
		index++
	}
	return out, index, nil
}

func parseYAMLSeq(lines []yamlLine, index, indent int) ([]any, int, error) {
	var out []any
	for index < len(lines) {
		line := lines[index]
		if line.indent < indent {
			break
		}
		if line.indent > indent {
			return nil, index, fmt.Errorf("line %d: unexpected indent", line.line)
		}
		if line.text != "-" && !strings.HasPrefix(line.text, "- ") {
			break
		}
		item := strings.TrimSpace(strings.TrimPrefix(line.text, "-"))
		if item == "" {
			if index+1 >= len(lines) || lines[index+1].indent <= indent {
				out = append(out, nil)
				index++
				continue
			}
			child, next, err := parseYAMLBlock(lines, index+1, lines[index+1].indent)
			if err != nil {
				return nil, index, err
			}
			out = append(out, child)
			index = next
			continue
		}
		if key, rest, ok := strings.Cut(item, ":"); ok && strings.TrimSpace(key) != "" {
			m := map[string]any{strings.TrimSpace(key): parseYAMLScalar(strings.TrimSpace(rest))}
			index++
			if index < len(lines) && lines[index].indent > indent {
				child, next, err := parseYAMLMap(lines, index, lines[index].indent)
				if err != nil {
					return nil, index, err
				}
				for childKey, childValue := range child {
					m[childKey] = childValue
				}
				index = next
			}
			out = append(out, m)
			continue
		}
		out = append(out, parseYAMLScalar(item))
		index++
	}
	return out, index, nil
}

func parseYAMLScalar(value string) any {
	value = strings.TrimSpace(value)
	if value == "" {
		return ""
	}
	if strings.HasPrefix(value, `"`) && strings.HasSuffix(value, `"`) {
		if unquoted, err := strconv.Unquote(value); err == nil {
			return unquoted
		}
	}
	if strings.HasPrefix(value, `'`) && strings.HasSuffix(value, `'`) && len(value) >= 2 {
		return strings.ReplaceAll(value[1:len(value)-1], "''", "'")
	}
	switch strings.ToLower(value) {
	case "true":
		return true
	case "false":
		return false
	case "null", "~":
		return nil
	}
	if n, err := strconv.Atoi(value); err == nil {
		return n
	}
	return value
}

func stripYAMLComment(value string) string {
	inSingle := false
	inDouble := false
	escaped := false
	for i, r := range value {
		switch {
		case escaped:
			escaped = false
		case r == '\\' && inDouble:
			escaped = true
		case r == '\'' && !inDouble:
			inSingle = !inSingle
		case r == '"' && !inSingle:
			inDouble = !inDouble
		case r == '#' && !inSingle && !inDouble:
			if i == 0 || value[i-1] == ' ' || value[i-1] == '\t' {
				return strings.TrimRight(value[:i], " ")
			}
		}
	}
	return value
}

func MarshalYAML(value any) ([]byte, error) {
	body, err := json.Marshal(value)
	if err != nil {
		return nil, err
	}
	var generic any
	if err := json.Unmarshal(body, &generic); err != nil {
		return nil, err
	}
	var b strings.Builder
	writeYAMLValue(&b, generic, 0)
	return []byte(b.String()), nil
}

func writeYAMLValue(b *strings.Builder, value any, indent int) {
	switch v := value.(type) {
	case map[string]any:
		keys := make([]string, 0, len(v))
		for key, child := range v {
			if child == nil || isEmptyYAMLValue(child) {
				continue
			}
			keys = append(keys, key)
		}
		sort.Strings(keys)
		for _, key := range keys {
			child := v[key]
			writeIndent(b, indent)
			if isScalarYAMLValue(child) {
				b.WriteString(key)
				b.WriteString(": ")
				b.WriteString(formatYAMLScalar(child))
				b.WriteByte('\n')
				continue
			}
			b.WriteString(key)
			b.WriteString(":\n")
			writeYAMLValue(b, child, indent+2)
		}
	case []any:
		for _, child := range v {
			writeIndent(b, indent)
			if isScalarYAMLValue(child) {
				b.WriteString("- ")
				b.WriteString(formatYAMLScalar(child))
				b.WriteByte('\n')
				continue
			}
			b.WriteString("-\n")
			writeYAMLValue(b, child, indent+2)
		}
	}
}

func isScalarYAMLValue(value any) bool {
	switch value.(type) {
	case string, bool, float64, int, nil:
		return true
	default:
		return false
	}
}

func isEmptyYAMLValue(value any) bool {
	switch v := value.(type) {
	case map[string]any:
		return len(v) == 0
	case []any:
		return len(v) == 0
	case string:
		return v == ""
	default:
		return false
	}
}

func formatYAMLScalar(value any) string {
	switch v := value.(type) {
	case nil:
		return "null"
	case bool:
		if v {
			return "true"
		}
		return "false"
	case float64:
		if v == float64(int64(v)) {
			return strconv.FormatInt(int64(v), 10)
		}
		return strconv.FormatFloat(v, 'f', -1, 64)
	case int:
		return strconv.Itoa(v)
	case string:
		if shouldQuoteYAMLString(v) {
			return strconv.Quote(v)
		}
		return v
	default:
		return fmt.Sprint(v)
	}
}

func shouldQuoteYAMLString(value string) bool {
	if value == "" || strings.TrimSpace(value) != value {
		return true
	}
	lower := strings.ToLower(value)
	switch lower {
	case "true", "false", "null", "~":
		return true
	}
	return strings.ContainsAny(value, "#\n\r\t") || strings.HasPrefix(value, "-") || strings.Contains(value, ": ")
}

func writeIndent(b *strings.Builder, indent int) {
	for i := 0; i < indent; i++ {
		b.WriteByte(' ')
	}
}
