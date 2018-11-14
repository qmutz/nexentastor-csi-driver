//TODO Consider to add NodeStageVolume() method:
// - called by k8s to temporarily mount the volume to a staging path
// - staging path is a global directory on the node
// - k8s allows user to use a single volume by multiple pods (for NFS)
// - if all pods run on the same node the single mount point will be used by all of them.

package driver

import (
	"fmt"
	"os"
	"regexp"
	"strings"

	csi "github.com/container-storage-interface/spec/lib/go/csi/v0"
	csiCommon "github.com/kubernetes-csi/drivers/pkg/csi-common" //TODO get rid of it
	"github.com/sirupsen/logrus"
	"golang.org/x/net/context"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"k8s.io/kubernetes/pkg/util/mount"

	"github.com/Nexenta/nexentastor-csi-driver/src/arrays"
	"github.com/Nexenta/nexentastor-csi-driver/src/config"
	"github.com/Nexenta/nexentastor-csi-driver/src/ns"
)

// NodeServer - k8s csi driver node server
type NodeServer struct {
	*csiCommon.DefaultNodeServer

	nsResolver *ns.Resolver
	config     *config.Config
	log        *logrus.Entry
}

func (s *NodeServer) refreshConfig() error {
	changed, err := s.config.Refresh()
	if err != nil {
		return err
	}

	if changed {
		s.nsResolver, err = ns.NewResolver(ns.ResolverArgs{
			Address:  s.config.Address,
			Username: s.config.Username,
			Password: s.config.Password,
			Log:      s.log,
		})
		if err != nil {
			return fmt.Errorf("Cannot create NexentaStor resolver: %v", err)
		}
	}

	return nil
}

func (s *NodeServer) resolveNS(datasetPath string) (ns.ProviderInterface, error) {
	nsProvider, err := s.nsResolver.Resolve(datasetPath)
	if err != nil {
		return nil, status.Errorf(
			codes.FailedPrecondition,
			"Cannot resolve '%v' on any NexentaStor(s): %v",
			datasetPath,
			err,
		)
	}
	return nsProvider, nil
}

// NodeGetCapabilities - get node capabilities
func (s *NodeServer) NodeGetCapabilities(ctx context.Context, req *csi.NodeGetCapabilitiesRequest) (
	*csi.NodeGetCapabilitiesResponse,
	error,
) {
	s.log.WithField("func", "NodeGetCapabilities()").Infof("request: %+v", req)
	return &csi.NodeGetCapabilitiesResponse{
		Capabilities: []*csi.NodeServiceCapability{
			{
				Type: &csi.NodeServiceCapability_Rpc{
					Rpc: &csi.NodeServiceCapability_RPC{
						Type: csi.NodeServiceCapability_RPC_UNKNOWN,
					},
				},
			},
		},
	}, nil
}

// NodePublishVolume - mounts NS fs to the node
func (s *NodeServer) NodePublishVolume(ctx context.Context, req *csi.NodePublishVolumeRequest) (
	*csi.NodePublishVolumeResponse,
	error,
) {
	l := s.log.WithField("func", "NodePublishVolume()")
	l.Infof("request: %+v", req)

	volumeID := req.GetVolumeId()
	if len(volumeID) == 0 {
		return nil, status.Error(codes.InvalidArgument, "Volume ID must be provided")
	}

	targetPath := req.GetTargetPath()
	if len(targetPath) == 0 {
		return nil, status.Error(codes.InvalidArgument, "Target path must be provided")
	}

	// read and validate config
	err := s.refreshConfig()
	if err != nil {
		return nil, status.Errorf(codes.FailedPrecondition, "Cannot use config file: %v", err)
	}

	nsProvider, err := s.resolveNS(volumeID)
	if err != nil {
		return nil, err
	}

	l.Infof("resolved NS: %v, %v", nsProvider, volumeID)

	// get filesystem information
	filesystem, err := nsProvider.GetFilesystem(volumeID)
	if err != nil {
		return nil, status.Errorf(
			codes.Internal,
			"Cannot get filesystem '%v' volume: %v",
			volumeID,
			err,
		)
	} else if filesystem == nil {
		return nil, status.Errorf(codes.FailedPrecondition, "Cannot find filesystem '%v'", volumeID)
	}

	// create NFS share if not exists
	if !filesystem.SharedOverNfs {
		err = nsProvider.CreateNfsShare(filesystem.Path)
		if err != nil {
			return nil, status.Errorf(codes.Internal, "Cannot share filesystem '%v': %v", filesystem.Path, err)
		}

		// select read-only or read-write mount options set
		aclRuleSet := ns.ACLReadWrite
		if req.GetReadonly() {
			aclRuleSet = ns.ACLReadOnly
		}
		// apply NS filesystem ACL (gets applied only for new volumes, not for already shared pre-provisioned volumes)
		err = nsProvider.SetFilesystemACL(filesystem.Path, aclRuleSet)
		if err != nil {
			return nil, status.Errorf(codes.Internal, "Cannot set filesystem ACL for '%v': %v", filesystem.Path, err)
		}
	}

	mountOptions := req.GetVolumeCapability().GetMount().GetMountFlags() // k8s.PersistentVolume.spec.mountOptions
	if mountOptions == nil {
		mountOptions = []string{}
	}

	// volume params passes from ControllerServer.CreateVolume()
	volumeAttributes := req.GetVolumeAttributes()
	if volumeAttributes == nil {
		volumeAttributes = make(map[string]string)
	}

	// get nfsMountOptions from runtime volume creation params, use config's value if not specified
	nfsMountOptions := s.config.DefaultNfsMountOptions
	if v, ok := volumeAttributes["nfsMountOptions"]; ok {
		nfsMountOptions = v
	}
	nfsMountOptionsList := strings.Split(nfsMountOptions, ",")
	for _, option := range nfsMountOptionsList {
		mountOptions = append(mountOptions, option)
	}

	// add "ro" mount option if k8s requests it
	if req.GetReadonly() {
		mountOptions = arrays.AppendIfRegexpNotExistString(mountOptions, regexp.MustCompile("^ro$"), "ro")
	}

	// NFS v3 is used by default if no version specified by user
	mountOptions = arrays.AppendIfRegexpNotExistString(mountOptions, regexp.MustCompile("^vers=.*$"), "vers=3")

	// get dataIP from runtime params, set default if not specified
	dataIP := ""
	if v, ok := volumeAttributes["dataIP"]; ok {
		dataIP = v
	} else {
		dataIP = s.config.DefaultDataIP
	}

	mountSource := fmt.Sprintf("%v:%v", dataIP, filesystem.MountPoint)

	err = s.doMount(mountSource, targetPath, mountOptions)
	if err != nil {
		return nil, err
	}

	l.Infof("volume '%v' has been published to '%v'", volumeID, targetPath)
	return &csi.NodePublishVolumeResponse{}, nil
}

// only "nfs" is supported for now
func (s *NodeServer) doMount(mountSource, targetPath string, mountOptions []string) error {
	l := s.log.WithField("func", "doMount()")

	mounter := mount.New("")

	// check if mountpoint exists, create if there is no such directory
	notMountPoint, err := mounter.IsLikelyNotMountPoint(targetPath)
	if err != nil {
		if os.IsNotExist(err) {
			if err := os.MkdirAll(targetPath, 0750); err != nil {
				return status.Errorf(
					codes.Internal,
					"Failed to mkdir to share target path '%v': %v",
					targetPath,
					err,
				)
			}
			notMountPoint = true
		} else {
			return status.Errorf(
				codes.Internal,
				"Cannot ensure that target path '%v' can be used as a mount point: %v",
				targetPath,
				err,
			)
		}
	}

	if !notMountPoint { // already mounted
		return status.Errorf(codes.Internal, "Target path '%v' is already a mount point", targetPath)
	}

	l.Infof(
		"mount params: mountSource: '%v', targetPath: '%v', mountOptions: '%v'",
		targetPath,
		mountSource,
		mountOptions,
	)

	err = mounter.Mount(mountSource, targetPath, "nfs", mountOptions)
	if err != nil {
		if os.IsPermission(err) {
			return status.Errorf(
				codes.PermissionDenied,
				"Permission denied to mount '%v' to '%v': %v",
				mountSource,
				targetPath,
				err,
			)
		} else if strings.Contains(err.Error(), "invalid argument") {
			return status.Errorf(
				codes.InvalidArgument,
				"Cannot mount '%v' to '%v', invalid argument: %v",
				mountSource,
				targetPath,
				err,
			)
		}
		return status.Errorf(
			codes.Internal,
			"Failed to mount '%v' to '%v': %v",
			mountSource,
			targetPath,
			err,
		)
	}

	return nil
}

// NodeUnpublishVolume - umount NS fs from the node and delete directory if successful
func (s *NodeServer) NodeUnpublishVolume(ctx context.Context, req *csi.NodeUnpublishVolumeRequest) (
	*csi.NodeUnpublishVolumeResponse,
	error,
) {
	l := s.log.WithField("func", "NodeUnpublishVolume()")
	l.Infof("request: %+v", req)

	volumeID := req.GetVolumeId()
	if len(volumeID) == 0 {
		return nil, status.Error(codes.InvalidArgument, "Volume ID must be provided")
	}

	targetPath := req.GetTargetPath()
	if len(targetPath) == 0 {
		return nil, status.Error(codes.InvalidArgument, "Target path must be provided")
	}

	mounter := mount.New("")

	if err := mounter.Unmount(targetPath); err != nil {
		return nil, status.Errorf(codes.Internal, "Failed to unmount target path '%v': %v", targetPath, err)
	}

	notMountPoint, err := mounter.IsLikelyNotMountPoint(targetPath)
	if err != nil {
		if os.IsNotExist(err) {
			l.Warnf("mount point '%v' already doesn't exist: '%v', return OK", targetPath, err)
			return &csi.NodeUnpublishVolumeResponse{}, nil
		}
		return nil, status.Errorf(
			codes.Internal,
			"Cannot ensure that target path '%v' is a mount point: '%v'",
			targetPath,
			err,
		)
	} else if !notMountPoint { // still mounted
		return nil, status.Errorf(codes.Internal, "Target path '%v' is still mounted", targetPath)
	}

	if err := os.Remove(targetPath); err != nil && !os.IsNotExist(err) {
		return nil, status.Errorf(codes.Internal, "Cannot remove unmounted target path '%v': %v", targetPath, err)
	}

	l.Infof("volume '%v' has been unpublished from '%v'", volumeID, targetPath)
	return &csi.NodeUnpublishVolumeResponse{}, nil
}

// NodeStageVolume - stage volume
//TODO use this to mount NFS, then do bind mount?
func (s *NodeServer) NodeStageVolume(ctx context.Context, req *csi.NodeStageVolumeRequest) (
	*csi.NodeStageVolumeResponse,
	error,
) {
	s.log.WithField("func", "NodeStageVolume()").Infof("request: %+v", req)
	return nil, status.Error(codes.Unimplemented, "")
}

// NodeUnstageVolume - unstage volume
//TODO use this to umount NFS?
func (s *NodeServer) NodeUnstageVolume(ctx context.Context, req *csi.NodeUnstageVolumeRequest) (
	*csi.NodeUnstageVolumeResponse,
	error,
) {
	s.log.WithField("func", "NodeUnstageVolume()").Infof("request: %+v", req)
	return nil, status.Error(codes.Unimplemented, "")
}

// NewNodeServer - create an instance of node service
func NewNodeServer(driver *Driver) (*NodeServer, error) {
	l := driver.log.WithField("cmp", "NodeServer")
	l.Info("create new NodeServer...")

	nsResolver, err := ns.NewResolver(ns.ResolverArgs{
		Address:  driver.config.Address,
		Username: driver.config.Username,
		Password: driver.config.Password,
		Log:      l,
	})
	if err != nil {
		return nil, fmt.Errorf("Cannot create NexentaStor resolver: %v", err)
	}

	return &NodeServer{
		DefaultNodeServer: csiCommon.NewDefaultNodeServer(driver.csiDriver),
		nsResolver:        nsResolver,
		config:            driver.config,
		log:               l,
	}, nil
}
