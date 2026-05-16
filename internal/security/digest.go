package security

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"

	"github.com/s1liconcow/skiff/internal/state/canonical"
)

const DigestAlgorithmSHA256 = "sha256"

func CanonicalDigest(v any) (string, error) {
	body, err := canonical.Marshal(v)
	if err != nil {
		return "", err
	}
	return DigestBytes(body), nil
}

func DigestBytes(body []byte) string {
	sum := sha256.Sum256(body)
	return DigestAlgorithmSHA256 + ":" + hex.EncodeToString(sum[:])
}

func IsSHA256Digest(value string) bool {
	const prefix = DigestAlgorithmSHA256 + ":"
	if len(value) != len(prefix)+sha256.Size*2 {
		return false
	}
	if value[:len(prefix)] != prefix {
		return false
	}
	for _, c := range value[len(prefix):] {
		if (c < '0' || c > '9') && (c < 'a' || c > 'f') {
			return false
		}
	}
	return true
}

func CanonicalJSONDigest(body []byte) (string, error) {
	value, err := decodeSingleJSONValue(body)
	if err != nil {
		return "", err
	}
	canonicalBody, err := canonical.Marshal(value)
	if err != nil {
		return "", err
	}
	return DigestBytes(canonicalBody), nil
}

func UnsignedJSONDigest(body []byte) (string, error) {
	value, err := decodeSingleJSONValue(body)
	if err != nil {
		return "", err
	}
	object, ok := value.(map[string]any)
	if !ok {
		return "", fmt.Errorf("signed object must be a JSON object")
	}
	delete(object, "digest")
	delete(object, "signatures")
	canonicalBody, err := canonical.Marshal(object)
	if err != nil {
		return "", err
	}
	return DigestBytes(canonicalBody), nil
}

func decodeSingleJSONValue(body []byte) (any, error) {
	dec := json.NewDecoder(bytes.NewReader(body))
	dec.UseNumber()
	var value any
	if err := dec.Decode(&value); err != nil {
		return nil, err
	}
	var extra any
	if err := dec.Decode(&extra); err != io.EOF {
		if err == nil {
			return nil, fmt.Errorf("canonical JSON: multiple JSON values")
		}
		return nil, err
	}
	return value, nil
}
