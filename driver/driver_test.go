package driver

import (
	"context"
	"github.com/Azure/azure-sdk-for-go/services/compute/mgmt/2020-12-01/compute"
	"github.com/Azure/go-autorest/autorest/azure"
	"github.com/container-storage-interface/spec/lib/go/csi"
	"github.com/golang/mock/gomock"
	"github.com/google/uuid"
	"github.com/kubernetes-csi/csi-test/v4/pkg/sanity"
	"github.com/stretchr/testify/require"
	"github.com/toughnoah/ananas/pkg"
	"golang.org/x/sync/errgroup"
	cloudprovider "k8s.io/cloud-provider"
	"net/http"
	"os"
	"sigs.k8s.io/cloud-provider-azure/pkg/azureclients/diskclient/mockdiskclient"
	"sigs.k8s.io/cloud-provider-azure/pkg/azureclients/vmclient/mockvmclient"
	"sigs.k8s.io/cloud-provider-azure/pkg/retry"
	"strings"
	"testing"
)

type idGenerator struct{}

func (g *idGenerator) GenerateUniqueValidVolumeID() string {
	return "unique" + uuid.New().String()
}

func (g *idGenerator) GenerateInvalidVolumeID() string {
	return g.GenerateUniqueValidVolumeID()
}

func (g *idGenerator) GenerateUniqueValidNodeID() string {
	return "unique-node" + uuid.New().String()
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
	stdCapacityRangetest := &csi.CapacityRange{
		RequiredBytes: pkg.GiBToBytes(10),
		LimitBytes:    pkg.GiBToBytes(15),
	}
	disk := NewFakeDisk(stdCapacityRangetest)
	testCloud := driver.GetCloud()
	mockDisksClient := testCloud.DisksClient.(*mockdiskclient.MockInterface)
	//disk := getTestDisk(test.diskName)
	var maxVolumeBan = false

	mockDisksClient.EXPECT().Get(gomock.Any(), gomock.Any(), gomock.Any()).DoAndReturn(func(ctx context.Context, resourceGroupName string, diskName string) (compute.Disk, *retry.Error) {
		if strings.Index(diskName, "unique") != -1 {
			return compute.Disk{}, &retry.Error{HTTPStatusCode: http.StatusNotFound, RawError: cloudprovider.DiskNotFound}
		}
		if strings.Index(diskName, "sanity-max-attach-limit-vol+1") != -1 {
			maxVolumeBan = true
		} else {
			maxVolumeBan = false
		}
		return disk, nil
	}).AnyTimes()

	mockDisksClient.EXPECT().Delete(gomock.Any(), gomock.Any(), gomock.Any()).Return(nil).AnyTimes()
	mockDisksClient.EXPECT().CreateOrUpdate(gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any()).Return(nil).AnyTimes()
	dataDisk := make([]compute.DataDisk, 0)
	vm := NewFakeVm(dataDisk)
	mockVMsClient := testCloud.VirtualMachinesClient.(*mockvmclient.MockInterface)
	mockVMsClient.EXPECT().Get(gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any()).DoAndReturn(func(ctx context.Context, resourceGroupName string, VMName string, expand compute.InstanceViewTypes) (compute.VirtualMachine, *retry.Error) {
		if strings.Index(VMName, "unique-node") != -1 {
			return compute.VirtualMachine{}, &retry.Error{HTTPStatusCode: http.StatusNotFound, RawError: cloudprovider.InstanceNotFound}
		}
		if maxVolumeBan {
			*vm.StorageProfile.DataDisks = make([]compute.DataDisk, _defaultMaxAzureVolumeLimit+1)
		} else {
			*vm.StorageProfile.DataDisks = make([]compute.DataDisk, 0)
		}
		return *vm, nil
	}).AnyTimes()
	mockVMsClient.EXPECT().UpdateAsync(gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any()).DoAndReturn(func(ctx context.Context, resourceGroupName string, VMName string, parameters compute.VirtualMachineUpdate, source string) (*azure.Future, *retry.Error) {
		*vm.StorageProfile.DataDisks = make([]compute.DataDisk, 0)
		return &azure.Future{}, nil
	}).AnyTimes()
	mockVMsClient.EXPECT().WaitForUpdateResult(gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any()).Return(nil).AnyTimes()
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
