package pluginconformance

import (
	"path/filepath"
	"testing"

	fakeplugin "github.com/s1liconcow/skiff/internal/plugins/fake"
)

func TestFakePluginConformance(t *testing.T) {
	Run(t, Suite{
		Plugin:          fakeplugin.Plugin(),
		Runner:          fakeplugin.Runner{},
		ManifestPath:    filepath.Join("..", "..", "fixtures", "plugins", "fake"),
		SagaStepKind:    fakeplugin.SagaStepKind,
		PackageStepKind: fakeplugin.PackageStepKind,
	})
}
