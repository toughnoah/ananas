package driver

import (
	"context"
	"fmt"
	"github.com/container-storage-interface/spec/lib/go/csi"
	"github.com/sirupsen/logrus"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"k8s.io/kubernetes/pkg/volume"
	"k8s.io/kubernetes/pkg/volume/util/hostutil"
	"k8s.io/utils/exec"
	"os"
	"strconv"
)

const (
	_volumeModeBlock         = "block"
	_volumeModeFilesystem    = "filesystem"
	_defaultAzureVolumeLimit = 16
)

func (d *Driver) NodeStageVolume(ctx context.Context, req *csi.NodeStageVolumeRequest) (*csi.NodeStageVolumeResponse, error) {

	if err := ValidateNodeStageVolumeRequest(req); err != nil {
		return nil, err
	}
	switch req.VolumeCapability.GetAccessType().(type) {
	case *csi.VolumeCapability_Block:
		return &csi.NodeStageVolumeResponse{}, nil
	}

	volumeID := req.GetVolumeId()
	if acquired := d.volumeLocks.TryAcquire(volumeID); !acquired {
		return nil, status.Errorf(codes.Aborted, "An operation with the given Volume ID %s already exists", volumeID)
	}
	defer d.volumeLocks.Release(volumeID)

	Lun, ok := req.PublishContext[req.GetVolumeId()]
	if !ok {
		return nil, status.Error(codes.InvalidArgument, fmt.Sprintf("Could not find the lun number from the publish context %q", Lun))
	}
	lun, err := strconv.Atoi(Lun)
	if err != nil {
		return nil, status.Errorf(codes.Internal, err.Error())
	}
	devPath, err := findDeviceAttached(lun)
	if err != nil {
		return nil, status.Error(codes.Internal, err.Error())
	}

	mnt := req.VolumeCapability.GetMount()
	options := mnt.MountFlags
	fsType := "ext4"
	if mnt.FsType != "" {
		fsType = mnt.FsType
	}

	log := d.log.WithFields(logrus.Fields{
		"volume_mode":     "block",
		"volume_name":     req.GetVolumeId(),
		"volume_context":  req.VolumeContext,
		"publish_context": req.PublishContext,
		"dev_path":        devPath,
		"fs_type":         fsType,
		"mount_options":   options,
	})
	staging := req.StagingTargetPath

	mounted, err := d.mounter.IsMounted(staging)
	if err != nil {
		return nil, status.Error(codes.Internal, err.Error())
	}

	d.log.WithFields(logrus.Fields{
		"cmd":         "blkid",
		"device path": devPath,
	}).Info("checking if source is formatted")
	formatted, err := d.mounter.IsFormatted(devPath)
	if err != nil {
		return nil, status.Error(codes.Internal, err.Error())
	}
	if !formatted {
		d.log.WithFields(logrus.Fields{
			"cmd":  "mkfs",
			"args": fsType,
		}).Info("executing format command")
		if err := d.mounter.Format(devPath, fsType); err != nil {
			return nil, status.Error(codes.Internal, err.Error())
		}
	} else {
		log.Info("source device is already formatted")
	}
	log.Info("mounting the volume for staging")
	if !mounted {
		if err := d.mounter.Mount(devPath, staging, fsType, options...); err != nil {
			return nil, status.Error(codes.Internal, err.Error())
		}
	} else {
		log.Info("source device is already mounted to the target path")
	}

	log.Info("formatting and mounting stage volume is finished")
	return &csi.NodeStageVolumeResponse{}, nil

}

func (d *Driver) NodeUnstageVolume(ctx context.Context, req *csi.NodeUnstageVolumeRequest) (*csi.NodeUnstageVolumeResponse, error) {
	volumeID := req.GetVolumeId()
	if err := ValidateValidateNodeUnStageVolumeRequest(volumeID, req); err != nil {
		return nil, err
	}
	if acquired := d.volumeLocks.TryAcquire(volumeID); !acquired {
		return nil, status.Errorf(codes.Aborted, "An operation with the given Volume ID %s already exists", volumeID)
	}
	defer d.volumeLocks.Release(volumeID)
	log := d.log.WithFields(logrus.Fields{
		"volume_id":           req.VolumeId,
		"staging_target_path": req.StagingTargetPath,
		"method":              "node_unstage_volume",
	})
	log.Info("node unstage volume called")

	d.log.WithFields(logrus.Fields{
		"cmd":                 "findmnt",
		"staging_target_path": req.StagingTargetPath,
	}).Info("checking if target is mounted")
	mounted, err := d.mounter.IsMounted(req.StagingTargetPath)
	if err != nil {
		return nil, err
	}

	if mounted {
		d.log.WithFields(logrus.Fields{
			"cmd":  "umount",
			"args": req.StagingTargetPath,
		}).Info("executing umount command")
		err = d.mounter.Unmount(req.StagingTargetPath)
		if err != nil {
			return nil, err
		}
	} else {
		log.Info("staging target path is already unmounted")
	}

	log.Info("unmounting stage volume is finished")

	return &csi.NodeUnstageVolumeResponse{}, nil
}

func (d *Driver) NodePublishVolume(ctx context.Context, req *csi.NodePublishVolumeRequest) (*csi.NodePublishVolumeResponse, error) {
	var err error
	if err = ValidateNodePublishVolumeRequest(req); err != nil {
		return nil, err
	}

	log := d.log.WithFields(logrus.Fields{
		"volume_id":           req.VolumeId,
		"staging_target_path": req.StagingTargetPath,
		"target_path":         req.TargetPath,
		"method":              "node_publish_volume",
	})
	log.Info("node publish volume called")

	options := []string{"bind"}
	if req.Readonly {
		options = append(options, "ro")
	}

	switch req.GetVolumeCapability().GetAccessType().(type) {
	case *csi.VolumeCapability_Block:
		err = d.nodePublishVolumeForBlock(req, options, log)
	case *csi.VolumeCapability_Mount:
		err = d.nodePublishVolumeForFileSystem(req, options, log)
	default:
		return nil, status.Error(codes.InvalidArgument, "Unknown access type")
	}

	if err != nil {
		return nil, err
	}

	log.Info("bind mounting the volume is finished")
	return &csi.NodePublishVolumeResponse{}, nil
}

func (d *Driver) NodeUnpublishVolume(ctx context.Context, req *csi.NodeUnpublishVolumeRequest) (*csi.NodeUnpublishVolumeResponse, error) {
	targetPath := req.GetTargetPath()
	volumeID := req.GetVolumeId()

	if len(volumeID) == 0 {
		return nil, status.Error(codes.InvalidArgument, "Volume ID missing in request")
	}
	if len(targetPath) == 0 {
		return nil, status.Error(codes.InvalidArgument, "Target path missing in request")
	}
	log := d.log.WithFields(logrus.Fields{
		"volume_id":   req.VolumeId,
		"target_path": req.TargetPath,
		"method":      "node_unpublish_volume",
	})
	log.Info("node unpublish volume called")

	d.log.WithFields(logrus.Fields{
		"volume_id":           req.VolumeId,
		"staging_target_path": req.TargetPath,
	}).Info("checking if target is mounted")
	mounted, err := d.mounter.IsMounted(req.TargetPath)
	if err != nil {
		return nil, status.Error(codes.Internal, "Failed to check mount point")
	}

	if mounted {
		d.log.WithFields(logrus.Fields{
			"cmd":  "umount",
			"args": req.TargetPath,
		}).Info("executing umount command")
		err := d.mounter.Unmount(req.TargetPath)
		if err != nil {
			return nil, status.Error(codes.Internal, "Failed to unmout target path")
		}
	} else {
		log.Info("target path is already unmounted")
	}
	_, err = os.Stat(targetPath)
	if err == nil {
		os.Remove(targetPath)
	}

	log.Info("unmounting volume is finished")
	return &csi.NodeUnpublishVolumeResponse{}, nil
}

func (d *Driver) NodeGetVolumeStats(ctx context.Context, req *csi.NodeGetVolumeStatsRequest) (*csi.NodeGetVolumeStatsResponse, error) {
	if len(req.VolumeId) == 0 {
		return nil, status.Error(codes.InvalidArgument, "NodeGetVolumeStats volume ID was empty")
	}
	if len(req.VolumePath) == 0 {
		return nil, status.Error(codes.InvalidArgument, "NodeGetVolumeStats volume path was empty")
	}

	_, err := os.Stat(req.VolumePath)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, status.Errorf(codes.NotFound, "path %s does not exist", req.VolumePath)
		}
		return nil, status.Errorf(codes.Internal, "failed to stat file %s: %v", req.VolumePath, err)
	}

	isBlock, err := hostutil.NewHostUtil().PathIsDevice(req.VolumePath)
	if err != nil {
		return nil, status.Errorf(codes.NotFound, "failed to determine whether %s is block device: %v", req.VolumePath, err)
	}
	if isBlock {
		bcap, err := d.mounter.GetStatistics(req.GetVolumePath())
		if err != nil {
			return nil, status.Errorf(codes.Internal, "failed to get block capacity on path %s: %v", req.VolumePath, err)
		}
		return &csi.NodeGetVolumeStatsResponse{
			Usage: []*csi.VolumeUsage{
				{
					Unit:  csi.VolumeUsage_BYTES,
					Total: bcap.TotalBytes,
				},
			},
		}, nil
	}

	volumeMetrics, err := volume.NewMetricsStatFS(req.VolumePath).GetMetrics()
	if err != nil {
		return nil, err
	}
	available, ok := volumeMetrics.Available.AsInt64()
	if !ok {
		return nil, status.Errorf(codes.Internal, "failed to transform volume available size(%v)", volumeMetrics.Available)
	}
	capacity, ok := volumeMetrics.Capacity.AsInt64()
	if !ok {
		return nil, status.Errorf(codes.Internal, "failed to transform volume capacity size(%v)", volumeMetrics.Capacity)
	}
	used, ok := volumeMetrics.Used.AsInt64()
	if !ok {
		return nil, status.Errorf(codes.Internal, "failed to transform volume used size(%v)", volumeMetrics.Used)
	}

	inodesFree, ok := volumeMetrics.InodesFree.AsInt64()
	if !ok {
		return nil, status.Errorf(codes.Internal, "failed to transform disk inodes free(%v)", volumeMetrics.InodesFree)
	}
	inodes, ok := volumeMetrics.Inodes.AsInt64()
	if !ok {
		return nil, status.Errorf(codes.Internal, "failed to transform disk inodes(%v)", volumeMetrics.Inodes)
	}
	inodesUsed, ok := volumeMetrics.InodesUsed.AsInt64()
	if !ok {
		return nil, status.Errorf(codes.Internal, "failed to transform disk inodes used(%v)", volumeMetrics.InodesUsed)
	}

	return &csi.NodeGetVolumeStatsResponse{
		Usage: []*csi.VolumeUsage{
			{
				Unit:      csi.VolumeUsage_BYTES,
				Available: available,
				Total:     capacity,
				Used:      used,
			},
			{
				Unit:      csi.VolumeUsage_INODES,
				Available: inodesFree,
				Total:     inodes,
				Used:      inodesUsed,
			},
		},
	}, nil
}

func (d *Driver) NodeExpandVolume(ctx context.Context, request *csi.NodeExpandVolumeRequest) (*csi.NodeExpandVolumeResponse, error) {
	panic("implement me")
}

func (d *Driver) NodeGetCapabilities(ctx context.Context, request *csi.NodeGetCapabilitiesRequest) (*csi.NodeGetCapabilitiesResponse, error) {
	caps := []*csi.NodeServiceCapability{
		{
			Type: &csi.NodeServiceCapability_Rpc{
				Rpc: &csi.NodeServiceCapability_RPC{
					Type: csi.NodeServiceCapability_RPC_STAGE_UNSTAGE_VOLUME,
				},
			},
		},
		{
			Type: &csi.NodeServiceCapability_Rpc{
				Rpc: &csi.NodeServiceCapability_RPC{
					Type: csi.NodeServiceCapability_RPC_GET_VOLUME_STATS,
				},
			},
		},
		{
			Type: &csi.NodeServiceCapability_Rpc{
				Rpc: &csi.NodeServiceCapability_RPC{
					Type: csi.NodeServiceCapability_RPC_UNKNOWN,
				},
			},
		},
	}

	d.log.WithFields(logrus.Fields{
		"node_capabilities": caps,
		"method":            "node_get_capabilities",
	}).Info("node get capabilities called")
	return &csi.NodeGetCapabilitiesResponse{
		Capabilities: caps,
	}, nil
}

func (d *Driver) NodeGetInfo(ctx context.Context, request *csi.NodeGetInfoRequest) (*csi.NodeGetInfoResponse, error) {
	d.log.WithField("method", "node_get_info").Info("node get info called")
	return &csi.NodeGetInfoResponse{
		NodeId:            d.nodeId,
		MaxVolumesPerNode: _defaultAzureVolumeLimit,

		// make sure that the driver works on this particular region only
		AccessibleTopology: &csi.Topology{
			Segments: map[string]string{
				"location": d.az.Location,
			},
		},
	}, nil
}

func (d *Driver) nodePublishVolumeForFileSystem(req *csi.NodePublishVolumeRequest, mountOptions []string, log *logrus.Entry) error {
	source := req.StagingTargetPath
	target := req.TargetPath

	mnt := req.VolumeCapability.GetMount()
	mountOptions = append(mountOptions, mnt.MountFlags...)

	fsType := "ext4"
	if mnt.FsType != "" {
		fsType = mnt.FsType
	}

	mounted, err := d.mounter.IsMounted(target)
	if err != nil {
		return err
	}

	log = log.WithFields(logrus.Fields{
		"source_path":   source,
		"volume_mode":   _volumeModeFilesystem,
		"fs_type":       fsType,
		"mount_options": mountOptions,
	})

	if !mounted {
		d.log.WithFields(logrus.Fields{
			"cmd":    "mount",
			"source": source,
			"dest":   target,
		}).Info("executing mount command")
		if err := d.mounter.Mount(source, target, fsType, mountOptions...); err != nil {
			return status.Error(codes.Internal, err.Error())
		}
	} else {
		log.Info("volume is already mounted")
	}

	return nil
}

func (d *Driver) nodePublishVolumeForBlock(req *csi.NodePublishVolumeRequest, mountOptions []string, log *logrus.Entry) error {
	lunString, ok := req.PublishContext[req.GetVolumeId()]
	if !ok {
		return status.Error(codes.InvalidArgument, fmt.Sprintf("Could not find the lun number from the publish context %q", lunString))
	}
	lun, err := strconv.Atoi(lunString)
	if err != nil {
		return status.Errorf(codes.Internal, err.Error())
	}
	source, err := findDeviceAttached(lun)
	if err != nil {
		return status.Errorf(codes.Internal, "Failed to find device path for volume %s. %v", req.GetVolumeId(), err)
	}

	target := req.TargetPath

	mounted, err := d.mounter.IsMounted(target)
	if err != nil {
		return err
	}

	log = log.WithFields(logrus.Fields{
		"source_path":   source,
		"volume_mode":   _volumeModeBlock,
		"mount_options": mountOptions,
	})

	if !mounted {
		d.log.WithFields(logrus.Fields{
			"cmd":    "mount",
			"source": source,
			"dest":   target,
		}).Info("executing mount command")
		if err := d.mounter.Mount(source, target, "", mountOptions...); err != nil {
			return status.Errorf(codes.Internal, err.Error())
		}
	} else {
		log.Info("volume is already mounted")
	}

	return nil
}

func findDeviceAttached(lun int) (string, error) {
	io := NewIOHandler()
	return findDiskByLun(lun, io, exec.New())
}
