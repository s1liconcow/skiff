package cli

import (
	"bytes"
	"strings"
	"testing"
)

func TestProgressRendererSuppressesJSONAndAvoidsANSI(t *testing.T) {
	var jsonOut bytes.Buffer
	newProgressRenderer(&jsonOut, "json", false).Step("deploying %s", "payments-api")
	if jsonOut.Len() != 0 {
		t.Fatalf("json progress output = %q, want empty", jsonOut.String())
	}

	var humanOut bytes.Buffer
	newProgressRenderer(&humanOut, "human", true).Step("deploying %s", "payments-api")
	if got := humanOut.String(); got != "deploying payments-api\n" || strings.Contains(got, "\x1b[") {
		t.Fatalf("human progress output = %q", got)
	}
}
