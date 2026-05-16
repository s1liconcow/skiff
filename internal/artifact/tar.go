package artifact

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
)

func (p Preparer) prepareTarball(ctx context.Context, req Request, dest string) error {
	body, err := p.fetcher().Fetch(ctx, req.Artifact.URI)
	if err != nil {
		return err
	}
	if err := verifyDigest(body, req.Artifact.Digest); err != nil {
		return err
	}
	reader, closeReader, err := tarReader(body)
	if err != nil {
		return err
	}
	defer closeReader()
	return extractTar(reader, dest)
}

func tarReader(body []byte) (*tar.Reader, func(), error) {
	raw := bytes.NewReader(body)
	gz, err := gzip.NewReader(raw)
	if err == nil {
		return tar.NewReader(gz), func() { _ = gz.Close() }, nil
	}
	if _, seekErr := raw.Seek(0, io.SeekStart); seekErr != nil {
		return nil, func() {}, seekErr
	}
	return tar.NewReader(raw), func() {}, nil
}

func extractTar(reader *tar.Reader, dest string) error {
	for {
		header, err := reader.Next()
		if errors.Is(err, io.EOF) {
			return nil
		}
		if err != nil {
			return err
		}
		target, err := archiveTarget(dest, header.Name)
		if err != nil {
			return err
		}
		switch header.Typeflag {
		case tar.TypeDir:
			if err := os.MkdirAll(target, modeOrDefault(header.FileInfo().Mode().Perm(), 0o755)); err != nil {
				return err
			}
		case tar.TypeReg, tar.TypeRegA:
			if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
				return err
			}
			mode := modeOrDefault(header.FileInfo().Mode().Perm(), 0o644)
			file, err := os.OpenFile(target, os.O_CREATE|os.O_EXCL|os.O_WRONLY, mode)
			if err != nil {
				return err
			}
			if _, err := io.Copy(file, reader); err != nil {
				_ = file.Close()
				return err
			}
			if err := file.Close(); err != nil {
				return err
			}
		case tar.TypeSymlink, tar.TypeLink:
			return fmt.Errorf("%w: archive links are not supported: %s", ErrUnsafeArchive, header.Name)
		default:
			return fmt.Errorf("%w: unsupported tar entry type %d for %s", ErrUnsafeArchive, header.Typeflag, header.Name)
		}
	}
}

func archiveTarget(dest, name string) (string, error) {
	cleaned := filepath.Clean(name)
	if cleaned == "." || filepath.IsAbs(cleaned) || strings.HasPrefix(cleaned, ".."+string(filepath.Separator)) || cleaned == ".." {
		return "", fmt.Errorf("%w: archive path %q escapes destination", ErrUnsafeArchive, name)
	}
	target := filepath.Join(dest, cleaned)
	if err := ensureInside(dest, target); err != nil {
		return "", err
	}
	return target, nil
}

func modeOrDefault(mode os.FileMode, fallback os.FileMode) os.FileMode {
	mode &= 0o777
	if mode == 0 {
		return fallback
	}
	return mode
}
