package driver

import (
	"context"
	"fmt"
	"github.com/Azure/azure-sdk-for-go/profiles/latest/compute/mgmt/compute"
	"github.com/Azure/go-autorest/autorest/to"
	"github.com/container-storage-interface/spec/lib/go/csi"
	"github.com/sirupsen/logrus"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"k8s.io/apimachinery/pkg/util/sets"
	"strconv"
	"strings"
	"time"
)

const (
	_   = iota
	kiB = 1 << (10 * iota)
	miB
	giB
	tiB
)

const (
	// minimumVolumeSizeInBytes is used to validate that the user is not trying
	// to create a volume that is smaller than what we support
	minimumVolumeSizeInBytes int64 = 1 * giB

	// maximumVolumeSizeInBytes is used to validate that the user is not trying
	// to create a volume that is larger than what we support
	maximumVolumeSizeInBytes int64 = 4 * tiB

	// defaultVolumeSizeInBytes is used when the user did not provide a size or
	// the size they provided did not satisfy our requirements
	defaultVolumeSizeInBytes int64 = 32 * giB

	// createdByDO is used to tag volumes that are created by this CSI plugin
	createdByAnanas = "Created by AzureDisk CSI driver"

	// doAPITimeout sets the timeout we will use when communicating with the
	// Digital Ocean API. NOTE: some queries inherit the context timeout
	doAPITimeout = 10 * time.Second

	// maxVolumesPerDropletErrorMessage is the error message returned by the DO
	// API when the per-droplet volume limit would be exceeded.
	maxVolumesPerDropletErrorMessage = "cannot attach more than 7 volumes to a single Droplet"
)

var (
	// Testing currently only supports a single node to be attached to a single node
	// in read/write mode. This corresponds to `accessModes.ReadWriteOnce` in a
	// PVC resource on Kubernetes
	supportedAccessMode = &csi.VolumeCapability_AccessMode{
		Mode: csi.VolumeCapability_AccessMode_SINGLE_NODE_WRITER,
	}
)

func (d *Driver) CreateVolume(ctx context.Context, req *csi.CreateVolumeRequest) (*csi.CreateVolumeResponse, error) {
	diskClient := d.az.NewDiskClient()
	authorizer, err := d.az.NewAzureAuthorizer()
	if err != nil {
		return nil, err
	}
	//Azure disk client

	diskClient.Authorizer = authorizer

	//Azure vm client
	if req.Name == "" {
		return nil, status.Error(codes.InvalidArgument, "CreateVolume Name must be provided")
	}

	if req.VolumeCapabilities == nil || len(req.VolumeCapabilities) == 0 {
		return nil, status.Error(codes.InvalidArgument, "CreateVolume Volume capabilities must be provided")
	}

	if violations := validateCapabilities(req.VolumeCapabilities); len(violations) > 0 {
		return nil, status.Error(codes.InvalidArgument, fmt.Sprintf("volume capabilities cannot be satisified: %s", strings.Join(violations, "; ")))
	}

	size, err := extractStorage(req.CapacityRange)
	if err != nil {
		return nil, status.Errorf(codes.OutOfRange, "invalid capacity range: %v", err)
	}
	volumeName := req.Name
	log := d.log.WithFields(logrus.Fields{
		"volume_name":             volumeName,
		"storage_size_giga_bytes": size / giB,
		"method":                  "create_volume",
		"volume_capabilities":     req.VolumeCapabilities,
		"location":                d.az.Location,
	})
	log.Info("create volume called")
	// get volume first, if it's created do no thing
	volume, err := diskClient.Get(ctx, d.az.ResourceGroup, volumeName)
	if err != nil && volume.StatusCode != 404 {
		return nil, status.Error(codes.Internal, err.Error())
	}
	if err == nil && volume.StatusCode == 200 {
		return nil, status.Errorf(codes.AlreadyExists, "fatal issue: duplicate volume %q exists", volumeName)
	}
	// create logic
	newVolume := d.az.NewAzureDisk(size, req.Name)
	future, err := diskClient.CreateOrUpdate(ctx, d.az.ResourceGroup, req.Name, newVolume)
	if err != nil {
		return nil, status.Error(codes.Internal, err.Error())
	}
	err = future.WaitForCompletionRef(ctx, diskClient.Client)
	if err != nil {
		return nil, status.Error(codes.Internal, err.Error())
	}
	resp := &csi.CreateVolumeResponse{
		Volume: &csi.Volume{
			VolumeId:      req.Name,
			CapacityBytes: size,
			AccessibleTopology: []*csi.Topology{
				{
					Segments: map[string]string{
						"location": d.az.Location,
					},
				},
			},
			VolumeContext: map[string]string{},
		},
	}
	log.WithField("response", future).Info("volume was created")
	return resp, nil

}

func (d *Driver) DeleteVolume(ctx context.Context, req *csi.DeleteVolumeRequest) (*csi.DeleteVolumeResponse, error) {

	if req.VolumeId == "" {
		return nil, status.Error(codes.InvalidArgument, "DeleteVolume Volume ID must be provided")
	}

	log := d.log.WithFields(logrus.Fields{
		"volume_id": req.VolumeId,
		"method":    "delete_volume",
	})
	log.Info("delete volume called")

	diskClient := d.az.NewDiskClient()
	authorizer, err := d.az.NewAzureAuthorizer()
	if err != nil {
		return nil, err
	}
	//Azure disk client
	diskClient.Authorizer = authorizer

	future, err := diskClient.Delete(ctx, d.az.ResourceGroup, req.GetVolumeId())
	if err != nil {
		return nil, status.Error(codes.Internal, err.Error())
	}
	err = future.WaitForCompletionRef(ctx, diskClient.Client)
	if err != nil {
		return nil, status.Error(codes.Internal, err.Error())
	}
	log.WithField("response", future).Info("volume was deleted")
	return &csi.DeleteVolumeResponse{}, nil
}

func (d *Driver) ControllerPublishVolume(ctx context.Context, req *csi.ControllerPublishVolumeRequest) (*csi.ControllerPublishVolumeResponse, error) {
	if req.VolumeId == "" {
		return nil, status.Error(codes.InvalidArgument, "ControllerPublishVolume Volume ID must be provided")
	}

	if req.NodeId == "" {
		return nil, status.Error(codes.InvalidArgument, "ControllerPublishVolume Node ID must be provided")
	}
	if req.VolumeCapability == nil {
		return nil, status.Error(codes.InvalidArgument, "ControllerPublishVolume Volume capability must be provided")
	}
	log := d.log.WithFields(logrus.Fields{
		"volume_id": req.VolumeId,
		"node_id":   req.NodeId,
		"method":    "controller_publish_volume",
	})
	log.Info("controller publish volume called")

	vmClient := d.az.NewVmClient()
	authorizer, err := d.az.NewAzureAuthorizer()
	if err != nil {
		return nil, err
	}
	//Azure disk client
	vmClient.Authorizer = authorizer
	future, err := vmClient.Get(ctx, d.az.ResourceGroup, req.NodeId, compute.InstanceView)
	if err != nil {
		return nil, status.Error(codes.Internal, "Failed to get request node info")
	}
	lun := new(int32)
	dataDisks := *future.StorageProfile.DataDisks
	if len(dataDisks) == 0 {
		*lun = 0
	} else {
		*lun = *dataDisks[len(dataDisks)-1].Lun + 1
	}
	diskToBeAttach := compute.DataDisk{
		Lun:          lun,
		Name:         to.StringPtr(req.GetVolumeId()),
		CreateOption: compute.DiskCreateOptionTypesAttach,
		ManagedDisk: &compute.ManagedDiskParameters{
			StorageAccountType: compute.StorageAccountTypesPremiumLRS,
			ID: to.StringPtr(fmt.Sprintf("/subscriptions/%s/resourceGroups/%s/providers/Microsoft.Compute/disks/%s",
				d.az.SubscriptionId,
				d.az.ResourceGroup,
				req.GetVolumeId())),
		},
	}
	dataDisks = append(dataDisks, diskToBeAttach)
	*future.StorageProfile.DataDisks = dataDisks
	vmCli, err := vmClient.CreateOrUpdate(ctx, d.az.ResourceGroup, req.GetNodeId(), future)
	if err != nil {
		return nil, status.Error(codes.Internal, err.Error())
	}

	err = vmCli.WaitForCompletionRef(ctx, vmClient.Client)
	if err != nil {
		return nil, status.Error(codes.Internal, err.Error())
	}

	log.Info("attach success")
	return &csi.ControllerPublishVolumeResponse{
		PublishContext: map[string]string{
			req.VolumeId: strconv.Itoa(int(*lun)),
		},
	}, nil
}

func (d *Driver) ControllerUnpublishVolume(ctx context.Context, req *csi.ControllerUnpublishVolumeRequest) (*csi.ControllerUnpublishVolumeResponse, error) {
	if req.VolumeId == "" {
		return nil, status.Error(codes.InvalidArgument, "ControllerUnpublishVolume Volume ID must be provided")
	}

	if req.NodeId == "" {
		return nil, status.Error(codes.InvalidArgument, "ControllerUnpublishVolume Node ID %q must be provided")
	}
	log := d.log.WithFields(logrus.Fields{
		"volume_id": req.VolumeId,
		"node_id":   req.NodeId,
		"method":    "controller_unpublish_volume",
	})
	log.Info("controller unpublish volume called")
	vmClient := d.az.NewVmClient()
	authorizer, err := d.az.NewAzureAuthorizer()
	if err != nil {
		return nil, err
	}
	//Azure disk client
	vmClient.Authorizer = authorizer
	future, err := vmClient.Get(ctx, d.az.ResourceGroup, req.NodeId, compute.InstanceView)
	if err != nil {
		return nil, status.Error(codes.Internal, "Failed to get request node info")
	}
	oldDataDisks := *future.StorageProfile.DataDisks
	newDataDisks := make([]compute.DataDisk, 0)
	for _, disk := range oldDataDisks {
		if *disk.Name != req.GetVolumeId() {
			newDataDisks = append(newDataDisks, disk)
		}
	}
	*future.StorageProfile.DataDisks = newDataDisks
	vmCli, err := vmClient.CreateOrUpdate(ctx, d.az.ResourceGroup, req.GetNodeId(), future)
	if err != nil {
		return nil, status.Error(codes.Internal, err.Error())
	}

	err = vmCli.WaitForCompletionRef(ctx, vmClient.Client)
	if err != nil {
		return nil, status.Error(codes.Internal, err.Error())
	}

	log.Info("detach success")
	return &csi.ControllerUnpublishVolumeResponse{}, nil
}

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
	diskClient := d.az.NewDiskClient()
	authorizer, err := d.az.NewAzureAuthorizer()
	if err != nil {
		return nil, err
	}
	//Azure disk client
	diskClient.Authorizer = authorizer
	get, err := diskClient.Get(ctx, d.az.ResourceGroup, req.GetVolumeId())
	if err != nil {
		if get.StatusCode == 404 {
			return nil, status.Errorf(codes.NotFound, "volume %q does not exist", req.VolumeId)
		}
		return nil, status.Error(codes.Internal, err.Error())
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

func (d *Driver) ListVolumes(ctx context.Context, request *csi.ListVolumesRequest) (*csi.ListVolumesResponse, error) {
	panic("implement me")
}

func (d *Driver) GetCapacity(ctx context.Context, request *csi.GetCapacityRequest) (*csi.GetCapacityResponse, error) {
	panic("implement me")
}

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

func (d *Driver) CreateSnapshot(ctx context.Context, request *csi.CreateSnapshotRequest) (*csi.CreateSnapshotResponse, error) {
	panic("implement me")
}

func (d *Driver) DeleteSnapshot(ctx context.Context, request *csi.DeleteSnapshotRequest) (*csi.DeleteSnapshotResponse, error) {
	panic("implement me")
}

func (d *Driver) ListSnapshots(ctx context.Context, request *csi.ListSnapshotsRequest) (*csi.ListSnapshotsResponse, error) {
	panic("implement me")
}

func (d *Driver) ControllerExpandVolume(ctx context.Context, request *csi.ControllerExpandVolumeRequest) (*csi.ControllerExpandVolumeResponse, error) {
	panic("implement me")
}

func (d *Driver) ControllerGetVolume(ctx context.Context, request *csi.ControllerGetVolumeRequest) (*csi.ControllerGetVolumeResponse, error) {
	panic("implement me")
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
		return defaultVolumeSizeInBytes, nil
	}
	requiredBytes := capRange.GetRequiredBytes()
	requiredSet := 0 < requiredBytes
	limitBytes := capRange.GetLimitBytes()
	limitSet := 0 < limitBytes

	if !requiredSet && !limitSet {
		return defaultVolumeSizeInBytes, nil
	}

	if requiredSet && limitSet && limitBytes < requiredBytes {
		return 0, fmt.Errorf("limit (%v) can not be less than required (%v) size", formatBytes(limitBytes), formatBytes(requiredBytes))
	}

	if requiredSet && !limitSet && requiredBytes < minimumVolumeSizeInBytes {
		return 0, fmt.Errorf("required (%v) can not be less than minimum supported volume size (%v)", formatBytes(requiredBytes), formatBytes(minimumVolumeSizeInBytes))
	}

	if limitSet && limitBytes < minimumVolumeSizeInBytes {
		return 0, fmt.Errorf("limit (%v) can not be less than minimum supported volume size (%v)", formatBytes(limitBytes), formatBytes(minimumVolumeSizeInBytes))
	}

	if requiredSet && requiredBytes > maximumVolumeSizeInBytes {
		return 0, fmt.Errorf("required (%v) can not exceed maximum supported volume size (%v)", formatBytes(requiredBytes), formatBytes(maximumVolumeSizeInBytes))
	}

	if !requiredSet && limitSet && limitBytes > maximumVolumeSizeInBytes {
		return 0, fmt.Errorf("limit (%v) can not exceed maximum supported volume size (%v)", formatBytes(limitBytes), formatBytes(maximumVolumeSizeInBytes))
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

	return defaultVolumeSizeInBytes, nil
}

func formatBytes(inputBytes int64) string {
	output := float64(inputBytes)
	unit := ""

	switch {
	case inputBytes >= tiB:
		output = output / tiB
		unit = "Ti"
	case inputBytes >= giB:
		output = output / giB
		unit = "Gi"
	case inputBytes >= miB:
		output = output / miB
		unit = "Mi"
	case inputBytes >= kiB:
		output = output / kiB
		unit = "Ki"
	case inputBytes == 0:
		return "0"
	}

	result := strconv.FormatFloat(output, 'f', 1, 64)
	result = strings.TrimSuffix(result, ".0")
	return result + unit
}
