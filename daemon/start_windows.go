package daemon // import "github.com/docker/docker/daemon"

import (
	"fmt"
	"path/filepath"
	"syscall"
	"unsafe"

	"github.com/Microsoft/go-winio/vhd"
	"github.com/Microsoft/hcsshim/cmd/containerd-shim-runhcs-v1/options"
	"github.com/Microsoft/opengcs/client"
	"github.com/docker/docker/container"
	"github.com/docker/docker/pkg/system"
	specs "github.com/opencontainers/runtime-spec/specs-go"
	"github.com/pkg/errors"
)

func (daemon *Daemon) getLibcontainerdCreateOptions(container *container.Container) (interface{}, error) {

	// Set the runtime options to debug regardless of current logging level.
	if system.ContainerdRuntimeSupported() {
		opts := &options.Options{Debug: true}
		return opts, nil
	}

	// TODO @jhowardmsft (containerd) - Probably need to revisit LCOW options here
	// rather than blindly ignoring them.

	// LCOW options.
	if container.OS == "linux" {
		config := &client.Config{}
		if err := config.GenerateDefault(daemon.configStore.GraphOptions); err != nil {
			return nil, err
		}
		// Override from user-supplied options.
		for k, v := range container.HostConfig.StorageOpt {
			switch k {
			case "lcow.kirdpath":
				config.KirdPath = v
			case "lcow.kernel":
				config.KernelFile = v
			case "lcow.initrd":
				config.InitrdFile = v
			case "lcow.bootparameters":
				config.BootParameters = v
			}
		}
		if err := config.Validate(); err != nil {
			return nil, err
		}

		return config, nil
	}

	return nil, nil
}

// postCreate does platform-specific process after a container has been created,
// but before it has been started.
func postCreate(spec *specs.Spec) (syscall.Handle, error) {

	// Check if any action is needed first.
	if !postCreateStartActionNeeded(spec) {
		return 0, nil
	}

	// Operating on the scratch disk
	path := filepath.Join(spec.Windows.LayerFolders[len(spec.Windows.LayerFolders)-1], "sandbox.vhdx")

	if spec.Windows.HyperV == nil {
		// Argon (WCOW)
		handle, err := vhd.OpenVirtualDisk(path, vhd.VirtualDiskAccessNone, vhd.OpenVirtualDiskFlagParentCachedIO|vhd.OpenVirtualDiskFlagIgnoreRelativeParentLocator)
		if err != nil {
			syscall.CloseHandle(handle)
			return 0, errors.Wrap(err, fmt.Sprintf("failed to open %s", path))
		}
		if err := setVhdWriteCacheMode(handle, WriteCacheModeDisableFlushing); err != nil {
			syscall.CloseHandle(handle)
			return 0, errors.Wrap(err, fmt.Sprintf("failed to disable flushing on %s", path))
		}
		return handle, nil
	}

	// TODO Xenon (WCOW)
	return 0, nil
}

// postStart does platform-specific process after a container has been started.
func postStart(spec *specs.Spec, handle syscall.Handle) {
	if handle == 0 {
		return
	}

	if !postCreateStartActionNeeded(spec) {
		return
	}

	setVhdWriteCacheMode(handle, WriteCacheModeCacheMetadata)
	syscall.CloseHandle(handle)
}

// postCreateStartActionNeeded determines if there is something that needs
// to be done in the postCreate or postStart functions.
func postCreateStartActionNeeded(spec *specs.Spec) bool {
	// No-op if not using containerd runtime
	if !system.ContainerdRuntimeSupported() {
		return false
	}

	// No-op pre-RS5 or post-18855. Pre-RS5 doesn't use v2. Post 18855 has
	// these optimisations in the platform for v2 callers.
	osv := system.GetOSVersion()
	fmt.Println(osv)
	if osv.Build < 17763 || osv.Build >= 18855 {
		return false
	}

	// No-op if we're not optimising, or LCOW.
	if spec == nil || spec.Windows == nil || !spec.Windows.IgnoreFlushesDuringBoot || spec.Linux != nil {
		return false
	}
	return true
}

type WriteCacheMode uint16

const (
	// Write Cache Mode for a VHD.
	WriteCacheModeCacheMetadata         WriteCacheMode = 0
	WriteCacheModeWriteInternalMetadata WriteCacheMode = 1
	WriteCacheModeWriteMetadata         WriteCacheMode = 2
	WriteCacheModeCommitAll             WriteCacheMode = 3
	WriteCacheModeDisableFlushing       WriteCacheMode = 4
)

// setVhdWriteCacheMode sets the WriteCacheMode for a VHD. The handle
// to the VHD should be opened with Access: None, Flags: ParentCachedIO |
// IgnoreRelativeParentLocator. Use DisableFlushing for optimisation during
// first boot, and CacheMetadata following container start
func setVhdWriteCacheMode(handle syscall.Handle, wcm WriteCacheMode) error {
	type storageSetSurfaceCachePolicyRequest struct {
		RequestLevel uint32
		CacheMode    uint16
		pad          uint16 // For 4-byte alignment
	}
	const ioctlSetSurfaceCachePolicy uint32 = 0x2d1a10
	request := storageSetSurfaceCachePolicyRequest{
		RequestLevel: 1,
		CacheMode:    uint16(wcm),
		pad:          0,
	}
	var bytesReturned uint32
	return syscall.DeviceIoControl(
		handle,
		ioctlSetSurfaceCachePolicy,
		(*byte)(unsafe.Pointer(&request)),
		uint32(unsafe.Sizeof(request)),
		nil,
		0,
		&bytesReturned,
		nil)
}
