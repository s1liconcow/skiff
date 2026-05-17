package providerconformance

import (
	"testing"
	"time"

	"github.com/s1liconcow/skiff/internal/objstore/memory"
	fakeprovider "github.com/s1liconcow/skiff/internal/provider/fake"
)

func TestFakeProviderConformance(t *testing.T) {
	store := memory.New()
	Run(t, Suite{
		Provider: fakeprovider.New(
			fakeprovider.WithStateStore(store),
			fakeprovider.WithClock(func() time.Time {
				return time.Date(2026, 5, 17, 0, 0, 0, 0, time.UTC)
			}),
		),
		Store: store,
	})
}
