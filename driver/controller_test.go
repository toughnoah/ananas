package driver

import (
	"context"
	"github.com/Azure/azure-sdk-for-go/services/compute/mgmt/2020-12-01/compute"
	"github.com/Azure/go-autorest/autorest/azure"
	"github.com/Azure/go-autorest/autorest/to"
	"github.com/container-storage-interface/spec/lib/go/csi"
	"github.com/golang/mock/gomock"
	"github.com/stretchr/testify/require"
	"github.com/toughnoah/ananas/pkg"
	"sigs.k8s.io/cloud-provider-azure/pkg/azureclients/diskclient/mockdiskclient"
	"sigs.k8s.io/cloud-provider-azure/pkg/azureclients/vmclient/mockvmclient"
	"testing"
)

func TestCreateVolume(t *testing.T) {
	testCases := []struct {
		name     string
		testFunc func(t *testing.T)
	}{
		{
			name: "valid request",
			testFunc: func(t *testing.T) {
				d, err := NewFakeDriver(t)
				require.NoError(t, err)
				stdCapacityRangetest := &csi.CapacityRange{
					RequiredBytes: pkg.GiBToBytes(10),
					LimitBytes:    pkg.GiBToBytes(15),
				}
				req := &csi.CreateVolumeRequest{
					Name:               testVolumeName,
					VolumeCapabilities: stdVolumeCapabilities,
					CapacityRange:      stdCapacityRangetest,
				}
				disk := NewFakeDisk(stdCapacityRangetest)
				testCloud := d.GetCloud()
				mockDisksClient := testCloud.DisksClient.(*mockdiskclient.MockInterface)
				//disk := getTestDisk(test.diskName)
				mockDisksClient.EXPECT().CreateOrUpdate(gomock.Any(), testCloud.ResourceGroup, testVolumeName, gomock.Any()).Return(nil).AnyTimes()
				mockDisksClient.EXPECT().Get(gomock.Any(), testCloud.ResourceGroup, testVolumeName).Return(disk, nil).AnyTimes()
				_, err = d.CreateVolume(context.Background(), req)
				require.NoError(t, err)
			},
		},
	}
	for _, tc := range testCases {
		t.Run(tc.name, tc.testFunc)
	}
}

func TestControllerPublishVolume(t *testing.T) {
	d, err := NewFakeDriver(t)
	require.NoError(t, err)
	dataDisk := make([]compute.DataDisk, 0)
	vm := NewFakeVm(dataDisk)
	testCloud := d.GetCloud()
	stdCapacityRangetest := &csi.CapacityRange{
		RequiredBytes: pkg.GiBToBytes(10),
		LimitBytes:    pkg.GiBToBytes(15),
	}
	disk := NewFakeDisk(stdCapacityRangetest)
	mockDisksClient := testCloud.DisksClient.(*mockdiskclient.MockInterface)
	mockDisksClient.EXPECT().Get(gomock.Any(), testCloud.ResourceGroup, testVolumeName).Return(disk, nil).AnyTimes()
	mockVMsClient := testCloud.VirtualMachinesClient.(*mockvmclient.MockInterface)
	mockVMsClient.EXPECT().Get(gomock.Any(), testCloud.ResourceGroup, fakeNode, gomock.Any()).Return(*vm, nil).AnyTimes()
	mockVMsClient.EXPECT().Update(gomock.Any(), testCloud.ResourceGroup, gomock.Any(), gomock.Any(), gomock.Any()).Return(nil).AnyTimes()
	mockVMsClient.EXPECT().UpdateAsync(gomock.Any(), testCloud.ResourceGroup, gomock.Any(), gomock.Any(), gomock.Any()).Return(&azure.Future{}, nil).AnyTimes()
	mockVMsClient.EXPECT().WaitForUpdateResult(gomock.Any(), gomock.Any(), testCloud.ResourceGroup, gomock.Any()).Return(nil).AnyTimes()
	req2 := &csi.ControllerPublishVolumeRequest{
		VolumeId:         testVolumeName,
		VolumeCapability: &csi.VolumeCapability{AccessMode: &csi.VolumeCapability_AccessMode{Mode: 2}},
		NodeId:           fakeNode,
	}
	_, err = d.ControllerPublishVolume(context.Background(), req2)
	require.NoError(t, err)
}

func TestControllerUnPublishVolume(t *testing.T) {
	dataDisk := make([]compute.DataDisk, 0)
	dataDisk = append(dataDisk, compute.DataDisk{Name: to.StringPtr(testVolumeName)})
	vm := NewFakeVm(dataDisk)
	d, err := NewFakeDriver(t)
	require.NoError(t, err)
	testCloud := d.GetCloud()
	mockVMsClient := testCloud.VirtualMachinesClient.(*mockvmclient.MockInterface)
	mockVMsClient.EXPECT().Get(gomock.Any(), testCloud.ResourceGroup, fakeNode, gomock.Any()).Return(*vm, nil).AnyTimes()
	mockVMsClient.EXPECT().UpdateAsync(gomock.Any(), testCloud.ResourceGroup, gomock.Any(), gomock.Any(), gomock.Any()).Return(&azure.Future{}, nil).AnyTimes()
	mockVMsClient.EXPECT().UpdateAsync(gomock.Any(), testCloud.ResourceGroup, gomock.Any(), gomock.Any(), gomock.Any()).Return(nil, nil).AnyTimes()
	mockVMsClient.EXPECT().WaitForUpdateResult(gomock.Any(), gomock.Any(), testCloud.ResourceGroup, gomock.Any()).Return(nil).AnyTimes()
	req := &csi.ControllerUnpublishVolumeRequest{
		VolumeId: testVolumeName,
		NodeId:   fakeNode,
	}
	_, err = d.ControllerUnpublishVolume(context.Background(), req)
	require.NoError(t, err)
}
