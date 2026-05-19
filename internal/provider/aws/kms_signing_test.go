package aws

import "testing"

func TestKMSReleaseSigningRefRoundTrip(t *testing.T) {
	ref := DefaultKMSReleaseSigningRef("QuickStart", "us-west-2")
	if ref != "aws-kms://alias/skiff-quickstart-release-signing?region=us-west-2" {
		t.Fatalf("ref = %q", ref)
	}
	keyID, err := ParseKMSReleaseSigningRef(ref)
	if err != nil {
		t.Fatal(err)
	}
	if keyID != "alias/skiff-quickstart-release-signing" {
		t.Fatalf("key ID = %q", keyID)
	}
	if region := KMSReleaseSigningRegion(ref); region != "us-west-2" {
		t.Fatalf("region = %q", region)
	}
}

func TestParseKMSReleaseSigningKeyRef(t *testing.T) {
	keyID, err := ParseKMSReleaseSigningRef("aws-kms://key/1234abcd?region=us-east-1")
	if err != nil {
		t.Fatal(err)
	}
	if keyID != "1234abcd" {
		t.Fatalf("key ID = %q", keyID)
	}
}
