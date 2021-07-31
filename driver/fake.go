package driver

import (
	"fmt"
	compute "github.com/Azure/azure-sdk-for-go/services/compute/mgmt/2020-12-01/compute"
	"github.com/Azure/go-autorest/autorest/to"
	"github.com/container-storage-interface/spec/lib/go/csi"
	"github.com/golang/mock/gomock"
	"github.com/toughnoah/ananas/pkg"
	"k8s.io/utils/mount"
	azure "sigs.k8s.io/cloud-provider-azure/pkg/provider"
	"testing"
)

const (
	FakeEndPoint    = "unix:///tmp/csi.sock"
	fakeNode        = "noah-test-node"
	managedDiskPath = "/subscriptions/%s/resourceGroups/%s/providers/Microsoft.Compute/disks/%s"
)

var (
	stdVolumeCapability = &csi.VolumeCapability{
		AccessType: &csi.VolumeCapability_Mount{
			Mount: &csi.VolumeCapability_MountVolume{},
		},
		AccessMode: &csi.VolumeCapability_AccessMode{
			Mode: csi.VolumeCapability_AccessMode_SINGLE_NODE_WRITER,
		},
	}
	stdVolumeCapabilities = []*csi.VolumeCapability{
		stdVolumeCapability,
	}
	//stdCapacityRange = &csi.CapacityRange{
	//	RequiredBytes: volumehelper.GiBToBytes(10),
	//	LimitBytes:    volumehelper.GiBToBytes(15),
	//}
)

var (
	testVolumeName = "noah-test-volume"
)

// NewFakeDriver use test cloud for mock
func NewFakeDriver(t *testing.T) (*Driver, error) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()
	fakeAz := azure.GetTestCloud(ctrl)
	fakeMnt := &fakeMounter{
		mounted: make(map[string]string),
	}
	return NewDriver(FakeEndPoint, fakeNode, fakeAz, fakeMnt)
}

var _ Mounter = &fakeMounter{}

type fakeMounter struct {
	mounted map[string]string
}

func (f *fakeMounter) Format(source string, fsType string) error {
	return nil
}

func (f *fakeMounter) Mount(source string, target string, fsType string, options ...string) error {
	f.mounted[target] = source
	return nil
}

func (f *fakeMounter) Unmount(target string) error {
	delete(f.mounted, target)
	return nil
}

func (f *fakeMounter) GetDeviceName(_ mount.Interface, mountPath string) (string, error) {
	if _, ok := f.mounted[mountPath]; ok {
		return "/mnt/sda1", nil
	}

	return "", nil
}

func (f *fakeMounter) IsFormatted(source string) (bool, error) {
	return true, nil
}

func (f *fakeMounter) IsMounted(target string) (bool, error) {
	_, ok := f.mounted[target]
	return ok, nil
}

func (f *fakeMounter) GetStatistics(volumePath string) (VolumeStatistics, error) {
	return VolumeStatistics{
		AvailableBytes: 3 * pkg.GiB,
		TotalBytes:     10 * pkg.GiB,
		UsedBytes:      7 * pkg.GiB,

		AvailableInodes: 3000,
		TotalInodes:     10000,
		UsedInodes:      7000,
	}, nil
}

// NewFakeDisk return fake disk for mock
func NewFakeDisk(stdCapacityRangetest *csi.CapacityRange) compute.Disk {
	size := int32(pkg.BytesToGiB(stdCapacityRangetest.RequiredBytes))
	id := fmt.Sprintf(managedDiskPath, "subscription", "rg", testVolumeName)
	disk := compute.Disk{
		ID:   &id,
		Name: &testVolumeName,
		DiskProperties: &compute.DiskProperties{
			DiskSizeGB:        &size,
			ProvisioningState: to.StringPtr("Succeeded"),
			DiskState:         compute.Unattached,
		},
	}
	return disk
}

// NewFakeVm return fake vm for mock
func NewFakeVm(dataDisk []compute.DataDisk) *compute.VirtualMachine {
	Location := "chinaeast2"
	NodeName := fakeNode
	InstanceId := "/subscriptions/subscription/resourceGroups/rg/providers/Microsoft.Compute/virtualMachines/noah-test-node"
	vm := compute.VirtualMachine{
		Name:     &NodeName,
		ID:       &InstanceId,
		Location: &Location,
		VirtualMachineProperties: &compute.VirtualMachineProperties{
			StorageProfile: &compute.StorageProfile{
				DataDisks: &dataDisk,
			},
		},
	}
	return &vm
}
