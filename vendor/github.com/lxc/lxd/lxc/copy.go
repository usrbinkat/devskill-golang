package main

import (
	"fmt"
	"strings"

	"github.com/spf13/cobra"

	"github.com/lxc/lxd/client"
	"github.com/lxc/lxd/lxc/config"
	"github.com/lxc/lxd/lxc/utils"
	"github.com/lxc/lxd/shared"
	cli "github.com/lxc/lxd/shared/cmd"
	"github.com/lxc/lxd/shared/i18n"
)

type cmdCopy struct {
	global *cmdGlobal

	flagNoProfiles    bool
	flagProfile       []string
	flagConfig        []string
	flagEphemeral     bool
	flagContainerOnly bool
	flagMode          string
	flagStateless     bool
	flagStorage       string
	flagTarget        string
}

func (c *cmdCopy) Command() *cobra.Command {
	cmd := &cobra.Command{}
	cmd.Use = i18n.G("copy [<remote>:]<source>[/<snapshot>] [[<remote>:]<destination>]")
	cmd.Aliases = []string{"cp"}
	cmd.Short = i18n.G("Copy containers within or in between LXD instances")
	cmd.Long = cli.FormatSection(i18n.G("Description"), i18n.G(
		`Copy containers within or in between LXD instances`))

	cmd.RunE = c.Run
	cmd.Flags().StringArrayVarP(&c.flagConfig, "config", "c", nil, i18n.G("Config key/value to apply to the new container")+"``")
	cmd.Flags().StringArrayVarP(&c.flagProfile, "profile", "p", nil, i18n.G("Profile to apply to the new container")+"``")
	cmd.Flags().BoolVarP(&c.flagEphemeral, "ephemeral", "e", false, i18n.G("Ephemeral container"))
	cmd.Flags().StringVar(&c.flagMode, "mode", "pull", i18n.G("Transfer mode. One of pull (default), push or relay")+"``")
	cmd.Flags().BoolVar(&c.flagContainerOnly, "container-only", false, i18n.G("Copy the container without its snapshots"))
	cmd.Flags().BoolVar(&c.flagStateless, "stateless", false, i18n.G("Copy a stateful container stateless"))
	cmd.Flags().StringVarP(&c.flagStorage, "storage", "s", "", i18n.G("Storage pool name")+"``")
	cmd.Flags().StringVar(&c.flagTarget, "target", "", i18n.G("Cluster member name")+"``")
	cmd.Flags().BoolVar(&c.flagNoProfiles, "no-profiles", false, i18n.G("Create the container with no profiles applied"))

	return cmd
}

func (c *cmdCopy) copyContainer(conf *config.Config, sourceResource string,
	destResource string, keepVolatile bool, ephemeral int, stateful bool,
	containerOnly bool, mode string, pool string) error {
	// Parse the source
	sourceRemote, sourceName, err := conf.ParseRemote(sourceResource)
	if err != nil {
		return err
	}

	// Parse the destination
	destRemote, destName, err := conf.ParseRemote(destResource)
	if err != nil {
		return err
	}

	// Make sure we have a container or snapshot name
	if sourceName == "" {
		return fmt.Errorf(i18n.G("You must specify a source container name"))
	}

	// Check that a destination container was specified, if --target is passed.
	if destName == "" && c.flagTarget != "" {
		return fmt.Errorf(i18n.G("You must specify a destination container name when using --target"))
	}

	// If no destination name was provided, use the same as the source
	if destName == "" && destResource != "" {
		destName = sourceName
	}

	// Connect to the source host
	source, err := conf.GetContainerServer(sourceRemote)
	if err != nil {
		return err
	}
	source = source.UseTarget(c.flagTarget)

	// Connect to the destination host
	var dest lxd.ContainerServer
	if sourceRemote == destRemote {
		// Source and destination are the same
		dest = source
	} else {
		// Destination is different, connect to it
		dest, err = conf.GetContainerServer(destRemote)
		if err != nil {
			return err
		}
	}

	// Confirm that --target is only used with a cluster
	if c.flagTarget != "" && !dest.IsClustered() {
		return fmt.Errorf(i18n.G("To use --target, the destination remote must be a cluster"))
	}

	// Parse the config overrides
	configMap := map[string]string{}
	for _, entry := range c.flagConfig {
		if !strings.Contains(entry, "=") {
			return fmt.Errorf(i18n.G("Bad key=value pair: %s"), entry)
		}

		fields := strings.SplitN(entry, "=", 2)
		configMap[fields[0]] = fields[1]
	}

	var op lxd.RemoteOperation
	if shared.IsSnapshot(sourceName) {
		// Prepare the container creation request
		args := lxd.ContainerSnapshotCopyArgs{
			Name: destName,
			Mode: mode,
			Live: stateful,
		}

		// Copy of a snapshot into a new container
		srcFields := strings.SplitN(sourceName, shared.SnapshotDelimiter, 2)
		entry, _, err := source.GetContainerSnapshot(srcFields[0], srcFields[1])
		if err != nil {
			return err
		}

		// Allow adding additional profiles
		if c.flagProfile != nil {
			entry.Profiles = append(entry.Profiles, c.flagProfile...)
		} else if c.flagNoProfiles {
			entry.Profiles = []string{}
		}

		// Allow setting additional config keys
		if configMap != nil {
			for key, value := range configMap {
				entry.Config[key] = value
			}
		}

		// Allow overriding the ephemeral status
		if ephemeral == 1 {
			entry.Ephemeral = true
		} else if ephemeral == 0 {
			entry.Ephemeral = false
		}

		rootDiskDeviceKey, _, _ := shared.GetRootDiskDevice(entry.Devices)
		if err != nil {
			return err
		}

		if rootDiskDeviceKey != "" && pool != "" {
			entry.Devices[rootDiskDeviceKey]["pool"] = pool
		} else if pool != "" {
			entry.Devices["root"] = map[string]string{
				"type": "disk",
				"path": "/",
				"pool": pool,
			}
		}

		// Strip the volatile keys if requested
		if !keepVolatile {
			for k := range entry.Config {
				if k == "volatile.base_image" {
					continue
				}

				if strings.HasPrefix(k, "volatile") {
					delete(entry.Config, k)
				}
			}
		}

		// Do the actual copy
		op, err = dest.CopyContainerSnapshot(source, *entry, &args)
		if err != nil {
			return err
		}
	} else {
		// Prepare the container creation request
		args := lxd.ContainerCopyArgs{
			Name:          destName,
			Live:          stateful,
			ContainerOnly: containerOnly,
			Mode:          mode,
		}

		// Copy of a container into a new container
		entry, _, err := source.GetContainer(sourceName)
		if err != nil {
			return err
		}

		// Allow adding additional profiles
		if c.flagProfile != nil {
			entry.Profiles = append(entry.Profiles, c.flagProfile...)
		} else if c.flagNoProfiles {
			entry.Profiles = []string{}
		}

		// Allow setting additional config keys
		if configMap != nil {
			for key, value := range configMap {
				entry.Config[key] = value
			}
		}

		// Allow overriding the ephemeral status
		if ephemeral == 1 {
			entry.Ephemeral = true
		} else if ephemeral == 0 {
			entry.Ephemeral = false
		}

		rootDiskDeviceKey, _, _ := shared.GetRootDiskDevice(entry.Devices)
		if err != nil {
			return err
		}

		if rootDiskDeviceKey != "" && pool != "" {
			entry.Devices[rootDiskDeviceKey]["pool"] = pool
		} else if pool != "" {
			entry.Devices["root"] = map[string]string{
				"type": "disk",
				"path": "/",
				"pool": pool,
			}
		}

		// Strip the volatile keys if requested
		if !keepVolatile {
			for k := range entry.Config {
				if k == "volatile.base_image" {
					continue
				}

				if strings.HasPrefix(k, "volatile") {
					delete(entry.Config, k)
				}
			}
		}

		// Do the actual copy
		op, err = dest.CopyContainer(source, *entry, &args)
		if err != nil {
			return err
		}
	}

	// Watch the background operation
	progress := utils.ProgressRenderer{Format: i18n.G("Transferring container: %s")}
	_, err = op.AddHandler(progress.UpdateOp)
	if err != nil {
		progress.Done("")
		return err
	}

	// Wait for the copy to complete
	err = op.Wait()
	if err != nil {
		progress.Done("")
		return err
	}
	progress.Done("")

	// If choosing a random name, show it to the user
	if destResource == "" {
		// Get the successful operation data
		opInfo, err := op.GetTarget()
		if err != nil {
			return err
		}

		// Extract the list of affected containers
		containers, ok := opInfo.Resources["containers"]
		if !ok || len(containers) != 1 {
			return fmt.Errorf(i18n.G("Failed to get the new container name"))
		}

		// Extract the name of the container
		fields := strings.Split(containers[0], "/")
		fmt.Printf(i18n.G("Container name is: %s")+"\n", fields[len(fields)-1])
	}

	return nil
}

func (c *cmdCopy) Run(cmd *cobra.Command, args []string) error {
	conf := c.global.conf

	// Sanity checks
	exit, err := c.global.CheckArgs(cmd, args, 1, 2)
	if exit {
		return err
	}

	// For copies, default to non-ephemeral and allow override (move uses -1)
	ephem := 0
	if c.flagEphemeral {
		ephem = 1
	}

	// Parse the mode
	mode := "pull"
	if c.flagMode != "" {
		mode = c.flagMode
	}

	stateful := !c.flagStateless

	// If not target name is specified, one will be chosed by the server
	if len(args) < 2 {
		return c.copyContainer(conf, args[0], "", false, ephem,
			stateful, c.flagContainerOnly, mode, c.flagStorage)
	}

	// Normal copy with a pre-determined name
	return c.copyContainer(conf, args[0], args[1], false, ephem,
		stateful, c.flagContainerOnly, mode, c.flagStorage)
}
