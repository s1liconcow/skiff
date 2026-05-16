package paths

import (
	"fmt"
	"net/url"
	"strings"
	"time"
)

type InputError struct {
	Field   string `json:"field"`
	Value   string `json:"value,omitempty"`
	Code    string `json:"code"`
	Message string `json:"message"`
}

func (e InputError) Error() string {
	if e.Value == "" {
		return fmt.Sprintf("%s: %s", e.Field, e.Message)
	}
	return fmt.Sprintf("%s %q: %s", e.Field, e.Value, e.Message)
}

func ValidateName(field, value string) error {
	return validateName(field, value)
}

func ValidateID(field, value string) error {
	return validateID(field, value)
}

func ServiceControl(service string) (string, error) {
	if err := validateName("service", service); err != nil {
		return "", err
	}
	return "services/" + service + "/control.json", nil
}

func ReleaseManifest(service, release string) (string, error) {
	if err := validateName("service", service); err != nil {
		return "", err
	}
	if err := validateID("release", release); err != nil {
		return "", err
	}
	return "services/" + service + "/releases/" + release + "/release.json", nil
}

func RuntimeManifest(service, release string) (string, error) {
	if err := validateName("service", service); err != nil {
		return "", err
	}
	if err := validateID("release", release); err != nil {
		return "", err
	}
	return "services/" + service + "/releases/" + release + "/runtime-manifest.json", nil
}

func OperationIntent(service, operation string) (string, error) {
	if err := validateName("service", service); err != nil {
		return "", err
	}
	if err := validateID("operation", operation); err != nil {
		return "", err
	}
	return "services/" + service + "/operations/" + operation + "/intent.json", nil
}

func OperationControl(service, operation string) (string, error) {
	if err := validateName("service", service); err != nil {
		return "", err
	}
	if err := validateID("operation", operation); err != nil {
		return "", err
	}
	return "services/" + service + "/operations/" + operation + "/control.json", nil
}

func OperationEvent(service, operation, event string) (string, error) {
	if err := validateName("service", service); err != nil {
		return "", err
	}
	if err := validateID("operation", operation); err != nil {
		return "", err
	}
	if err := validateID("event", event); err != nil {
		return "", err
	}
	return "services/" + service + "/operations/" + operation + "/events/" + event + ".json", nil
}

func SagaIntent(saga string) (string, error) {
	if err := validateID("saga", saga); err != nil {
		return "", err
	}
	return "sagas/" + saga + "/intent.json", nil
}

func SagaGraph(saga string) (string, error) {
	if err := validateID("saga", saga); err != nil {
		return "", err
	}
	return "sagas/" + saga + "/graph.json", nil
}

func SagaControl(saga string) (string, error) {
	if err := validateID("saga", saga); err != nil {
		return "", err
	}
	return "sagas/" + saga + "/control.json", nil
}

func SagaEvent(saga, event string) (string, error) {
	if err := validateID("saga", saga); err != nil {
		return "", err
	}
	if err := validateID("event", event); err != nil {
		return "", err
	}
	return "sagas/" + saga + "/events/" + event + ".json", nil
}

func LogicalResource(kind, name string) (string, error) {
	if err := validateName("kind", kind); err != nil {
		return "", err
	}
	if err := validateName("name", name); err != nil {
		return "", err
	}
	return "resources/by-logical/" + kind + "/" + name + ".json", nil
}

func ProviderResource(provider, kind, id string) (string, error) {
	if err := validateName("provider", provider); err != nil {
		return "", err
	}
	if err := validateName("kind", kind); err != nil {
		return "", err
	}
	escaped, err := encodeProviderID(id)
	if err != nil {
		return "", err
	}
	return "resources/by-provider/" + provider + "/" + kind + "/" + escaped + ".json", nil
}

func ServiceObservation(service, observation string) (string, error) {
	if err := validateName("service", service); err != nil {
		return "", err
	}
	if err := validateID("observation", observation); err != nil {
		return "", err
	}
	return "observations/services/" + service + "/" + observation + ".json", nil
}

func ServicesIndex() string {
	return "indexes/services.json"
}

func ActiveSagasIndex() string {
	return "indexes/active-sagas.json"
}

func RecentEventsIndex() string {
	return "indexes/recent-events.json"
}

func AuditEvent(day, event string) (string, error) {
	if _, err := time.Parse("2006-01-02", day); err != nil {
		return "", InputError{
			Field:   "day",
			Value:   day,
			Code:    "INVALID_DATE",
			Message: "must use yyyy-mm-dd",
		}
	}
	if err := validateID("event", event); err != nil {
		return "", err
	}
	return "audit/" + day + "/" + event + ".json", nil
}

func AuditEventForTime(t time.Time, event string) (string, error) {
	return AuditEvent(t.UTC().Format("2006-01-02"), event)
}

var reservedSegments = map[string]struct{}{
	".":            {},
	"..":           {},
	"audit":        {},
	"by-logical":   {},
	"by-provider":  {},
	"control":      {},
	"control.json": {},
	"events":       {},
	"indexes":      {},
	"observations": {},
	"operations":   {},
	"releases":     {},
	"resources":    {},
	"sagas":        {},
	"services":     {},
}

func validateName(field, value string) error {
	if value == "" {
		return InputError{Field: field, Code: "REQUIRED", Message: "is required"}
	}
	if strings.TrimSpace(value) != value {
		return InputError{Field: field, Value: value, Code: "INVALID_NAME", Message: "must not contain leading or trailing whitespace"}
	}
	if _, reserved := reservedSegments[value]; reserved {
		return InputError{Field: field, Value: value, Code: "RESERVED_NAME", Message: "is reserved for Skiff object-state layout"}
	}
	if !isLowerAlnum(value[0]) || !isLowerAlnum(value[len(value)-1]) {
		return InputError{Field: field, Value: value, Code: "INVALID_NAME", Message: "must start and end with a lowercase letter or digit"}
	}
	for i := 0; i < len(value); i++ {
		c := value[i]
		if isLowerAlnum(c) || c == '-' {
			continue
		}
		return InputError{Field: field, Value: value, Code: "INVALID_NAME", Message: "must contain only lowercase letters, digits, and hyphens"}
	}
	return nil
}

func validateID(field, value string) error {
	if value == "" {
		return InputError{Field: field, Code: "REQUIRED", Message: "is required"}
	}
	if strings.TrimSpace(value) != value {
		return InputError{Field: field, Value: value, Code: "INVALID_ID", Message: "must not contain leading or trailing whitespace"}
	}
	if value == "." || value == ".." {
		return InputError{Field: field, Value: value, Code: "INVALID_ID", Message: "must not be a relative path segment"}
	}
	for i := 0; i < len(value); i++ {
		c := value[i]
		if isIDChar(c) {
			continue
		}
		return InputError{Field: field, Value: value, Code: "INVALID_ID", Message: "must not contain path separators, whitespace, or control characters"}
	}
	return nil
}

func encodeProviderID(value string) (string, error) {
	if value == "" {
		return "", InputError{Field: "id", Code: "REQUIRED", Message: "is required"}
	}
	if strings.TrimSpace(value) != value {
		return "", InputError{Field: "id", Value: value, Code: "INVALID_ID", Message: "must not contain leading or trailing whitespace"}
	}
	for i := 0; i < len(value); i++ {
		if value[i] <= ' ' || value[i] == 0x7f {
			return "", InputError{Field: "id", Value: value, Code: "INVALID_ID", Message: "must not contain whitespace or control characters"}
		}
	}
	escaped := url.PathEscape(value)
	if escaped == "." || escaped == ".." {
		return "", InputError{Field: "id", Value: value, Code: "INVALID_ID", Message: "must not be a relative path segment"}
	}
	return escaped, nil
}

func isLowerAlnum(c byte) bool {
	return c >= 'a' && c <= 'z' || c >= '0' && c <= '9'
}

func isIDChar(c byte) bool {
	switch {
	case c >= 'a' && c <= 'z':
		return true
	case c >= 'A' && c <= 'Z':
		return true
	case c >= '0' && c <= '9':
		return true
	case c == '-' || c == '_' || c == '.' || c == ':' || c == '@' || c == '+':
		return true
	default:
		return false
	}
}
