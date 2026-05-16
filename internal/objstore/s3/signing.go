package s3store

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"net/http"
	"net/url"
	"sort"
	"strings"
	"time"

	skiffaws "github.com/s1liconcow/skiff/internal/aws"
)

const (
	awsDateFormat = "20060102"
	awsTimeFormat = "20060102T150405Z"
)

func signRequest(req *http.Request, credentials skiffaws.Credentials, region, service, payloadHash string, now time.Time) error {
	if err := credentials.Validate(); err != nil {
		return err
	}

	canonicalHeaders, signedHeaders := canonicalHeaders(req)
	canonicalRequest := strings.Join([]string{
		req.Method,
		canonicalURI(req.URL),
		canonicalQueryString(req.URL.Query()),
		canonicalHeaders,
		signedHeaders,
		payloadHash,
	}, "\n")

	date := now.UTC().Format(awsDateFormat)
	scope := strings.Join([]string{date, region, service, "aws4_request"}, "/")
	hashedCanonical := sha256Hex([]byte(canonicalRequest))
	stringToSign := strings.Join([]string{
		"AWS4-HMAC-SHA256",
		now.UTC().Format(awsTimeFormat),
		scope,
		hashedCanonical,
	}, "\n")

	signingKey := deriveSigningKey(credentials.SecretAccessKey, date, region, service)
	signature := hex.EncodeToString(hmacSHA256(signingKey, []byte(stringToSign)))
	req.Header.Set("Authorization", "AWS4-HMAC-SHA256 Credential="+credentials.AccessKeyID+"/"+scope+", SignedHeaders="+signedHeaders+", Signature="+signature)
	return nil
}

func canonicalHeaders(req *http.Request) (string, string) {
	values := map[string][]string{
		"host": {req.URL.Host},
	}
	for key, headerValues := range req.Header {
		lower := strings.ToLower(key)
		if lower == "authorization" {
			continue
		}
		for _, value := range headerValues {
			values[lower] = append(values[lower], normalizeHeaderValue(value))
		}
	}

	keys := make([]string, 0, len(values))
	for key := range values {
		keys = append(keys, key)
	}
	sort.Strings(keys)

	var headers strings.Builder
	for _, key := range keys {
		sort.Strings(values[key])
		headers.WriteString(key)
		headers.WriteByte(':')
		headers.WriteString(strings.Join(values[key], ","))
		headers.WriteByte('\n')
	}
	return headers.String(), strings.Join(keys, ";")
}

func canonicalURI(u *url.URL) string {
	escaped := u.EscapedPath()
	if escaped == "" {
		return "/"
	}
	return escaped
}

func canonicalQueryString(query url.Values) string {
	if len(query) == 0 {
		return ""
	}
	type pair struct {
		key   string
		value string
	}
	pairs := make([]pair, 0)
	for key, values := range query {
		if len(values) == 0 {
			pairs = append(pairs, pair{key: key})
			continue
		}
		for _, value := range values {
			pairs = append(pairs, pair{key: key, value: value})
		}
	}
	sort.Slice(pairs, func(i, j int) bool {
		if pairs[i].key == pairs[j].key {
			return pairs[i].value < pairs[j].value
		}
		return pairs[i].key < pairs[j].key
	})

	encoded := make([]string, 0, len(pairs))
	for _, item := range pairs {
		encoded = append(encoded, uriEncode(item.key)+"="+uriEncode(item.value))
	}
	return strings.Join(encoded, "&")
}

func normalizeHeaderValue(value string) string {
	return strings.Join(strings.Fields(value), " ")
}

func uriEncode(value string) string {
	escaped := url.QueryEscape(value)
	escaped = strings.ReplaceAll(escaped, "+", "%20")
	escaped = strings.ReplaceAll(escaped, "%7E", "~")
	return escaped
}

func sha256Hex(body []byte) string {
	hash := sha256.Sum256(body)
	return hex.EncodeToString(hash[:])
}

func deriveSigningKey(secret, date, region, service string) []byte {
	dateKey := hmacSHA256([]byte("AWS4"+secret), []byte(date))
	dateRegionKey := hmacSHA256(dateKey, []byte(region))
	dateRegionServiceKey := hmacSHA256(dateRegionKey, []byte(service))
	return hmacSHA256(dateRegionServiceKey, []byte("aws4_request"))
}

func hmacSHA256(key []byte, body []byte) []byte {
	mac := hmac.New(sha256.New, key)
	mac.Write(body)
	return mac.Sum(nil)
}
