package compat

import "testing"

func TestSatisfiesMinimum(t *testing.T) {
	cases := []struct {
		current string
		minimum string
		want    bool
	}{
		{current: "v1.2.3", minimum: "1.2.3", want: true},
		{current: "1.3.0", minimum: "1.2.9", want: true},
		{current: "1.2.2", minimum: "1.2.3", want: false},
		{current: "dev", minimum: "1.2.3", want: true},
		{current: "1.2.3", minimum: "", want: true},
	}
	for _, tc := range cases {
		got, err := SatisfiesMinimum(tc.current, tc.minimum)
		if err != nil {
			t.Fatalf("SatisfiesMinimum(%q, %q) error = %v", tc.current, tc.minimum, err)
		}
		if got != tc.want {
			t.Fatalf("SatisfiesMinimum(%q, %q) = %v, want %v", tc.current, tc.minimum, got, tc.want)
		}
	}
}

func TestCheckClientServerWarnsWhenSkiffdOlder(t *testing.T) {
	findings := CheckClientServer("1.2.0", "1.1.9")
	if len(findings) != 1 || findings[0].Code != "SKIFFD_VERSION_OLDER_THAN_CLI" {
		t.Fatalf("findings = %+v", findings)
	}
}
