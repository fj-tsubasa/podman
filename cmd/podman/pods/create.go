package pods

import (
	"context"
	"fmt"
	"io/ioutil"
	"os"
	"runtime"
	"sort"
	"strconv"
	"strings"

	"github.com/containers/common/pkg/completion"
	"github.com/containers/common/pkg/sysinfo"
	"github.com/containers/podman/v3/cmd/podman/common"
	"github.com/containers/podman/v3/cmd/podman/parse"
	"github.com/containers/podman/v3/cmd/podman/registry"
	"github.com/containers/podman/v3/cmd/podman/validate"
	"github.com/containers/podman/v3/pkg/domain/entities"
	"github.com/containers/podman/v3/pkg/errorhandling"
	"github.com/containers/podman/v3/pkg/specgen"
	"github.com/containers/podman/v3/pkg/util"
	"github.com/docker/docker/pkg/parsers"
	"github.com/pkg/errors"
	"github.com/sirupsen/logrus"
	"github.com/spf13/cobra"
	"github.com/spf13/pflag"
)

var (
	podCreateDescription = `After creating the pod, the pod ID is printed to stdout.

  You can then start it at any time with the  podman pod start <pod_id> command. The pod will be created with the initial state 'created'.`

	createCommand = &cobra.Command{
		Use:               "create [options]",
		Args:              validate.NoArgs,
		Short:             "Create a new empty pod",
		Long:              podCreateDescription,
		RunE:              create,
		ValidArgsFunction: completion.AutocompleteNone,
	}
)

var (
	createOptions     entities.PodCreateOptions
	labels, labelFile []string
	podIDFile         string
	replace           bool
	share             string
	userns            string
)

func init() {
	registry.Commands = append(registry.Commands, registry.CliCommand{
		Command: createCommand,
		Parent:  podCmd,
	})
	flags := createCommand.Flags()
	flags.SetInterspersed(false)

	common.DefineNetFlags(createCommand)

	cpusetflagName := "cpuset-cpus"
	flags.StringVar(&createOptions.CpusetCpus, cpusetflagName, "", "CPUs in which to allow execution")
	_ = createCommand.RegisterFlagCompletionFunc(cpusetflagName, completion.AutocompleteDefault)

	cpusflagName := "cpus"
	flags.Float64Var(&createOptions.Cpus, cpusflagName, 0.000, "set amount of CPUs for the pod")
	_ = createCommand.RegisterFlagCompletionFunc(cpusflagName, completion.AutocompleteDefault)

	cgroupParentflagName := "cgroup-parent"
	flags.StringVar(&createOptions.CGroupParent, cgroupParentflagName, "", "Set parent cgroup for the pod")
	_ = createCommand.RegisterFlagCompletionFunc(cgroupParentflagName, completion.AutocompleteDefault)

	usernsFlagName := "userns"
	flags.StringVar(&userns, usernsFlagName, os.Getenv("PODMAN_USERNS"), "User namespace to use")
	_ = createCommand.RegisterFlagCompletionFunc(usernsFlagName, common.AutocompleteUserNamespace)

	flags.BoolVar(&createOptions.Infra, "infra", true, "Create an infra container associated with the pod to share namespaces with")

	infraConmonPidfileFlagName := "infra-conmon-pidfile"
	flags.StringVar(&createOptions.InfraConmonPidFile, infraConmonPidfileFlagName, "", "Path to the file that will receive the POD of the infra container's conmon")
	_ = createCommand.RegisterFlagCompletionFunc(infraConmonPidfileFlagName, completion.AutocompleteDefault)

	infraImageFlagName := "infra-image"
	flags.String(infraImageFlagName, containerConfig.Engine.InfraImage, "The image of the infra container to associate with the pod")
	_ = createCommand.RegisterFlagCompletionFunc(infraImageFlagName, common.AutocompleteImages)

	infraCommandFlagName := "infra-command"
	flags.String(infraCommandFlagName, containerConfig.Engine.InfraCommand, "The command to run on the infra container when the pod is started")
	_ = createCommand.RegisterFlagCompletionFunc(infraCommandFlagName, completion.AutocompleteNone)

	infraNameFlagName := "infra-name"
	flags.StringVarP(&createOptions.InfraName, infraNameFlagName, "", "", "The name used as infra container name")
	_ = createCommand.RegisterFlagCompletionFunc(infraNameFlagName, completion.AutocompleteNone)

	labelFileFlagName := "label-file"
	flags.StringSliceVar(&labelFile, labelFileFlagName, []string{}, "Read in a line delimited file of labels")
	_ = createCommand.RegisterFlagCompletionFunc(labelFileFlagName, completion.AutocompleteDefault)

	labelFlagName := "label"
	flags.StringSliceVarP(&labels, labelFlagName, "l", []string{}, "Set metadata on pod (default [])")
	_ = createCommand.RegisterFlagCompletionFunc(labelFlagName, completion.AutocompleteNone)

	nameFlagName := "name"
	flags.StringVarP(&createOptions.Name, nameFlagName, "n", "", "Assign a name to the pod")
	_ = createCommand.RegisterFlagCompletionFunc(nameFlagName, completion.AutocompleteNone)

	hostnameFlagName := "hostname"
	flags.StringVarP(&createOptions.Hostname, hostnameFlagName, "", "", "Set a hostname to the pod")
	_ = createCommand.RegisterFlagCompletionFunc(hostnameFlagName, completion.AutocompleteNone)

	pidFlagName := "pid"
	flags.StringVar(&createOptions.Pid, pidFlagName, "", "PID namespace to use")
	_ = createCommand.RegisterFlagCompletionFunc(pidFlagName, common.AutocompleteNamespace)

	podIDFileFlagName := "pod-id-file"
	flags.StringVar(&podIDFile, podIDFileFlagName, "", "Write the pod ID to the file")
	_ = createCommand.RegisterFlagCompletionFunc(podIDFileFlagName, completion.AutocompleteDefault)

	flags.BoolVar(&replace, "replace", false, "If a pod with the same name exists, replace it")

	shareFlagName := "share"
	flags.StringVar(&share, shareFlagName, specgen.DefaultKernelNamespaces, "A comma delimited list of kernel namespaces the pod will share")
	_ = createCommand.RegisterFlagCompletionFunc(shareFlagName, common.AutocompletePodShareNamespace)

	flags.SetNormalizeFunc(aliasNetworkFlag)
}

func aliasNetworkFlag(_ *pflag.FlagSet, name string) pflag.NormalizedName {
	if name == "net" {
		name = "network"
	}
	return pflag.NormalizedName(name)
}

func create(cmd *cobra.Command, args []string) error {
	var (
		err     error
		podIDFD *os.File
	)
	createOptions.Labels, err = parse.GetAllLabels(labelFile, labels)
	if err != nil {
		return errors.Wrapf(err, "unable to process labels")
	}

	if !createOptions.Infra {
		logrus.Debugf("Not creating an infra container")
		if cmd.Flag("infra-conmon-pidfile").Changed {
			return errors.New("cannot set infra-conmon-pid without an infra container")
		}
		if cmd.Flag("infra-command").Changed {
			return errors.New("cannot set infra-command without an infra container")
		}
		if cmd.Flag("infra-image").Changed {
			return errors.New("cannot set infra-image without an infra container")
		}
		createOptions.InfraImage = ""
		if createOptions.InfraName != "" {
			return errors.New("cannot set infra-name without an infra container")
		}

		if cmd.Flag("share").Changed && share != "none" && share != "" {
			return fmt.Errorf("cannot set share(%s) namespaces without an infra container", cmd.Flag("share").Value)
		}
		createOptions.Share = nil
	} else {
		createOptions.Share = strings.Split(share, ",")
		if cmd.Flag("infra-command").Changed {
			// Only send content to server side if user changed defaults
			createOptions.InfraCommand, err = cmd.Flags().GetString("infra-command")
			if err != nil {
				return err
			}
		}
		if cmd.Flag("infra-image").Changed {
			// Only send content to server side if user changed defaults
			createOptions.InfraImage, err = cmd.Flags().GetString("infra-image")
			if err != nil {
				return err
			}
		}
	}

	createOptions.Userns, err = specgen.ParseUserNamespace(userns)
	if err != nil {
		return err
	}

	if cmd.Flag("pod-id-file").Changed {
		podIDFD, err = util.OpenExclusiveFile(podIDFile)
		if err != nil && os.IsExist(err) {
			return errors.Errorf("pod id file exists. Ensure another pod is not using it or delete %s", podIDFile)
		}
		if err != nil {
			return errors.Errorf("error opening pod-id-file %s", podIDFile)
		}
		defer errorhandling.CloseQuiet(podIDFD)
		defer errorhandling.SyncQuiet(podIDFD)
	}

	createOptions.Pid = cmd.Flag("pid").Value.String()

	createOptions.Net, err = common.NetFlagsToNetOptions(cmd, createOptions.Infra)
	if err != nil {
		return err
	}

	if len(createOptions.Net.PublishPorts) > 0 {
		if !createOptions.Infra {
			return errors.Errorf("you must have an infra container to publish port bindings to the host")
		}
	}

	createOptions.CreateCommand = os.Args

	if replace {
		if err := replacePod(createOptions.Name); err != nil {
			return err
		}
	}

	numCPU := sysinfo.NumCPU()
	if numCPU == 0 {
		numCPU = runtime.NumCPU()
	}
	if createOptions.Cpus > float64(numCPU) {
		createOptions.Cpus = float64(numCPU)
	}
	copy := createOptions.CpusetCpus
	cpuSet := createOptions.Cpus
	if cpuSet == 0 {
		cpuSet = float64(sysinfo.NumCPU())
	}
	ret, err := parsers.ParseUintList(copy)
	copy = ""
	if err != nil {
		errors.Wrapf(err, "could not parse list")
	}
	var vals []int
	for ind, val := range ret {
		if val {
			vals = append(vals, ind)
		}
	}
	sort.Ints(vals)
	for ind, core := range vals {
		if core > int(cpuSet) {
			if copy == "" {
				copy = "0-" + strconv.Itoa(int(cpuSet))
				createOptions.CpusetCpus = copy
				break
			} else {
				createOptions.CpusetCpus = copy
				break
			}
		} else if ind != 0 {
			copy += "," + strconv.Itoa(core)
		} else {
			copy = "" + strconv.Itoa(core)
		}
	}
	response, err := registry.ContainerEngine().PodCreate(context.Background(), createOptions)
	if err != nil {
		return err
	}
	if len(podIDFile) > 0 {
		if err = ioutil.WriteFile(podIDFile, []byte(response.Id), 0644); err != nil {
			return errors.Wrapf(err, "failed to write pod ID to file")
		}
	}
	fmt.Println(response.Id)
	return nil
}

func replacePod(name string) error {
	if len(name) == 0 {
		return errors.New("cannot replace pod without --name being set")
	}
	rmOptions := entities.PodRmOptions{
		Force:  true, // stop and remove pod
		Ignore: true, // ignore if pod doesn't exist
	}
	return removePods([]string{name}, rmOptions, false)
}
