//go:build amd64 || arm64

package machine

import (
	"fmt"
	"os"

	"github.com/containers/common/pkg/completion"
	"github.com/containers/common/pkg/config"
	"github.com/containers/podman/v4/cmd/podman/registry"
	"github.com/containers/podman/v4/libpod/events"
	"github.com/containers/podman/v4/pkg/machine"
	"github.com/containers/podman/v4/pkg/machine/define"
	"github.com/spf13/cobra"
)

var (
	initCmd = &cobra.Command{
		Use:               "init [options] [NAME]",
		Short:             "Initialize a virtual machine",
		Long:              "Initialize a virtual machine",
		PersistentPreRunE: machinePreRunE,
		RunE:              initMachine,
		Args:              cobra.MaximumNArgs(1),
		Example:           `podman machine init podman-machine-default`,
		ValidArgsFunction: completion.AutocompleteNone,
	}

	initOpts           = machine.InitOptions{}
	initOptionalFlags  = InitOptionalFlags{}
	defaultMachineName = machine.DefaultMachineName
	now                bool
)

// Flags which have a meaning when unspecified that differs from the flag default
type InitOptionalFlags struct {
	UserModeNetworking bool
}

// maxMachineNameSize is set to thirty to limit huge machine names primarily
// because macOS has a much smaller file size limit.
const maxMachineNameSize = 30

func init() {
	registry.Commands = append(registry.Commands, registry.CliCommand{
		Command: initCmd,
		Parent:  machineCmd,
	})
	flags := initCmd.Flags()
	cfg := registry.PodmanConfig()

	cpusFlagName := "cpus"
	flags.Uint64Var(
		&initOpts.CPUS,
		cpusFlagName, cfg.ContainersConfDefaultsRO.Machine.CPUs,
		"Number of CPUs",
	)
	_ = initCmd.RegisterFlagCompletionFunc(cpusFlagName, completion.AutocompleteNone)

	diskSizeFlagName := "disk-size"
	flags.Uint64Var(
		&initOpts.DiskSize,
		diskSizeFlagName, cfg.ContainersConfDefaultsRO.Machine.DiskSize,
		"Disk size in GiB",
	)

	_ = initCmd.RegisterFlagCompletionFunc(diskSizeFlagName, completion.AutocompleteNone)

	memoryFlagName := "memory"
	flags.Uint64VarP(
		&initOpts.Memory,
		memoryFlagName, "m", cfg.ContainersConfDefaultsRO.Machine.Memory,
		"Memory in MiB",
	)
	_ = initCmd.RegisterFlagCompletionFunc(memoryFlagName, completion.AutocompleteNone)

	flags.BoolVar(
		&now,
		"now", false,
		"Start machine now",
	)
	timezoneFlagName := "timezone"
	defaultTz := cfg.ContainersConfDefaultsRO.TZ()
	if len(defaultTz) < 1 {
		defaultTz = "local"
	}
	flags.StringVar(&initOpts.TimeZone, timezoneFlagName, defaultTz, "Set timezone")
	_ = initCmd.RegisterFlagCompletionFunc(timezoneFlagName, completion.AutocompleteDefault)

	flags.BoolVar(
		&initOpts.ReExec,
		"reexec", false,
		"process was rexeced",
	)
	_ = flags.MarkHidden("reexec")

	UsernameFlagName := "username"
	flags.StringVar(&initOpts.Username, UsernameFlagName, cfg.ContainersConfDefaultsRO.Machine.User, "Username used in image")
	_ = initCmd.RegisterFlagCompletionFunc(UsernameFlagName, completion.AutocompleteDefault)

	ImagePathFlagName := "image-path"
	flags.StringVar(&initOpts.ImagePath, ImagePathFlagName, cfg.ContainersConfDefaultsRO.Machine.Image, "Path to bootable image")
	_ = initCmd.RegisterFlagCompletionFunc(ImagePathFlagName, completion.AutocompleteDefault)

	VolumeFlagName := "volume"
	flags.StringArrayVarP(&initOpts.Volumes, VolumeFlagName, "v", cfg.ContainersConfDefaultsRO.Machine.Volumes.Get(), "Volumes to mount, source:target")
	_ = initCmd.RegisterFlagCompletionFunc(VolumeFlagName, completion.AutocompleteDefault)

	USBFlagName := "usb"
	flags.StringArrayVarP(&initOpts.USBs, USBFlagName, "", []string{},
		"USB Host passthrough: bus=$1,devnum=$2 or vendor=$1,product=$2")
	_ = initCmd.RegisterFlagCompletionFunc(USBFlagName, completion.AutocompleteDefault)

	VolumeDriverFlagName := "volume-driver"
	flags.StringVar(&initOpts.VolumeDriver, VolumeDriverFlagName, "", "Optional volume driver")
	_ = initCmd.RegisterFlagCompletionFunc(VolumeDriverFlagName, completion.AutocompleteDefault)

	IgnitionPathFlagName := "ignition-path"
	flags.StringVar(&initOpts.IgnitionPath, IgnitionPathFlagName, "", "Path to ignition file")
	_ = initCmd.RegisterFlagCompletionFunc(IgnitionPathFlagName, completion.AutocompleteDefault)

	rootfulFlagName := "rootful"
	flags.BoolVar(&initOpts.Rootful, rootfulFlagName, false, "Whether this machine should prefer rootful container execution")

	userModeNetFlagName := "user-mode-networking"
	flags.BoolVar(&initOptionalFlags.UserModeNetworking, userModeNetFlagName, false,
		"Whether this machine should use user-mode networking, routing traffic through a host user-space process")
}

func initMachine(cmd *cobra.Command, args []string) error {
	var (
		err error
		vm  machine.VM
	)
	initOpts.Name = defaultMachineName
	if len(args) > 0 {
		if len(args[0]) > maxMachineNameSize {
			return fmt.Errorf("machine name %q must be %d characters or less", args[0], maxMachineNameSize)
		}
		initOpts.Name = args[0]
	}

	// The vmtype names need to be reserved and cannot be used for podman machine names
	if _, err := define.ParseVMType(initOpts.Name, define.UnknownVirt); err == nil {
		return fmt.Errorf("cannot use %q for a machine name", initOpts.Name)
	}

	if _, err := provider.LoadVMByName(initOpts.Name); err == nil {
		return fmt.Errorf("%s: %w", initOpts.Name, machine.ErrVMAlreadyExists)
	}

	cfg, err := config.ReadCustomConfig()
	if err != nil {
		return err
	}

	// check if a system connection already exists
	for _, connection := range []string{initOpts.Name, fmt.Sprintf("%s-root", initOpts.Name)} {
		if _, valueFound := cfg.Engine.ServiceDestinations[connection]; valueFound {
			return fmt.Errorf("system connection %q already exists. consider a different machine name or remove the connection with `podman system connection rm`", connection)
		}
	}

	for idx, vol := range initOpts.Volumes {
		initOpts.Volumes[idx] = os.ExpandEnv(vol)
	}

	// Process optional flags (flags where unspecified / nil has meaning )
	if cmd.Flags().Changed("user-mode-networking") {
		initOpts.UserModeNetworking = &initOptionalFlags.UserModeNetworking
	}

	vm, err = provider.NewMachine(initOpts)
	if err != nil {
		return err
	}
	if finished, err := vm.Init(initOpts); err != nil || !finished {
		// Finished = true,  err  = nil  -  Success! Log a message with further instructions
		// Finished = false, err  = nil  -  The installation is partially complete and podman should
		//                                  exit gracefully with no error and no success message.
		//                                  Examples:
		//                                  - a user has chosen to perform their own reboot
		//                                  - reexec for limited admin operations, returning to parent
		// Finished = *,     err != nil  -  Exit with an error message
		return err
	}
	newMachineEvent(events.Init, events.Event{Name: initOpts.Name})
	fmt.Println("Machine init complete")

	if now {
		return start(cmd, args)
	}
	extra := ""
	if initOpts.Name != defaultMachineName {
		extra = " " + initOpts.Name
	}
	fmt.Printf("To start your machine run:\n\n\tpodman machine start%s\n\n", extra)
	return err
}
