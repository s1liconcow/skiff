package canonical

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"time"
)

const ContentType = "application/json"

func Marshal(v any) ([]byte, error) {
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	if err := enc.Encode(v); err != nil {
		return nil, err
	}
	out := buf.Bytes()
	if len(out) > 0 && out[len(out)-1] == '\n' {
		out = out[:len(out)-1]
	}
	return append([]byte(nil), out...), nil
}

func MarshalString(v any) (string, error) {
	body, err := Marshal(v)
	if err != nil {
		return "", err
	}
	return string(body), nil
}

func UnmarshalStrict(body []byte, dst any) error {
	dec := json.NewDecoder(bytes.NewReader(body))
	dec.DisallowUnknownFields()
	dec.UseNumber()
	if err := dec.Decode(dst); err != nil {
		return err
	}
	var extra any
	if err := dec.Decode(&extra); err != io.EOF {
		if err == nil {
			return fmt.Errorf("canonical JSON: multiple JSON values")
		}
		return err
	}
	return nil
}

func Time(t time.Time) string {
	return t.UTC().Round(0).Format(time.RFC3339Nano)
}
