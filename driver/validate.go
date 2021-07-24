package driver

import (
	"github.com/container-storage-interface/spec/lib/go/csi"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"os"
)

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

// checkDirExists checks directory  exists or not.
func checkDirExists(p string) bool {
	if _, err := os.Stat(p); os.IsNotExist(err) {
		return false
	}

	return true
}
