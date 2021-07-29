package driver

import (
	"context"
	"github.com/Azure/azure-sdk-for-go/services/compute/mgmt/2020-12-01/compute"
	"github.com/bouk/monkey"
	"github.com/container-storage-interface/spec/lib/go/csi"
	"github.com/golang/mock/gomock"
	"github.com/stretchr/testify/require"
	"github.com/toughnoah/ananas/pkg"
	"k8s.io/apimachinery/pkg/types"
	"reflect"
	"sigs.k8s.io/cloud-provider-azure/pkg/azureclients/diskclient/mockdiskclient"
	azure "sigs.k8s.io/cloud-provider-azure/pkg/provider"
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
				d.GetCloud().DisksClient.(*mockdiskclient.MockInterface).EXPECT().Get(gomock.Any(), gomock.Any(), gomock.Any()).Return(disk, nil).AnyTimes()
				d.GetCloud().DisksClient.(*mockdiskclient.MockInterface).EXPECT().CreateOrUpdate(gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any()).Return(nil).AnyTimes()
				_, err = d.CreateVolume(context.Background(), req)
				expectedErr := error(nil)
				if !reflect.DeepEqual(err, expectedErr) {
					t.Errorf("actualErr: (%v), expectedErr: (%v)", err, expectedErr)
				}
			},
		},
	}
	for _, tc := range testCases {
		t.Run(tc.name, tc.testFunc)
	}
}

func TestPublishVolume(t *testing.T) {
	d, _ := NewFakeDriver(t)
	monkey.PatchInstanceMethod(reflect.TypeOf(d.az), "AttachDisk", func(_ *azure.Cloud, isManagedDisk bool, diskName, diskURI string, nodeName types.NodeName, cachingMode compute.CachingTypes) (int32, error) {
		return 1, nil
	})
	req := &csi.ControllerPublishVolumeRequest{
		VolumeId:         "testVolumeID",
		VolumeCapability: &csi.VolumeCapability{AccessMode: &csi.VolumeCapability_AccessMode{Mode: 2}},
		NodeId:           fakeNode,
	}
	_, err := d.ControllerPublishVolume(context.Background(), req)
	require.NoError(t, err)
}
