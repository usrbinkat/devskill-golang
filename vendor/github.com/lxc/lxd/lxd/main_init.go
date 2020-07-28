package main

import (
	"fmt"

	"github.com/pkg/errors"
	"github.com/spf13/cobra"

	"github.com/lxc/lxd/client"
	"github.com/lxc/lxd/lxd/cluster"
	"github.com/lxc/lxd/lxd/util"
	"github.com/lxc/lxd/shared"
	"github.com/lxc/lxd/shared/api"
)

type initData struct {
	api.ServerPut `yaml:",inline"`
	Cluster       *initDataCluster       `json:"cluster" yaml:"cluster"`
	Networks      []api.NetworksPost     `json:"networks" yaml:"networks"`
	StoragePools  []api.StoragePoolsPost `json:"storage_pools" yaml:"storage_pools"`
	Profiles      []api.ProfilesPost     `json:"profiles" yaml:"profiles"`
}

type initDataCluster struct {
	api.ClusterPut  `yaml:",inline"`
	ClusterPassword string `json:"cluster_password" yaml:"cluster_password"`
}

type cmdInit struct {
	global *cmdGlobal

	flagAuto    bool
	flagPreseed bool

	flagNetworkAddress  string
	flagNetworkPort     int
	flagStorageBackend  string
	flagStorageDevice   string
	flagStorageLoopSize int
	flagStoragePool     string
	flagTrustPassword   string
}

func (c *cmdInit) Command() *cobra.Command {
	cmd := &cobra.Command{}
	cmd.Use = "init"
	cmd.Short = "Configure the LXD daemon"
	cmd.Long = `Description:
  Configure the LXD daemon
`
	cmd.Example = `  init --preseed
  init --auto [--network-address=IP] [--network-port=8443] [--storage-backend=dir]
              [--storage-create-device=DEVICE] [--storage-create-loop=SIZE]
              [--storage-pool=POOL] [--trust-password=PASSWORD]
`
	cmd.RunE = c.Run
	cmd.Flags().BoolVar(&c.flagAuto, "auto", false, "Automatic (non-interactive) mode")
	cmd.Flags().BoolVar(&c.flagPreseed, "preseed", false, "Pre-seed mode, expects YAML config from stdin")

	cmd.Flags().StringVar(&c.flagNetworkAddress, "network-address", "", "Address to bind LXD to (default: none)"+"``")
	cmd.Flags().IntVar(&c.flagNetworkPort, "network-port", -1, "Port to bind LXD to (default: 8443)"+"``")
	cmd.Flags().StringVar(&c.flagStorageBackend, "storage-backend", "", "Storage backend to use (btrfs, dir, lvm or zfs, default: dir)"+"``")
	cmd.Flags().StringVar(&c.flagStorageDevice, "storage-create-device", "", "Setup device based storage using DEVICE"+"``")
	cmd.Flags().IntVar(&c.flagStorageLoopSize, "storage-create-loop", -1, "Setup loop based storage with SIZE in GB"+"``")
	cmd.Flags().StringVar(&c.flagStoragePool, "storage-pool", "", "Storage pool to use or create"+"``")
	cmd.Flags().StringVar(&c.flagTrustPassword, "trust-password", "", "Password required to add new clients"+"``")

	return cmd
}

func (c *cmdInit) Run(cmd *cobra.Command, args []string) error {
	// Sanity checks
	if c.flagAuto && c.flagPreseed {
		return fmt.Errorf("Can't use --auto and --preseed together")
	}

	if !c.flagAuto && (c.flagNetworkAddress != "" || c.flagNetworkPort != -1 ||
		c.flagStorageBackend != "" || c.flagStorageDevice != "" ||
		c.flagStorageLoopSize != -1 || c.flagStoragePool != "" ||
		c.flagTrustPassword != "") {
		return fmt.Errorf("Configuration flags require --auto")
	}

	// Connect to LXD
	d, err := lxd.ConnectLXDUnix("", nil)
	if err != nil {
		return errors.Wrap(err, "Failed to connect to local LXD")
	}

	// Prepare the input data
	var config *initData

	// Preseed mode
	if c.flagPreseed {
		config, err = c.RunPreseed(cmd, args, d)
		if err != nil {
			return err
		}
	}

	// Auto mode
	if c.flagAuto {
		config, err = c.RunAuto(cmd, args, d)
		if err != nil {
			return err
		}
	}

	// Interactive mode
	if !c.flagAuto && !c.flagPreseed {
		config, err = c.RunInteractive(cmd, args, d)
		if err != nil {
			return err
		}
	}

	return c.ApplyConfig(cmd, args, d, *config)
}

func (c *cmdInit) availableStorageDrivers(poolType string) []string {
	drivers := []string{}

	backingFs, err := util.FilesystemDetect(shared.VarPath())
	if err != nil {
		backingFs = "dir"
	}

	// Check available backends
	for _, driver := range supportedStoragePoolDrivers {
		if poolType == "remote" && driver != "ceph" {
			continue
		}

		if poolType == "local" && driver == "ceph" {
			continue
		}

		if driver == "dir" {
			drivers = append(drivers, driver)
			continue
		}

		// btrfs can work in user namespaces too. (If
		// source=/some/path/on/btrfs is used.)
		if shared.RunningInUserNS() && (backingFs != "btrfs" || driver != "btrfs") {
			continue
		}

		// Initialize a core storage interface for the given driver.
		_, err := storageCoreInit(driver)
		if err != nil {
			continue
		}

		drivers = append(drivers, driver)
	}

	return drivers
}

func (c *cmdInit) ApplyConfig(cmd *cobra.Command, args []string, d lxd.ContainerServer, config initData) error {
	// Handle reverts
	revert := true
	reverts := []func(){}
	defer func() {
		if !revert {
			return
		}

		// Lets undo things in reverse order
		for i := len(reverts) - 1; i >= 0; i-- {
			reverts[i]()
		}
	}()

	// Apply server configuration
	if config.Config != nil && len(config.Config) > 0 {
		// Get current config
		currentServer, etag, err := d.GetServer()
		if err != nil {
			return errors.Wrap(err, "Failed to retrieve current server configuration")
		}

		// Setup reverter
		reverts = append(reverts, func() {
			d.UpdateServer(currentServer.Writable(), "")
		})

		// Prepare the update
		newServer := api.ServerPut{}
		err = shared.DeepCopy(currentServer.Writable(), &newServer)
		if err != nil {
			return errors.Wrap(err, "Failed to copy server configuration")
		}

		for k, v := range config.Config {
			newServer.Config[k] = fmt.Sprintf("%v", v)
		}

		// Apply it
		err = d.UpdateServer(newServer, etag)
		if err != nil {
			return errors.Wrap(err, "Failed to update server configuration")
		}
	}

	// Apply network configuration
	if config.Networks != nil && len(config.Networks) > 0 {
		// Get the list of networks
		networkNames, err := d.GetNetworkNames()
		if err != nil {
			return errors.Wrap(err, "Failed to retrieve list of networks")
		}

		// Network creator
		createNetwork := func(network api.NetworksPost) error {
			// Create the network if doesn't exist
			err := d.CreateNetwork(network)
			if err != nil {
				return errors.Wrapf(err, "Failed to create network '%s'", network.Name)
			}

			// Setup reverter
			reverts = append(reverts, func() {
				d.DeleteNetwork(network.Name)
			})

			return nil
		}

		// Network updater
		updateNetwork := func(network api.NetworksPost) error {
			// Get the current network
			currentNetwork, etag, err := d.GetNetwork(network.Name)
			if err != nil {
				return errors.Wrapf(err, "Failed to retrieve current network '%s'", network.Name)
			}

			// Setup reverter
			reverts = append(reverts, func() {
				d.UpdateNetwork(currentNetwork.Name, currentNetwork.Writable(), "")
			})

			// Prepare the update
			newNetwork := api.NetworkPut{}
			err = shared.DeepCopy(currentNetwork.Writable(), &newNetwork)
			if err != nil {
				return errors.Wrapf(err, "Failed to copy configuration of network '%s'", network.Name)
			}

			// Description override
			if network.Description != "" {
				newNetwork.Description = network.Description
			}

			// Config overrides
			for k, v := range network.Config {
				newNetwork.Config[k] = fmt.Sprintf("%v", v)
			}

			// Apply it
			err = d.UpdateNetwork(currentNetwork.Name, newNetwork, etag)
			if err != nil {
				return errors.Wrapf(err, "Failed to update network '%s'", network.Name)
			}

			return nil
		}

		for _, network := range config.Networks {
			// New network
			if !shared.StringInSlice(network.Name, networkNames) {
				err := createNetwork(network)
				if err != nil {
					return err
				}

				continue
			}

			// Existing network
			err := updateNetwork(network)
			if err != nil {
				return err
			}
		}
	}

	// Apply storage configuration
	if config.StoragePools != nil && len(config.StoragePools) > 0 {
		// Get the list of storagePools
		storagePoolNames, err := d.GetStoragePoolNames()
		if err != nil {
			return errors.Wrap(err, "Failed to retrieve list of storage pools")
		}

		// StoragePool creator
		createStoragePool := func(storagePool api.StoragePoolsPost) error {
			// Create the storagePool if doesn't exist
			err := d.CreateStoragePool(storagePool)
			if err != nil {
				return errors.Wrapf(err, "Failed to create storage pool '%s'", storagePool.Name)
			}

			// Setup reverter
			reverts = append(reverts, func() {
				d.DeleteStoragePool(storagePool.Name)
			})

			return nil
		}

		// StoragePool updater
		updateStoragePool := func(storagePool api.StoragePoolsPost) error {
			// Get the current storagePool
			currentStoragePool, etag, err := d.GetStoragePool(storagePool.Name)
			if err != nil {
				return errors.Wrapf(err, "Failed to retrieve current storage pool '%s'", storagePool.Name)
			}

			// Sanity check
			if currentStoragePool.Driver != storagePool.Driver {
				return fmt.Errorf("Storage pool '%s' is of type '%s' instead of '%s'", currentStoragePool.Name, currentStoragePool.Driver, storagePool.Driver)
			}

			// Setup reverter
			reverts = append(reverts, func() {
				d.UpdateStoragePool(currentStoragePool.Name, currentStoragePool.Writable(), "")
			})

			// Prepare the update
			newStoragePool := api.StoragePoolPut{}
			err = shared.DeepCopy(currentStoragePool.Writable(), &newStoragePool)
			if err != nil {
				return errors.Wrapf(err, "Failed to copy configuration of storage pool '%s'", storagePool.Name)
			}

			// Description override
			if storagePool.Description != "" {
				newStoragePool.Description = storagePool.Description
			}

			// Config overrides
			for k, v := range storagePool.Config {
				newStoragePool.Config[k] = fmt.Sprintf("%v", v)
			}

			// Apply it
			err = d.UpdateStoragePool(currentStoragePool.Name, newStoragePool, etag)
			if err != nil {
				return errors.Wrapf(err, "Failed to update storage pool '%s'", storagePool.Name)
			}

			return nil
		}

		for _, storagePool := range config.StoragePools {
			// New storagePool
			if !shared.StringInSlice(storagePool.Name, storagePoolNames) {
				err := createStoragePool(storagePool)
				if err != nil {
					return err
				}

				continue
			}

			// Existing storagePool
			err := updateStoragePool(storagePool)
			if err != nil {
				return err
			}
		}
	}

	// Apply profile configuration
	if config.Profiles != nil && len(config.Profiles) > 0 {
		// Get the list of profiles
		profileNames, err := d.GetProfileNames()
		if err != nil {
			return errors.Wrap(err, "Failed to retrieve list of profiles")
		}

		// Profile creator
		createProfile := func(profile api.ProfilesPost) error {
			// Create the profile if doesn't exist
			err := d.CreateProfile(profile)
			if err != nil {
				return errors.Wrapf(err, "Failed to create profile '%s'", profile.Name)
			}

			// Setup reverter
			reverts = append(reverts, func() {
				d.DeleteProfile(profile.Name)
			})

			return nil
		}

		// Profile updater
		updateProfile := func(profile api.ProfilesPost) error {
			// Get the current profile
			currentProfile, etag, err := d.GetProfile(profile.Name)
			if err != nil {
				return errors.Wrapf(err, "Failed to retrieve current profile '%s'", profile.Name)
			}

			// Setup reverter
			reverts = append(reverts, func() {
				d.UpdateProfile(currentProfile.Name, currentProfile.Writable(), "")
			})

			// Prepare the update
			newProfile := api.ProfilePut{}
			err = shared.DeepCopy(currentProfile.Writable(), &newProfile)
			if err != nil {
				return errors.Wrapf(err, "Failed to copy configuration of profile '%s'", profile.Name)
			}

			// Description override
			if profile.Description != "" {
				newProfile.Description = profile.Description
			}

			// Config overrides
			for k, v := range profile.Config {
				newProfile.Config[k] = fmt.Sprintf("%v", v)
			}

			// Device overrides
			for k, v := range profile.Devices {
				// New device
				_, ok := newProfile.Devices[k]
				if !ok {
					newProfile.Devices[k] = v
					continue
				}

				// Existing device
				for configKey, configValue := range v {
					newProfile.Devices[k][configKey] = fmt.Sprintf("%v", configValue)
				}
			}

			// Apply it
			err = d.UpdateProfile(currentProfile.Name, newProfile, etag)
			if err != nil {
				return errors.Wrapf(err, "Failed to update profile '%s'", profile.Name)
			}

			return nil
		}

		for _, profile := range config.Profiles {
			// New profile
			if !shared.StringInSlice(profile.Name, profileNames) {
				err := createProfile(profile)
				if err != nil {
					return err
				}

				continue
			}

			// Existing profile
			err := updateProfile(profile)
			if err != nil {
				return err
			}
		}
	}

	// Apply clustering configuration
	if config.Cluster != nil && config.Cluster.Enabled {
		// Get the current cluster configuration
		currentCluster, etag, err := d.GetCluster()
		if err != nil {
			return errors.Wrap(err, "Failed to retrieve current cluster config")
		}

		// Check if already enabled
		if !currentCluster.Enabled {
			// Setup trust relationship
			if config.Cluster.ClusterAddress != "" && config.Cluster.ClusterPassword != "" {
				// Get our certificate
				serverConfig, _, err := d.GetServer()
				if err != nil {
					return errors.Wrap(err, "Failed to retrieve server configuration")
				}

				// Try to setup trust
				err = cluster.SetupTrust(serverConfig.Environment.Certificate, config.Cluster.ClusterAddress,
					config.Cluster.ClusterCertificate, config.Cluster.ClusterPassword)
				if err != nil {
					return errors.Wrap(err, "Failed to setup cluster trust")
				}
			}

			// Configure the cluster
			op, err := d.UpdateCluster(config.Cluster.ClusterPut, etag)
			if err != nil {
				return errors.Wrap(err, "Failed to configure cluster")
			}

			err = op.Wait()
			if err != nil {
				return errors.Wrap(err, "Failed to configure cluster")
			}
		}
	}

	revert = false
	return nil
}
