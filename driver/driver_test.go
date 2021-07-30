package driver

import (
	"context"
	"github.com/container-storage-interface/spec/lib/go/csi"
	"github.com/golang/mock/gomock"
	"github.com/google/uuid"
	"github.com/kubernetes-csi/csi-test/v4/pkg/sanity"
	"github.com/stretchr/testify/require"
	"github.com/toughnoah/ananas/pkg"
	"golang.org/x/sync/errgroup"
	"os"
	"sigs.k8s.io/cloud-provider-azure/pkg/azureclients/diskclient/mockdiskclient"
	"testing"
)

type idGenerator struct{}

func (g *idGenerator) GenerateUniqueValidVolumeID() string {
	return uuid.New().String()
}

func (g *idGenerator) GenerateInvalidVolumeID() string {
	return g.GenerateUniqueValidVolumeID()
}

func (g *idGenerator) GenerateUniqueValidNodeID() string {
	return "noah-test-vm-disk"
}

func (g *idGenerator) GenerateInvalidNodeID() string {
	return "not-an-integer"
}

func TestDriverSuite(t *testing.T) {
	var eg errgroup.Group
	driver, err := NewFakeDriver(t)
	require.NoError(t, err)
	eg.Go(func() error {
		return driver.Run(context.Background())
	})
	/*
		I finally found this should use as e2e test. It should not be mocked!
	*/

	stdCapacityRangetest := &csi.CapacityRange{
		RequiredBytes: pkg.GiBToBytes(10),
		LimitBytes:    pkg.GiBToBytes(15),
	}
	disk := NewFakeDisk(stdCapacityRangetest)
	driver.GetCloud().DisksClient.(*mockdiskclient.MockInterface).EXPECT().Get(gomock.Any(), gomock.Any(), gomock.Any()).Return(disk, nil).AnyTimes()
	driver.GetCloud().DisksClient.(*mockdiskclient.MockInterface).EXPECT().Delete(gomock.Any(), gomock.Any(), gomock.Any()).Return(nil).AnyTimes()
	driver.GetCloud().DisksClient.(*mockdiskclient.MockInterface).EXPECT().CreateOrUpdate(gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any()).Return(nil).AnyTimes()

	cfg := sanity.NewTestConfig()
	if err := os.RemoveAll(cfg.TargetPath); err != nil {
		t.Fatalf("failed to delete target path %s: %s", cfg.TargetPath, err)
	}
	if err := os.RemoveAll(cfg.StagingPath); err != nil {
		t.Fatalf("failed to delete staging path %s: %s", cfg.StagingPath, err)
	}
	cfg.Address = FakeEndPoint
	cfg.IDGen = &idGenerator{}
	cfg.IdempotentCount = 5
	cfg.TestNodeVolumeAttachLimit = true
	sanity.Test(t, cfg)
}
