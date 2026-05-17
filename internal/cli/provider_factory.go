package cli

import (
	"strings"

	"github.com/s1liconcow/skiff/internal/config"
	"github.com/s1liconcow/skiff/internal/objstore"
	"github.com/s1liconcow/skiff/internal/provider"
	"github.com/s1liconcow/skiff/internal/provider/aws"
	fakeprovider "github.com/s1liconcow/skiff/internal/provider/fake"
)

func newCLIProvider(cfg config.Config, store objstore.ObjectStore) (provider.Provider, error) {
	if isFakeProvider(cfg.Provider) {
		opts := []fakeprovider.Option{}
		if store != nil {
			opts = append(opts, fakeprovider.WithStateStore(store))
		}
		return fakeprovider.New(opts...), nil
	}
	opts := []aws.Option{}
	if store != nil {
		opts = append(opts, aws.WithStateStore(store))
	}
	return aws.NewFromConfig(cfg, opts...)
}

func newCLIProviderNoStore(cfg config.Config) (provider.Provider, error) {
	return newCLIProvider(cfg, nil)
}

func isFakeProvider(name string) bool {
	return strings.EqualFold(strings.TrimSpace(name), fakeprovider.Name)
}
