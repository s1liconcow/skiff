package artifact

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"strings"
)

type FileFetcher struct {
	HTTPClient *http.Client
}

func (f FileFetcher) Fetch(ctx context.Context, uri string) ([]byte, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	parsed, err := url.Parse(uri)
	if err != nil {
		return nil, err
	}
	switch parsed.Scheme {
	case "", "file":
		path := uri
		if parsed.Scheme == "file" {
			path = parsed.Path
		}
		return os.ReadFile(path)
	case "http", "https":
		client := f.HTTPClient
		if client == nil {
			client = http.DefaultClient
		}
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, uri, nil)
		if err != nil {
			return nil, err
		}
		resp, err := client.Do(req)
		if err != nil {
			return nil, err
		}
		defer resp.Body.Close()
		if resp.StatusCode < 200 || resp.StatusCode >= 300 {
			return nil, fmt.Errorf("fetch artifact %s: HTTP %s", uri, resp.Status)
		}
		return io.ReadAll(resp.Body)
	default:
		if strings.Contains(uri, "://") {
			return nil, fmt.Errorf("unsupported artifact URI scheme %q", parsed.Scheme)
		}
		return os.ReadFile(uri)
	}
}
