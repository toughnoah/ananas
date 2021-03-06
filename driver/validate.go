package driver

import (
	"fmt"
	"github.com/container-storage-interface/spec/lib/go/csi"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"os"
	"strings"
)

// ValidateCreateVolume validates the create volume request.
func ValidateCreateVolume(req *csi.CreateVolumeRequest) (int64, error) {
	if req.Name == "" {
		return 0, status.Error(codes.InvalidArgument, "CreateVolume Name must be provided")
	}

	if req.VolumeCapabilities == nil || len(req.VolumeCapabilities) == 0 {
		return 0, status.Error(codes.InvalidArgument, "CreateVolume Volume capabilities must be provided")
	}

	if violations := validateCapabilities(req.VolumeCapabilities); len(violations) > 0 {
		return 0, status.Error(codes.InvalidArgument, fmt.Sprintf("volume capabilities cannot be satisified: %s", strings.Join(violations, "; ")))
	}

	size, err := extractStorage(req.CapacityRange)
	if err != nil {
		return 0, status.Errorf(codes.OutOfRange, "invalid capacity range: %v", err)
	}
	return size, nil
}

// ValidateNodeStageVolumeRequest validates the node stage request.
func ValidateNodeStageVolumeRequest(req *csi.NodeStageVolumeRequest) error {
	if req.GetVolumeCapability() == nil {
		return status.Error(codes.InvalidArgument, "volume capability missing in request")
	}

	if req.GetVolumeId() == "" {
		return status.Error(codes.InvalidArgument, "volume ID missing in request")
	}

	if req.GetStagingTargetPath() == "" {
		return status.Error(codes.InvalidArgument, "staging target path missing in request")
	}

	ok := checkDirExists(req.GetStagingTargetPath())
	if !ok {
		return status.Errorf(
			codes.InvalidArgument,
			"staging path %s does not exist on node",
			req.GetStagingTargetPath())
	}

	return nil
}

// ValidateNodePublishVolumeRequest validates the node publish volume request.
func ValidateNodePublishVolumeRequest(req *csi.NodePublishVolumeRequest) error {
	if req.GetVolumeCapability() == nil {
		return status.Error(codes.InvalidArgument, "volume capability missing in request")
	}

	if req.GetVolumeId() == "" {
		return status.Error(codes.InvalidArgument, "volume ID missing in request")
	}

	if req.GetStagingTargetPath() == "" {
		return status.Error(codes.InvalidArgument, "staging target path missing in request")
	}

	if req.GetTargetPath() == "" {
		return status.Error(codes.InvalidArgument, "target path missing in request")
	}

	ok := checkDirExists(req.GetStagingTargetPath())
	if !ok {
		return status.Errorf(
			codes.InvalidArgument,
			"staging path %s does not exist on node",
			req.GetStagingTargetPath())
	}

	return nil
}

// ValidateValidateNodeUnStageVolumeRequest validates the node unstage volume request.
func ValidateValidateNodeUnStageVolumeRequest(volumeID string, req *csi.NodeUnstageVolumeRequest) error {

	if len(volumeID) == 0 {
		return status.Error(codes.InvalidArgument, "Volume ID not provided")
	}

	stagingTargetPath := req.GetStagingTargetPath()
	if len(stagingTargetPath) == 0 {
		return status.Error(codes.InvalidArgument, "Staging target not provided")
	}
	return nil
}

// ValidateControllerPublishVolume validates the controller publish volume request.
func ValidateControllerPublishVolume(req *csi.ControllerPublishVolumeRequest) error {
	if req.VolumeId == "" {
		return status.Error(codes.InvalidArgument, "ControllerPublishVolume Volume ID must be provided")
	}

	if req.NodeId == "" {
		return status.Error(codes.InvalidArgument, "ControllerPublishVolume Node ID must be provided")
	}
	if req.VolumeCapability == nil {
		return status.Error(codes.InvalidArgument, "ControllerPublishVolume Volume capability must be provided")
	}
	return nil
}

// ValidateControllerUnPublishVolume validates the controller unpublish volume request.
func ValidateControllerUnPublishVolume(req *csi.ControllerUnpublishVolumeRequest) error {
	if req.VolumeId == "" {
		return status.Error(codes.InvalidArgument, "ControllerUnpublishVolume Volume ID must be provided")
	}

	if req.NodeId == "" {
		return status.Error(codes.InvalidArgument, "ControllerUnpublishVolume Node ID %q must be provided")
	}
	return nil
}

// ValidateNodeUnPublishVolume validates the node unpublish volume request.
func ValidateNodeUnPublishVolume(req *csi.NodeUnpublishVolumeRequest) error {
	if len(req.VolumeId) == 0 {
		return status.Error(codes.InvalidArgument, "Volume ID missing in request")
	}
	if len(req.TargetPath) == 0 {
		return status.Error(codes.InvalidArgument, "Target path missing in request")
	}
	return nil
}

// ValidateCreateSnapshot validates the snapshot request
func ValidateCreateSnapshot(req *csi.CreateSnapshotRequest) error {
	volumeId := req.GetSourceVolumeId()
	if len(volumeId) == 0 {
		return status.Error(codes.InvalidArgument, "CreateSnapshot Source Volume ID must be provided")
	}
	snapshotName := req.Name
	if len(snapshotName) == 0 {
		return status.Error(codes.InvalidArgument, "snapshot name must be provided")
	}
	return nil
}

// checkDirExists checks directory  exists or not.
func checkDirExists(p string) bool {
	if _, err := os.Stat(p); os.IsNotExist(err) {
		return false
	}
	return true
}
