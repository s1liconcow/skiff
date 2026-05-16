package signing

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/s1liconcow/skiff/internal/security"
	"github.com/s1liconcow/skiff/internal/state/schema"
)

type ObjectFinding struct {
	Code    string `json:"code"`
	Summary string `json:"summary"`
}

type ObjectVerification struct {
	OK                bool              `json:"ok"`
	SchemaVersion     string            `json:"schema_version,omitempty"`
	Digest            string            `json:"digest,omitempty"`
	VerifiedSignature *schema.Signature `json:"verified_signature,omitempty"`
	Findings          []ObjectFinding   `json:"findings,omitempty"`
}

type signedObjectHeader struct {
	SchemaVersion string             `json:"schema_version"`
	Digest        string             `json:"digest"`
	Signatures    []schema.Signature `json:"signatures"`
}

func VerifySignedJSON(ctx context.Context, body []byte, verifier Verifier, supportedSchemaVersion string) ObjectVerification {
	var header signedObjectHeader
	result := ObjectVerification{}
	if err := json.Unmarshal(body, &header); err != nil {
		result.Findings = append(result.Findings, ObjectFinding{Code: "INVALID_JSON", Summary: err.Error()})
		return result
	}
	result.SchemaVersion = header.SchemaVersion
	if supportedSchemaVersion != "" && header.SchemaVersion != supportedSchemaVersion {
		result.Findings = append(result.Findings, ObjectFinding{
			Code:    "UNSUPPORTED_SCHEMA",
			Summary: fmt.Sprintf("object schema version %q is not supported", header.SchemaVersion),
		})
	}
	digest, err := security.UnsignedJSONDigest(body)
	if err != nil {
		result.Findings = append(result.Findings, ObjectFinding{Code: "DIGEST_CALCULATION_FAILED", Summary: err.Error()})
		return result
	}
	result.Digest = digest
	if header.Digest == "" || header.Digest != digest {
		result.Findings = append(result.Findings, ObjectFinding{Code: "INVALID_DIGEST", Summary: "object digest is missing or does not match canonical unsigned object"})
	}
	signature, err := VerifyAny(ctx, verifier, digest, header.Signatures)
	if err != nil {
		code := "INVALID_SIGNATURE"
		if len(header.Signatures) == 0 {
			code = "MISSING_SIGNATURE"
		}
		result.Findings = append(result.Findings, ObjectFinding{Code: code, Summary: err.Error()})
	} else {
		result.VerifiedSignature = &signature
	}
	result.OK = len(result.Findings) == 0
	return result
}
