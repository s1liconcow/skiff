package spec

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"os"
)

type DecodeOptions struct {
	AllowUnknownFields bool
}

func LoadFile(path string, opts DecodeOptions) (*Document, error) {
	body, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	return Decode(body, opts)
}

func Decode(body []byte, opts DecodeOptions) (*Document, error) {
	trimmed := bytes.TrimSpace(body)
	if len(trimmed) == 0 {
		return nil, fmt.Errorf("spec is empty")
	}
	if trimmed[0] == '{' {
		return decodeJSON(trimmed, opts)
	}
	converted, err := parseYAMLSubset(trimmed)
	if err != nil {
		return nil, err
	}
	jsonBody, err := json.Marshal(converted)
	if err != nil {
		return nil, err
	}
	return decodeJSON(jsonBody, opts)
}

func decodeJSON(body []byte, opts DecodeOptions) (*Document, error) {
	var doc Document
	dec := json.NewDecoder(bytes.NewReader(body))
	if !opts.AllowUnknownFields {
		dec.DisallowUnknownFields()
	}
	if err := dec.Decode(&doc); err != nil {
		return nil, fmt.Errorf("decode spec: %w", err)
	}
	var extra any
	if err := dec.Decode(&extra); err != io.EOF {
		return nil, fmt.Errorf("decode spec: multiple JSON documents are not supported")
	}
	ApplyDefaults(&doc)
	return &doc, nil
}

func Parse(body []byte, opts DecodeOptions) (*Document, Result, error) {
	doc, err := Decode(body, opts)
	if err != nil {
		return nil, Result{OK: false}, err
	}
	result := Validate(*doc)
	return doc, result, nil
}
