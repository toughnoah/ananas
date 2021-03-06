package driver

import (
	"context"
	"errors"
	"fmt"
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
	"sigs.k8s.io/cloud-provider-azure/pkg/azureclients/snapshotclient/mocksnapshotclient"
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
	var (
		eg           errgroup.Group
		maxVolumeBan bool
	)
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
	dataDisk := make([]compute.DataDisk, 0)
	vm := NewFakeVm(dataDisk)
	testCloud := driver.GetCloud()

	mockDisksClient := testCloud.DisksClient.(*mockdiskclient.MockInterface)
	//disk := getTestDisk(test.diskName)
	mockDisksClient.EXPECT().Get(gomock.Any(), gomock.Any(), gomock.Any()).DoAndReturn(func(ctx context.Context, resourceGroupName string, diskName string) (compute.Disk, *retry.Error) {
		//mock for case that volumes not exits
		if strings.Index(diskName, "unique") != -1 {
			return compute.Disk{}, &retry.Error{HTTPStatusCode: http.StatusNotFound, RawError: cloudprovider.DiskNotFound}
		}

		//mock for case that reach max volume limit
		if strings.Index(diskName, "sanity-max-attach-limit-vol+1") != -1 {
			maxVolumeBan = true
		} else {
			maxVolumeBan = false
		}
		if strings.Index(diskName, "sanity-controller-vol-from-snap") != -1 {
			return compute.Disk{}, &retry.Error{HTTPStatusCode: http.StatusNotFound, RawError: cloudprovider.DiskNotFound}
		}
		return disk, nil
	}).AnyTimes()

	mockDisksClient.EXPECT().Delete(gomock.Any(), gomock.Any(), gomock.Any()).Return(nil).AnyTimes()
	mockDisksClient.EXPECT().CreateOrUpdate(gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any()).DoAndReturn(func(ctx context.Context, resourceGroupName string, diskName string, diskParameter compute.Disk) *retry.Error {
		if *diskParameter.CreationData.SourceResourceID == "/subscriptions/subscription/resourceGroups/rg/providers/Microsoft.Compute/snapshots/non-existing-snapshot-id" {
			return &retry.Error{HTTPStatusCode: http.StatusNotFound, RawError: errors.New("NotFound")}
		}
		return nil
	}).AnyTimes()
	mockVMsClient := testCloud.VirtualMachinesClient.(*mockvmclient.MockInterface)
	mockVMsClient.EXPECT().Get(gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any()).DoAndReturn(func(ctx context.Context, resourceGroupName string, VMName string, expand compute.InstanceViewTypes) (compute.VirtualMachine, *retry.Error) {
		// mock for case that node not found
		if strings.Index(VMName, "unique-node") != -1 {
			return compute.VirtualMachine{}, &retry.Error{HTTPStatusCode: http.StatusNotFound, RawError: cloudprovider.InstanceNotFound}
		}
		//mock for case that reach max volume limit
		if maxVolumeBan {
			*vm.StorageProfile.DataDisks = make([]compute.DataDisk, _defaultMaxAzureVolumeLimit+1)
		} else {
			*vm.StorageProfile.DataDisks = make([]compute.DataDisk, 0)
		}
		return *vm, nil
	}).AnyTimes()
	mockVMsClient.EXPECT().UpdateAsync(gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any()).DoAndReturn(func(ctx context.Context, resourceGroupName string, VMName string, parameters compute.VirtualMachineUpdate, source string) (*azure.Future, *retry.Error) {
		// reset data disks
		*vm.StorageProfile.DataDisks = make([]compute.DataDisk, 0)
		return &azure.Future{}, nil
	}).AnyTimes()
	mockVMsClient.EXPECT().WaitForUpdateResult(gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any()).Return(nil).AnyTimes()
	fakeDiskId := fmt.Sprintf(managedDiskPath, testCloud.SubscriptionID, testCloud.ResourceGroup, testVolumeName)
	snapshot := NewFakeSnapshot(fakeDiskId, testCloud.Location)
	mockSpClient := testCloud.SnapshotsClient.(*mocksnapshotclient.MockInterface)
	mockSpClient.EXPECT().CreateOrUpdate(gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any()).DoAndReturn(func(ctx context.Context, resourceGroupName string, snapshotName string, snapshot compute.Snapshot) *retry.Error {
		if strings.Index(snapshotName, "CreateSnapshot-snapshot-2") != -1 &&
			strings.Index(*snapshot.Name, "CreateSnapshot-volume-3") != -1 {
			return &retry.Error{HTTPStatusCode: http.StatusConflict, RawError: errors.New("already exist")}
		}
		return nil
	}).AnyTimes()
	mockSpClient.EXPECT().Get(gomock.Any(), gomock.Any(), gomock.Any()).DoAndReturn(func(ctx context.Context, resourceGroupName string, snapshotName string) (compute.Snapshot, *retry.Error) {
		return snapshot, nil
	}).AnyTimes()
	mockSpClient.EXPECT().Delete(gomock.Any(), gomock.Any(), gomock.Any()).Return(nil).AnyTimes()
	testCloud.SnapshotsClient = mockSpClient

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
