package google

import (
	"context"
	"testing"

	models "github.com/vcp-vsa-control-Plane/vsa-control-plane/hyperscaler/models"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/utils/middleware/log"
)

func TestCreateCloudRunServiceRejectsInvalidContainerPort(t *testing.T) {
	for _, port := range []int64{-1, 65536} {
		t.Run("invalid port", func(t *testing.T) {
			service := &GcpServices{Logger: log.NewLogger()}

			_, err := service.CreateCloudRunService(context.Background(), &models.CloudRunServiceConfig{
				ContainerPort: port,
			})
			if err == nil {
				t.Fatalf("CreateCloudRunService() accepted invalid port %d", port)
			}
		})
	}
}
