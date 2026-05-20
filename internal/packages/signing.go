package packages

import (
	"net/url"
	"os"
	"path/filepath"
	"strings"
)

func signatureRefForDirectory(dir, explicit string) (string, error) {
	if explicit != "" {
		if err := verifySignatureRef(explicit); err != nil {
			return "", err
		}
		return explicit, nil
	}
	for _, name := range []string{"skiff-package.sig", "package.sig"} {
		path := filepath.Join(dir, name)
		if _, err := os.Stat(path); err == nil {
			if err := verifySignatureFile(path); err != nil {
				return "", err
			}
			abs, err := filepath.Abs(path)
			if err != nil {
				return "", err
			}
			return "file://" + filepath.ToSlash(abs), nil
		}
	}
	return "", nil
}

func verifySignatureRef(ref string) error {
	ref = strings.TrimSpace(ref)
	if ref == "" {
		return packageError("PACKAGE_SIGNATURE_REQUIRED", "package signature reference is required", nil)
	}
	if strings.HasPrefix(ref, "file://") {
		path, err := signatureFilePath(ref)
		if err != nil {
			return err
		}
		return verifySignatureFile(path)
	}
	if strings.HasPrefix(ref, "oci://") && digestFromOCIRef(ref) == "" {
		return packageError("PACKAGE_SIGNATURE_DIGEST_REQUIRED", "OCI package signature refs must be digest-pinned", nil)
	}
	return nil
}

func verifySignatureFile(path string) error {
	info, err := os.Stat(path)
	if err != nil {
		if os.IsNotExist(err) {
			return packageError("PACKAGE_SIGNATURE_NOT_FOUND", "package signature file was not found", err)
		}
		return err
	}
	if info.IsDir() {
		return packageError("PACKAGE_SIGNATURE_INVALID", "package signature ref must point at a file", nil)
	}
	if info.Size() == 0 {
		return packageError("PACKAGE_SIGNATURE_INVALID", "package signature file is empty", nil)
	}
	return nil
}

func signatureFilePath(ref string) (string, error) {
	raw := strings.TrimPrefix(ref, "file://")
	if strings.HasPrefix(raw, "localhost/") {
		raw = strings.TrimPrefix(raw, "localhost")
	}
	if decoded, err := url.PathUnescape(raw); err == nil {
		raw = decoded
	}
	if strings.TrimSpace(raw) == "" {
		return "", packageError("PACKAGE_SIGNATURE_NOT_FOUND", "package signature file was not found", nil)
	}
	if !filepath.IsAbs(raw) {
		abs, err := filepath.Abs(raw)
		if err != nil {
			return "", err
		}
		raw = abs
	}
	return raw, nil
}

func isPackageSignatureFile(name string) bool {
	return name == "skiff-package.sig" || name == "package.sig"
}
