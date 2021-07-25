package driver

import (
	"encoding/json"
	"errors"
	"fmt"
	"github.com/sirupsen/logrus"
	"io/ioutil"
	"k8s.io/klog/v2"
	utilexec "k8s.io/utils/exec"
	"k8s.io/utils/mount"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
)

const (
	// blkidExitStatusNoIdentifiers defines the exit code returned from blkid indicating that no devices have been found. See http://www.polarhome.com/service/man/?qf=blkid&tf=2&of=Alpinelinux for details.
	blkidExitStatusNoIdentifiers = 2
)

var _ IoHandler = &osIOHandler{}

var _ Mounter = &mounter{}

func findDiskByLun(lun int, io IoHandler, exec utilexec.Interface) (string, error) {
	azureDisks := listAzureDiskPath(io)
	return findDiskByLunWithConstraint(lun, io, azureDisks)
}

// finds a device mounted to "current" node
func findDiskByLunWithConstraint(lun int, io IoHandler, azureDisks []string) (string, error) {
	var err error
	sysPath := "/sys/bus/scsi/devices"
	if dirs, err := io.ReadDir(sysPath); err == nil {
		for _, f := range dirs {
			name := f.Name()
			// look for path like /sys/bus/scsi/devices/3:0:0:1
			arr := strings.Split(name, ":")
			if len(arr) < 4 {
				continue
			}
			if len(azureDisks) == 0 {
				klog.V(4).Infof("/dev/disk/azure is not populated, now try to parse %v directly", name)
				target, err := strconv.Atoi(arr[0])
				if err != nil {
					klog.Errorf("failed to parse target from %v (%v), err %v", arr[0], name, err)
					continue
				}
				// as observed, targets 0-3 are used by OS disks. Skip them
				if target <= 3 {
					continue
				}
			}

			// extract LUN from the path.
			// LUN is the last index of the array, i.e. 1 in /sys/bus/scsi/devices/3:0:0:1
			l, err := strconv.Atoi(arr[3])
			if err != nil {
				// unknown path format, continue to read the next one
				klog.V(4).Infof("azure disk - failed to parse lun from %v (%v), err %v", arr[3], name, err)
				continue
			}
			if lun == l {
				// find the matching LUN
				// read vendor and model to ensure it is a VHD disk
				vendorPath := filepath.Join(sysPath, name, "vendor")
				vendorBytes, err := io.ReadFile(vendorPath)
				if err != nil {
					klog.Errorf("failed to read device vendor, err: %v", err)
					continue
				}
				vendor := strings.TrimSpace(string(vendorBytes))
				if strings.ToUpper(vendor) != "MSFT" {
					klog.V(4).Infof("vendor doesn't match VHD, got %s", vendor)
					continue
				}

				modelPath := filepath.Join(sysPath, name, "model")
				modelBytes, err := io.ReadFile(modelPath)
				if err != nil {
					klog.Errorf("failed to read device model, err: %v", err)
					continue
				}
				model := strings.TrimSpace(string(modelBytes))
				if strings.ToUpper(model) != "VIRTUAL DISK" {
					klog.V(4).Infof("model doesn't match VIRTUAL DISK, got %s", model)
					continue
				}

				// find a disk, validate name
				dir := filepath.Join(sysPath, name, "block")
				if dev, err := io.ReadDir(dir); err == nil {
					found := false
					devName := dev[0].Name()
					for _, diskName := range azureDisks {
						klog.V(12).Infof("azureDisk - validating disk %q with sys disk %q", devName, diskName)
						if devName == diskName {
							found = true
							break
						}
					}
					if !found {
						devLinkPaths := []string{"/dev/disk/azure/scsi1/", "/dev/disk/by-id/"}
						for _, devLinkPath := range devLinkPaths {
							diskPath, err := getDiskLinkByDevName(io, devLinkPath, devName)
							if err == nil {
								klog.V(4).Infof("azureDisk - found %s by %s under %s", diskPath, devName, devLinkPath)
								return diskPath, nil
							}
							klog.Warningf("azureDisk - getDiskLinkByDevName by %s under %s failed, error: %v", devName, devLinkPath, err)
						}
						return "/dev/" + devName, nil
					}
				}
			}
		}
	}
	return "", err
}

// exclude those used by azure as resource and OS root in /dev/disk/azure, /dev/disk/azure/scsi0
// "/dev/disk/azure/scsi0" dir is populated in Standard_DC4s/DC2s on Ubuntu 18.04
func listAzureDiskPath(io IoHandler) []string {
	var azureDiskList []string
	azureResourcePaths := []string{"/dev/disk/azure/", "/dev/disk/azure/scsi0/"}
	for _, azureDiskPath := range azureResourcePaths {
		if dirs, err := io.ReadDir(azureDiskPath); err == nil {
			for _, f := range dirs {
				name := f.Name()
				diskPath := filepath.Join(azureDiskPath, name)
				if link, linkErr := io.Readlink(diskPath); linkErr == nil {
					sd := link[(strings.LastIndex(link, "/") + 1):]
					azureDiskList = append(azureDiskList, sd)
				}
			}
		}
	}
	klog.V(12).Infof("Azure sys disks paths: %v", azureDiskList)
	return azureDiskList
}

// getDiskLinkByDevName get disk link by device name from devLinkPath, e.g. /dev/disk/azure/, /dev/disk/by-id/
func getDiskLinkByDevName(io IoHandler, devLinkPath, devName string) (string, error) {
	dirs, err := io.ReadDir(devLinkPath)
	klog.V(12).Infof("azureDisk - begin to find %s from %s", devName, devLinkPath)
	if err == nil {
		for _, f := range dirs {
			diskPath := devLinkPath + f.Name()
			klog.V(12).Infof("azureDisk - begin to Readlink: %s", diskPath)
			link, linkErr := io.Readlink(diskPath)
			if linkErr != nil {
				klog.Warningf("azureDisk - read link (%s) error: %v", diskPath, linkErr)
				continue
			}
			if strings.HasSuffix(link, devName) {
				return diskPath, nil
			}
		}
		return "", fmt.Errorf("device name(%s) is not found under %s", devName, devLinkPath)
	}
	return "", fmt.Errorf("read %s error: %v", devLinkPath, err)
}

type IoHandler interface {
	ReadDir(dirname string) ([]os.FileInfo, error)
	WriteFile(filename string, data []byte, perm os.FileMode) error
	Readlink(name string) (string, error)
	ReadFile(filename string) ([]byte, error)
}

//TODO: check if priming the iscsi interface is actually needed

type osIOHandler struct{}

//NewIOHandler Create a new IoHandler implementation
func NewIOHandler() IoHandler {
	return &osIOHandler{}
}

func (handler *osIOHandler) ReadDir(dirname string) ([]os.FileInfo, error) {
	return ioutil.ReadDir(dirname)
}

func (handler *osIOHandler) WriteFile(filename string, data []byte, perm os.FileMode) error {
	return ioutil.WriteFile(filename, data, perm)
}

func (handler *osIOHandler) Readlink(name string) (string, error) {
	return os.Readlink(name)
}

func (handler *osIOHandler) ReadFile(filename string) ([]byte, error) {
	return ioutil.ReadFile(filename)
}

type volumeStatistics struct {
	//availableBytes,
	totalBytes int64
	//usedBytes    int64
	//availableInodes,
	//totalInodes,
	//usedInodes int64
}

type Mounter interface {
	// Format formats the source with the given filesystem type
	Format(source, fsType string) error

	// Mount mounts source to target with the given fstype and options.
	Mount(source, target, fsType string, options ...string) error

	// Unmount unmounts the given target
	Unmount(target string) error

	// IsFormatted checks whether the source device is formatted or not. It
	// returns true if the source device is already formatted.
	IsFormatted(source string) (bool, error)

	// IsMounted checks whether the target path is a correct mount (i.e:
	// propagated). It returns true if it's mounted. An error is returned in
	// case of system errors or if it's mounted incorrectly.
	IsMounted(target string) (bool, error)

	GetDeviceName(mounter mount.Interface, mountPath string) (string, error)

	// GetStatistics returns capacity-related volume statistics for the given
	// volume path.
	GetStatistics(volumePath string) (volumeStatistics, error)
}

// newMounter returns a new mounter instance
func newMounter(log *logrus.Entry) *mounter {
	return &mounter{
		log: log,
	}
}

type mounter struct {
	log *logrus.Entry
}

func (m *mounter) Format(source, fsType string) error {
	mkfsCmd := fmt.Sprintf("mkfs.%s", fsType)

	_, err := exec.LookPath(mkfsCmd)
	if err != nil {
		if err == exec.ErrNotFound {
			return fmt.Errorf("%q executable not found in $PATH", mkfsCmd)
		}
		return err
	}

	mkfsArgs := []string{}

	if fsType == "" {
		return errors.New("fs type is not specified for formatting the volume")
	}

	if source == "" {
		return errors.New("source is not specified for formatting the volume")
	}

	mkfsArgs = append(mkfsArgs, source)
	if fsType == "ext4" || fsType == "ext3" {
		mkfsArgs = []string{"-F", source}
	}

	m.log.WithFields(logrus.Fields{
		"cmd":  mkfsCmd,
		"args": mkfsArgs,
	}).Info("executing format command")

	out, err := exec.Command(mkfsCmd, mkfsArgs...).CombinedOutput()
	if err != nil {
		return fmt.Errorf("formatting disk failed: %v cmd: '%s %s' output: %q",
			err, mkfsCmd, strings.Join(mkfsArgs, " "), string(out))
	}

	return nil
}

type findmntResponse struct {
	FileSystems []fileSystem `json:"filesystems"`
}

type fileSystem struct {
	Target      string `json:"target"`
	Propagation string `json:"propagation"`
	FsType      string `json:"fstype"`
	Options     string `json:"options"`
}

func (m *mounter) Mount(source, target, fsType string, opts ...string) error {
	mountCmd := "mount"
	mountArgs := []string{}

	if source == "" {
		return errors.New("source is not specified for mounting the volume")
	}

	if target == "" {
		return errors.New("target is not specified for mounting the volume")
	}

	// This is a raw block device mount. Create the mount point as a file
	// since bind mount device node requires it to be a file
	if fsType == "" {
		// create directory for target, os.Mkdirall is noop if directory exists
		err := os.MkdirAll(filepath.Dir(target), 0750)
		if err != nil {
			return fmt.Errorf("failed to create target directory for raw block bind mount: %v", err)
		}

		file, err := os.OpenFile(target, os.O_CREATE, 0660)
		if err != nil {
			return fmt.Errorf("failed to create target file for raw block bind mount: %v", err)
		}
		file.Close()
	} else {
		mountArgs = append(mountArgs, "-t", fsType)

		// create target, os.Mkdirall is noop if directory exists
		err := os.MkdirAll(target, 0750)
		if err != nil {
			return err
		}
	}

	if len(opts) > 0 {
		mountArgs = append(mountArgs, "-o", strings.Join(opts, ","))
	}

	mountArgs = append(mountArgs, source)
	mountArgs = append(mountArgs, target)

	m.log.WithFields(logrus.Fields{
		"cmd":  mountCmd,
		"args": mountArgs,
	}).Info("executing mount command")

	out, err := exec.Command(mountCmd, mountArgs...).CombinedOutput()
	if err != nil {
		return fmt.Errorf("mounting failed: %v cmd: '%s %s' output: %q",
			err, mountCmd, strings.Join(mountArgs, " "), string(out))
	}

	return nil
}

func (m *mounter) Unmount(target string) error {
	umountCmd := "umount"
	if target == "" {
		return errors.New("target is not specified for unmounting the volume")
	}

	umountArgs := []string{target}

	m.log.WithFields(logrus.Fields{
		"cmd":  umountCmd,
		"args": umountArgs,
	}).Info("executing umount command")

	out, err := exec.Command(umountCmd, umountArgs...).CombinedOutput()
	if err != nil {
		return fmt.Errorf("unmounting failed: %v cmd: '%s %s' output: %q",
			err, umountCmd, target, string(out))
	}

	return nil
}

func (m *mounter) IsFormatted(source string) (bool, error) {
	if source == "" {
		return false, errors.New("source is not specified")
	}

	blkidCmd := "blkid"
	_, err := exec.LookPath(blkidCmd)
	if err != nil {
		if err == exec.ErrNotFound {
			return false, fmt.Errorf("%q executable not found in $PATH", blkidCmd)
		}
		return false, err
	}

	blkidArgs := []string{source}

	m.log.WithFields(logrus.Fields{
		"cmd":  blkidCmd,
		"args": blkidArgs,
	}).Info("checking if source is formatted")

	exitCode := 0
	cmd := exec.Command(blkidCmd, blkidArgs...)
	err = cmd.Run()
	if err != nil {
		exitError, ok := err.(*exec.ExitError)
		if !ok {
			return false, fmt.Errorf("checking formatting failed: %v cmd: %q, args: %q", err, blkidCmd, blkidArgs)
		}
		ws := exitError.Sys().(syscall.WaitStatus)
		exitCode = ws.ExitStatus()
		if exitCode == blkidExitStatusNoIdentifiers {
			return false, nil
		}
		return false, fmt.Errorf("checking formatting failed: %v cmd: %q, args: %q", err, blkidCmd, blkidArgs)
	}

	return true, nil
}

func (m *mounter) IsMounted(target string) (bool, error) {
	if target == "" {
		return false, errors.New("target is not specified for checking the mount")
	}

	findmntCmd := "findmnt"
	_, err := exec.LookPath(findmntCmd)
	if err != nil {
		if err == exec.ErrNotFound {
			return false, fmt.Errorf("%q executable not found in $PATH", findmntCmd)
		}
		return false, err
	}
	// this is quite different from the host, -M means mountpoint, -J means formant output to json
	findmntArgs := []string{"-o", "TARGET,PROPAGATION,FSTYPE,OPTIONS", "-M", target, "-J"}

	m.log.WithFields(logrus.Fields{
		"cmd":  findmntCmd,
		"args": findmntArgs,
	}).Info("checking if target is mounted")

	out, err := exec.Command(findmntCmd, findmntArgs...).CombinedOutput()
	if err != nil {
		// findmnt exits with non zero exit status if it couldn't find anything
		if strings.TrimSpace(string(out)) == "" {
			return false, nil
		}

		return false, fmt.Errorf("checking mounted failed: %v cmd: %q output: %q",
			err, findmntCmd, string(out))
	}

	// no response means there is no mount
	if string(out) == "" {
		return false, nil
	}

	var resp *findmntResponse
	err = json.Unmarshal(out, &resp)
	if err != nil {
		return false, fmt.Errorf("couldn't unmarshal data: %q: %s", string(out), err)
	}

	targetFound := false
	for _, fs := range resp.FileSystems {
		// check if the mount is propagated correctly. It should be set to shared.
		if fs.Propagation != "shared" {
			return true, fmt.Errorf("mount propagation for target %q is not enabled", target)
		}

		// the mountpoint should match as well
		if fs.Target == target {
			targetFound = true
		}
	}

	return targetFound, nil
}

func (m *mounter) GetDeviceName(mounter mount.Interface, mountPath string) (string, error) {
	devicePath, _, err := mount.GetDeviceNameFromMount(mounter, mountPath)
	return devicePath, err
}

func (m *mounter) GetStatistics(volumePath string) (volumeStatistics, error) {
	// See http://man7.org/linux/man-pages/man8/blockdev.8.html for details
	output, err := exec.Command("blockdev", "getsize64", volumePath).CombinedOutput()
	if err != nil {
		return volumeStatistics{}, fmt.Errorf("error when getting size of block volume at path %s: output: %s, err: %v", volumePath, string(output), err)
	}
	strOut := strings.TrimSpace(string(output))
	gotSizeBytes, err := strconv.ParseInt(strOut, 10, 64)
	if err != nil {
		return volumeStatistics{}, fmt.Errorf("failed to parse size %s into int", strOut)
	}

	return volumeStatistics{
		totalBytes: gotSizeBytes,
	}, nil
}
