package driver

import (
	"context"
	"errors"
	"fmt"
	"github.com/Azure/azure-sdk-for-go/profiles/latest/compute/mgmt/compute"
	compute2 "github.com/Azure/azure-sdk-for-go/services/compute/mgmt/2020-12-01/compute"
	"github.com/Azure/go-autorest/autorest/to"
	"github.com/container-storage-interface/spec/lib/go/csi"
	"github.com/sirupsen/logrus"
	. "github.com/toughnoah/ananas/pkg"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/apimachinery/pkg/util/sets"
	azure "sigs.k8s.io/cloud-provider-azure/pkg/provider"
	"strconv"
	"strings"
)

var (
	// Azure disk currently only supports a single node to be attached
	// in read/write mode. This corresponds to `accessModes.ReadWriteOnce` in a
	// PVC resource on Kubernetes
	supportedAccessMode = &csi.VolumeCapability_AccessMode{
		Mode: csi.VolumeCapability_AccessMode_SINGLE_NODE_WRITER,
	}
)

// CreateVolume call azure api to create managed disk
func (d *Driver) CreateVolume(ctx context.Context, req *csi.CreateVolumeRequest) (*csi.CreateVolumeResponse, error) {
	size, err := ValidateCreateVolume(req)
	if err != nil {
		return nil, err
	}
	fmt.Printf("size: %d\n", size)
	volumeName := req.Name
	log := d.log.WithFields(logrus.Fields{
		"volume_name":         volumeName,
		"storage_size":        size / GiB,
		"method":              "create_volume",
		"volume_capabilities": req.VolumeCapabilities,
		"location":            d.az.Location,
	})
	log.Info("create volume called")
	// get volume first, if it's created do no thing
	// create logic
	gbSize := RoundUpGiB(size)

	resp := &csi.CreateVolumeResponse{
		Volume: &csi.Volume{
			VolumeId:      volumeName,
			CapacityBytes: size,
			AccessibleTopology: []*csi.Topology{
				{
					Segments: map[string]string{
						// this is used for pv nodeAffinity, at lease to have one
						"location": d.az.Location,
						//"diskUri":  diskUri,
					},
				},
			},
		},
	}

	disk, rerr := d.az.DisksClient.Get(ctx, d.az.ResourceGroup, volumeName)
	if rerr == nil {
		if *disk.DiskSizeGB != int32(gbSize) {
			return nil, status.Error(codes.AlreadyExists, "disk already exits")
		}
		return resp, nil
	}

	volumeOptions := &azure.ManagedDiskOptions{
		DiskName:           *disk.Name,
		StorageAccountType: compute2.PremiumLRS,
		ResourceGroup:      d.az.ResourceGroup,
		SizeGB:             int(gbSize),
	}
	diskUri, err := d.az.ManagedDiskController.CreateManagedDisk(volumeOptions)
	if err != nil {
		return nil, status.Error(codes.Internal, err.Error())
	}

	log.WithField("response", diskUri).Info("volume was created")
	return resp, nil

}

// DeleteVolume  call azure api to delete managed disk
func (d *Driver) DeleteVolume(ctx context.Context, req *csi.DeleteVolumeRequest) (*csi.DeleteVolumeResponse, error) {

	if req.VolumeId == "" {
		return nil, status.Error(codes.InvalidArgument, "DeleteVolume Volume ID must be provided")
	}

	log := d.log.WithFields(logrus.Fields{
		"volume_id": req.VolumeId,
		"method":    "delete_volume",
	})
	log.Info("delete volume called")
	volume := req.GetVolumeId()
	diskUri := d.GetDiskUri(volume)
	err := d.az.ManagedDiskController.DeleteManagedDisk(diskUri)
	//Azure disk client
	if err != nil {
		return nil, status.Error(codes.Internal, err.Error())
	}
	log.WithField("response", volume).Info("volume was deleted")
	return &csi.DeleteVolumeResponse{}, nil
}

// ControllerPublishVolume call azure api to attach azure-disk to specified vm
func (d *Driver) ControllerPublishVolume(ctx context.Context, req *csi.ControllerPublishVolumeRequest) (*csi.ControllerPublishVolumeResponse, error) {
	if err := ValidateControllerPublishVolume(req); err != nil {
		return nil, err
	}

	resourceGroup := d.az.ResourceGroup
	log := d.log.WithFields(logrus.Fields{
		"volume_id": req.VolumeId,
		"node_id":   req.NodeId,
		"method":    "controller_publish_volume",
	})
	log.Info("controller publish volume called")
	//should failed when volume does not exist
	volume := req.GetVolumeId()
	_, _, err := d.az.ManagedDiskController.GetDisk(d.az.ResourceGroup, volume)
	if err != nil {
		return nil, status.Error(codes.NotFound, err.Error())
	}
	diskUri := d.GetDiskUri(volume)
	disk := &compute2.Disk{
		Name:     to.StringPtr(volume),
		Location: to.StringPtr("chinaeast2"),
		DiskProperties: &compute2.DiskProperties{
			CreationData: &compute2.CreationData{
				CreateOption: compute2.Empty,
			},
			DiskSizeGB: to.Int32Ptr(64),
			DiskState:  compute2.DiskState(compute.DiskStateUnattached),
		},
		Sku: &compute2.DiskSku{
			Name: compute2.PremiumLRS,
		},
	}
	// should failed while can not find the vm
	vm, rErr := d.az.VirtualMachinesClient.Get(ctx, resourceGroup, req.NodeId, compute2.InstanceViewTypes(compute.InstanceViewTypesInstanceView))
	if rErr != nil {
		return nil, status.Error(codes.NotFound, rErr.RawError.Error())
	}
	// should failed if reach the max volume limit on node
	if len(*vm.StorageProfile.DataDisks) > _defaultMaxAzureVolumeLimit {
		return nil, status.Error(codes.ResourceExhausted, errors.New("reach max volume limit on node").Error())
	}

	lun, err := d.az.AttachDisk(true, volume, diskUri, types.NodeName(req.GetNodeId()), compute2.CachingTypes(compute.CachingTypesReadWrite), disk)
	if err != nil {
		return nil, status.Error(codes.Internal, err.Error())
	}
	log.Info("attach success")
	return &csi.ControllerPublishVolumeResponse{
		PublishContext: map[string]string{
			req.VolumeId: strconv.Itoa(int(lun)),
		},
	}, nil
}

// ControllerUnpublishVolume call azure api to detach azure-disk from specified vm
func (d *Driver) ControllerUnpublishVolume(ctx context.Context, req *csi.ControllerUnpublishVolumeRequest) (*csi.ControllerUnpublishVolumeResponse, error) {
	if err := ValidateControllerUnPublishVolume(req); err != nil {
		return nil, err
	}
	log := d.log.WithFields(logrus.Fields{
		"volume_id": req.VolumeId,
		"node_id":   req.NodeId,
		"method":    "controller_unpublish_volume",
	})
	log.Info("controller unpublish volume called")
	volume := req.GetVolumeId()
	diskUri := d.GetDiskUri(volume)
	err := d.az.DetachDisk(volume, diskUri, types.NodeName(req.GetNodeId()))
	if err != nil {
		return nil, status.Error(codes.Internal, err.Error())
	}

	log.Info("detach success")
	return &csi.ControllerUnpublishVolumeResponse{}, nil
}

// ValidateVolumeCapabilities validate VolumeCapability
func (d *Driver) ValidateVolumeCapabilities(ctx context.Context, req *csi.ValidateVolumeCapabilitiesRequest) (*csi.ValidateVolumeCapabilitiesResponse, error) {
	if req.VolumeId == "" {
		return nil, status.Error(codes.InvalidArgument, "ValidateVolumeCapabilities Volume ID must be provided")
	}

	if req.VolumeCapabilities == nil {
		return nil, status.Error(codes.InvalidArgument, "ValidateVolumeCapabilities Volume Capabilities must be provided")
	}

	log := d.log.WithFields(logrus.Fields{
		"volume_id":              req.VolumeId,
		"volume_capabilities":    req.VolumeCapabilities,
		"supported_capabilities": supportedAccessMode,
		"method":                 "validate_volume_capabilities",
	})
	log.Info("validate volume capabilities called")

	// check if volume exist before trying to validate it it
	_, _, err := d.az.ManagedDiskController.GetDisk(d.az.ResourceGroup, req.GetVolumeId())
	if err != nil {
		return nil, status.Error(codes.NotFound, err.Error())
	}
	// if it's not supported (i.e: wrong region), we shouldn't override it
	resp := &csi.ValidateVolumeCapabilitiesResponse{
		Confirmed: &csi.ValidateVolumeCapabilitiesResponse_Confirmed{
			VolumeCapabilities: []*csi.VolumeCapability{
				{
					AccessMode: supportedAccessMode,
				},
			},
		},
	}

	log.WithField("confirmed", resp.Confirmed).Info("supported capabilities")
	return resp, nil
}

// ListVolumes should list resource by resource group with pagination or list pv from k8s cluster
func (d *Driver) ListVolumes(ctx context.Context, request *csi.ListVolumesRequest) (*csi.ListVolumesResponse, error) {
	panic("implement me")
}

// GetCapacity returns the capacity of the storage pool
func (d *Driver) GetCapacity(ctx context.Context, request *csi.GetCapacityRequest) (*csi.GetCapacityResponse, error) {
	panic("implement me")
}

// ControllerGetCapabilities returns the capabilities of the controller service.
func (d *Driver) ControllerGetCapabilities(ctx context.Context, req *csi.ControllerGetCapabilitiesRequest) (*csi.ControllerGetCapabilitiesResponse, error) {
	newCap := func(cap csi.ControllerServiceCapability_RPC_Type) *csi.ControllerServiceCapability {
		return &csi.ControllerServiceCapability{
			Type: &csi.ControllerServiceCapability_Rpc{
				Rpc: &csi.ControllerServiceCapability_RPC{
					Type: cap,
				},
			},
		}
	}

	var caps []*csi.ControllerServiceCapability
	for _, cap := range []csi.ControllerServiceCapability_RPC_Type{
		csi.ControllerServiceCapability_RPC_CREATE_DELETE_VOLUME,
		csi.ControllerServiceCapability_RPC_PUBLISH_UNPUBLISH_VOLUME,
		//csi.ControllerServiceCapability_RPC_LIST_VOLUMES,
		//csi.ControllerServiceCapability_RPC_CREATE_DELETE_SNAPSHOT,
		//csi.ControllerServiceCapability_RPC_LIST_SNAPSHOTS,
	} {
		caps = append(caps, newCap(cap))
	}

	resp := &csi.ControllerGetCapabilitiesResponse{
		Capabilities: caps,
	}

	d.log.WithFields(logrus.Fields{
		"response": resp,
		"method":   "controller_get_capabilities",
	}).Info("controller get capabilities called")
	return resp, nil
}

// CreateSnapshot will be called by the CO to create a new snapshot from a
// source volume on behalf of a user.
func (d *Driver) CreateSnapshot(ctx context.Context, request *csi.CreateSnapshotRequest) (*csi.CreateSnapshotResponse, error) {
	panic("implement me")
}

// DeleteSnapshot will be called by the CO to delete a snapshot.
func (d *Driver) DeleteSnapshot(ctx context.Context, request *csi.DeleteSnapshotRequest) (*csi.DeleteSnapshotResponse, error) {
	panic("implement me")
}

// ListSnapshots returns the information about all snapshots on the storage
// system within the given parameters regardless of how they were created.
// ListSnapshots shold not list a snapshot that is being created but has not
// been cut successfully yet.
func (d *Driver) ListSnapshots(ctx context.Context, request *csi.ListSnapshotsRequest) (*csi.ListSnapshotsResponse, error) {
	panic("implement me")
}

// ControllerExpandVolume is called from the resizer to increase the volume size.
func (d *Driver) ControllerExpandVolume(ctx context.Context, request *csi.ControllerExpandVolumeRequest) (*csi.ControllerExpandVolumeResponse, error) {
	panic("implement me")
}

// ControllerGetVolume gets a specific volume.
// The call is used for the CSI health check feature
// (https://github.com/kubernetes/enhancements/pull/1077) which we do not
// support yet.
func (d *Driver) ControllerGetVolume(ctx context.Context, request *csi.ControllerGetVolumeRequest) (*csi.ControllerGetVolumeResponse, error) {
	panic("implement me")
}

// GetDiskUri use to get azure disk uri from volume
func (d *Driver) GetDiskUri(volume string) string {
	return fmt.Sprintf("/subscriptions/%s/resourceGroups/%s/providers/Microsoft.Compute/disks/%s", d.az.SubscriptionID, d.az.ResourceGroup, volume)
}

// validateCapabilities validates the requested capabilities. It returns a list
// of violations which may be empty if no violatons were found.
func validateCapabilities(capabilitys []*csi.VolumeCapability) []string {
	violations := sets.NewString()
	for _, capability := range capabilitys {
		if capability.GetAccessMode().GetMode() != supportedAccessMode.GetMode() {
			violations.Insert(fmt.Sprintf("unsupported access mode %s", capability.GetAccessMode().GetMode().String()))
		}

		accessType := capability.GetAccessType()
		switch accessType.(type) {
		case *csi.VolumeCapability_Block:
		case *csi.VolumeCapability_Mount:
		default:
			violations.Insert("unsupported access type")
		}
	}

	return violations.List()
}

// extractStorage extracts the storage size in bytes from the given capacity
// range. If the capacity range is not satisfied it returns the default volume
// size. If the capacity range is below or above supported sizes, it returns an
// error.
func extractStorage(capRange *csi.CapacityRange) (int64, error) {
	if capRange == nil {
		return DefaultVolumeSizeInBytes, nil
	}
	requiredBytes := capRange.GetRequiredBytes()
	requiredSet := 0 < requiredBytes
	limitBytes := capRange.GetLimitBytes()
	limitSet := 0 < limitBytes

	if !requiredSet && !limitSet {
		return DefaultVolumeSizeInBytes, nil
	}

	if requiredSet && limitSet && limitBytes < requiredBytes {
		return 0, fmt.Errorf("limit (%v) can not be less than required (%v) size", formatBytes(limitBytes), formatBytes(requiredBytes))
	}

	if requiredSet && !limitSet && requiredBytes < MinimumVolumeSizeInBytes {
		return 0, fmt.Errorf("required (%v) can not be less than minimum supported volume size (%v)", formatBytes(requiredBytes), formatBytes(MinimumVolumeSizeInBytes))
	}

	if limitSet && limitBytes < MinimumVolumeSizeInBytes {
		return 0, fmt.Errorf("limit (%v) can not be less than minimum supported volume size (%v)", formatBytes(limitBytes), formatBytes(MinimumVolumeSizeInBytes))
	}

	if requiredSet && requiredBytes > MaximumVolumeSizeInBytes {
		return 0, fmt.Errorf("required (%v) can not exceed maximum supported volume size (%v)", formatBytes(requiredBytes), formatBytes(MaximumVolumeSizeInBytes))
	}

	if !requiredSet && limitSet && limitBytes > MaximumVolumeSizeInBytes {
		return 0, fmt.Errorf("limit (%v) can not exceed maximum supported volume size (%v)", formatBytes(limitBytes), formatBytes(MaximumVolumeSizeInBytes))
	}

	if requiredSet && limitSet && requiredBytes == limitBytes {
		return requiredBytes, nil
	}

	if requiredSet {
		return requiredBytes, nil
	}

	if limitSet {
		return limitBytes, nil
	}

	return DefaultVolumeSizeInBytes, nil
}

func formatBytes(inputBytes int64) string {
	output := float64(inputBytes)
	unit := ""

	switch {
	case inputBytes >= TiB:
		output = output / TiB
		unit = "Ti"
	case inputBytes >= GiB:
		output = output / GiB
		unit = "Gi"
	case inputBytes >= MiB:
		output = output / MiB
		unit = "Mi"
	case inputBytes >= KiB:
		output = output / KiB
		unit = "Ki"
	case inputBytes == 0:
		return "0"
	}

	result := strconv.FormatFloat(output, 'f', 1, 64)
	result = strings.TrimSuffix(result, ".0")
	return result + unit
}
