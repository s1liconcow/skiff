package kube

import (
	"bytes"
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
)

type Object struct {
	APIVersion string         `json:"apiVersion,omitempty"`
	Kind       string         `json:"kind"`
	Metadata   Metadata       `json:"metadata"`
	Raw        map[string]any `json:"raw"`
}

type Metadata struct {
	Name        string            `json:"name"`
	Namespace   string            `json:"namespace,omitempty"`
	Labels      map[string]string `json:"labels,omitempty"`
	Annotations map[string]string `json:"annotations,omitempty"`
}

func Parse(body []byte) ([]Object, error) {
	body = bytes.TrimSpace(body)
	if len(body) == 0 {
		return nil, fmt.Errorf("kubernetes manifest is empty")
	}
	var out []Object
	for _, doc := range splitDocuments(string(body)) {
		doc = strings.TrimSpace(doc)
		if doc == "" {
			continue
		}
		value, err := parseDocument([]byte(doc))
		if err != nil {
			return nil, err
		}
		objects, err := objectsFromValue(value)
		if err != nil {
			return nil, err
		}
		out = append(out, objects...)
	}
	if len(out) == 0 {
		return nil, fmt.Errorf("kubernetes manifest contains no objects")
	}
	return out, nil
}

func parseDocument(body []byte) (any, error) {
	trimmed := bytes.TrimSpace(body)
	if len(trimmed) == 0 {
		return nil, nil
	}
	if trimmed[0] == '{' || trimmed[0] == '[' {
		var value any
		dec := json.NewDecoder(bytes.NewReader(trimmed))
		dec.UseNumber()
		if err := dec.Decode(&value); err != nil {
			return nil, fmt.Errorf("decode Kubernetes JSON: %w", err)
		}
		return normalizeJSONNumbers(value), nil
	}
	return parseYAMLSubset(trimmed)
}

func objectsFromValue(value any) ([]Object, error) {
	switch typed := value.(type) {
	case []any:
		var out []Object
		for _, item := range typed {
			objects, err := objectsFromValue(item)
			if err != nil {
				return nil, err
			}
			out = append(out, objects...)
		}
		return out, nil
	case map[string]any:
		if kind, _ := typed["kind"].(string); kind == "List" {
			items, _ := typed["items"].([]any)
			var out []Object
			for _, item := range items {
				objects, err := objectsFromValue(item)
				if err != nil {
					return nil, err
				}
				out = append(out, objects...)
			}
			return out, nil
		}
		object, err := objectFromMap(typed)
		if err != nil {
			return nil, err
		}
		return []Object{object}, nil
	default:
		return nil, fmt.Errorf("kubernetes document must be an object or list")
	}
}

func objectFromMap(raw map[string]any) (Object, error) {
	kind, _ := raw["kind"].(string)
	if strings.TrimSpace(kind) == "" {
		return Object{}, fmt.Errorf("kubernetes object is missing kind")
	}
	metadata, _ := raw["metadata"].(map[string]any)
	name, _ := metadata["name"].(string)
	namespace, _ := metadata["namespace"].(string)
	return Object{
		APIVersion: stringAt(raw, "apiVersion"),
		Kind:       kind,
		Metadata: Metadata{
			Name:        name,
			Namespace:   namespace,
			Labels:      stringMap(metadata["labels"]),
			Annotations: stringMap(metadata["annotations"]),
		},
		Raw: raw,
	}, nil
}

func splitDocuments(input string) []string {
	var docs []string
	var b strings.Builder
	for _, line := range strings.Split(input, "\n") {
		if strings.TrimSpace(line) == "---" {
			docs = append(docs, b.String())
			b.Reset()
			continue
		}
		b.WriteString(line)
		b.WriteByte('\n')
	}
	docs = append(docs, b.String())
	return docs
}

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
		return map[string]any{}, nil
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
			return nil, fmt.Errorf("line %d: tabs are not supported in Kubernetes YAML imports", i+1)
		}
		trimmedLeft := strings.TrimLeft(raw, " ")
		text := strings.TrimSpace(stripYAMLComment(trimmedLeft))
		if text == "" || text == "---" {
			continue
		}
		out = append(out, yamlLine{indent: len(raw) - len(trimmedLeft), text: text, line: i + 1})
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

func normalizeJSONNumbers(value any) any {
	switch typed := value.(type) {
	case map[string]any:
		for key, item := range typed {
			typed[key] = normalizeJSONNumbers(item)
		}
		return typed
	case []any:
		for i, item := range typed {
			typed[i] = normalizeJSONNumbers(item)
		}
		return typed
	case json.Number:
		if i, err := typed.Int64(); err == nil {
			return int(i)
		}
		if f, err := typed.Float64(); err == nil {
			return f
		}
		return typed.String()
	default:
		return value
	}
}
