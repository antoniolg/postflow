package parity_test

import (
	"testing"

	"github.com/antoniolg/publisher/internal/api"
	"github.com/antoniolg/publisher/internal/capabilities"
	"github.com/antoniolg/publisher/internal/cli"
)

func TestRequiredCapabilitiesHaveParityAcrossSurfaces(t *testing.T) {
	surfaces := map[capabilities.Surface]map[string]struct{}{
		capabilities.SurfaceAPI: api.HTTPExposedCapabilities(),
		capabilities.SurfaceMCP: api.MCPExposedCapabilities(),
		capabilities.SurfaceCLI: cli.ExposedCapabilities(),
	}

	for _, capability := range capabilities.RequiredParityCapabilities() {
		for _, surface := range capability.RequiredSurfaces {
			exposed, ok := surfaces[surface]
			if !ok {
				t.Fatalf("unknown surface %q for capability %q", surface, capability.ID)
			}
			if _, found := exposed[capability.ID]; !found {
				t.Errorf("capability %q is required on %q but not exposed", capability.ID, surface)
			}
		}
	}
}
