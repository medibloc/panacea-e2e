package suite

import (
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/ory/dockertest/v3"
	"github.com/ory/dockertest/v3/docker"
)

type oracleGroup struct {
	suite   *TestSuite
	dir     string
	oracles []*oracle
}

type oracle struct {
	group       *oracleGroup
	dir         string
	index       int
	moniker     string
	dkrResource *dockertest.Resource
}

func newOracleGroup(suite *TestSuite, testID, testDir string) (*oracleGroup, error) {
	groupDir := filepath.Join(testDir, "oracle-group")
	if err := os.MkdirAll(groupDir, os.ModePerm); err != nil {
		return nil, err
	}

	group := &oracleGroup{
		suite:   suite,
		dir:     groupDir,
		oracles: make([]*oracle, 0),
	}

	for i := 0; i < suite.opts.NumValidators; i++ {
		moniker := fmt.Sprintf("%s-oracle-%d", suite.Chain.ID, i)
		oracleDir := filepath.Join(groupDir, moniker)
		if err := os.MkdirAll(oracleDir, os.ModePerm); err != nil {
			return nil, err
		}

		group.oracles = append(group.oracles, &oracle{
			group:   group,
			dir:     oracleDir,
			index:   i,
			moniker: moniker,
		})
	}

	return group, nil
}

func (g *oracleGroup) close() error {
	for _, oracle := range g.oracles {
		if err := oracle.stop(); err != nil {
			return err
		}
	}

	os.RemoveAll(g.dir)

	return nil
}

func (g *oracleGroup) initAndProposeFirstOracle(validatorResource *dockertest.Resource) (string, error) {
	return g.oracles[0].initAndPropose(validatorResource)
}

func (g *oracleGroup) initAndStartRemainingOracles(validatorResource *dockertest.Resource) error {
	for _, oracle := range g.oracles[1:] {
		if err := oracle.initAndRegister(validatorResource); err != nil {
			return fmt.Errorf("failed to init and register oracle: %w", err)
		}

		if err := oracle.start(); err != nil {
			return fmt.Errorf("failed to start oracle: %w", err)
		}
	}
	
	return nil
}

func (o *oracle) initAndPropose(validatorResource *dockertest.Resource) (string, error) {
	endpoint := fmt.Sprintf("http://%s", validatorResource.GetHostPort("1317/tcp"))
	blockHash, blockHeight, err := queryLatestBlock(endpoint)
	if err != nil {
		return "", fmt.Errorf("failed to query latest block: %w", err)
	}

	scriptName := "oracle-init-propose.sh"
	_, err = copyFile(
		filepath.Join("./scripts", scriptName),
		filepath.Join(o.dir, scriptName),
	)
	if err != nil {
		return "", fmt.Errorf("failed to copy file: %w", err)
	}

	suite := o.group.suite

	r, err := suite.dkrPool.RunWithOptions(
		&dockertest.RunOptions{
			Repository: "ghcr.io/medibloc/panacea-doracle",
			Tag:        "pr-87",
			NetworkID:  suite.dkrNet.Network.ID,
			Mounts:     []string{fmt.Sprintf("%s/:/doracle", o.dir)},
			Cmd:        []string{"bash", fmt.Sprintf("/doracle/%s", scriptName)},
			Env: []string{
				fmt.Sprintf("ORACLE_MNEMONIC=%s", suite.mnemonic),
				fmt.Sprintf("ORACLE_ACC_NUM=%d", 0),
				fmt.Sprintf("ORACLE_ACC_INDEX=%d", 0),
				fmt.Sprintf("CHAIN_ID=%s", suite.Chain.ID),
				fmt.Sprintf("PANACEA_VAL_HOST=%s", validatorResource.Container.Name[1:]),
				fmt.Sprintf("TRUSTED_BLOCK_HASH=%s", blockHash),
				fmt.Sprintf("TRUSTED_BLOCK_HEIGHT=%d", blockHeight),
			},
		},
		noRestart,
		withSGXDevices,
	)
	if err != nil {
		return "", fmt.Errorf("failed to run container: %w", err)
	}
	defer func() {
		suite.dkrPool.Purge(r)
	}()

	for i := 0; i < 60; i++ {
		time.Sleep(1 * time.Second)

		container, err := suite.dkrPool.Client.InspectContainer(r.Container.ID)
		if err != nil {
			return "", fmt.Errorf("failed to inspect container: %w", err)
		}

		if container.State.StateString() == "exited" {
			if container.State.ExitCode == 0 {
				return filepath.Join(o.dir, "oracle-proposal.json"), nil
			} else {
				return "", fmt.Errorf("container was exited with code %d", container.State.ExitCode)
			}
		}
	}

	return "", fmt.Errorf("failed to wait until the container is exited")
}

func (o *oracle) initAndRegister(validatorResource *dockertest.Resource) error {
	endpoint := fmt.Sprintf("http://%s", validatorResource.GetHostPort("1317/tcp"))
	blockHash, blockHeight, err := queryLatestBlock(endpoint)
	if err != nil {
		return fmt.Errorf("failed to query latest block: %w", err)
	}

	scriptName := "oracle-init-register.sh"
	_, err = copyFile(
		filepath.Join("./scripts", scriptName),
		filepath.Join(o.dir, scriptName),
	)
	if err != nil {
		return fmt.Errorf("failed to copy file: %w", err)
	}

	suite := o.group.suite

	resource, err := suite.dkrPool.RunWithOptions(
		&dockertest.RunOptions{
			Repository: "ghcr.io/medibloc/panacea-doracle",
			Tag:        "pr-87",
			NetworkID:  suite.dkrNet.Network.ID,
			Mounts:     []string{fmt.Sprintf("%s/:/doracle", o.dir)},
			Cmd:        []string{"bash", fmt.Sprintf("/doracle/%s", scriptName)},
			Env: []string{
				fmt.Sprintf("ORACLE_MNEMONIC=%s", suite.mnemonic),
				fmt.Sprintf("ORACLE_ACC_NUM=%d", 0),
				fmt.Sprintf("ORACLE_ACC_INDEX=%d", 0),
				fmt.Sprintf("CHAIN_ID=%s", suite.Chain.ID),
				fmt.Sprintf("PANACEA_VAL_HOST=%s", validatorResource.Container.Name[1:]),
				fmt.Sprintf("TRUSTED_BLOCK_HASH=%s", blockHash),
				fmt.Sprintf("TRUSTED_BLOCK_HEIGHT=%d", blockHeight),
			},
		},
		noRestart,
		withSGXDevices,
	)
	if err != nil {
		return fmt.Errorf("failed to run container: %w", err)
	}
	defer func() {
		suite.dkrPool.Purge(resource)
	}()

	for i := 0; i < 200; i++ {
		time.Sleep(1 * time.Second)

		container, err := suite.dkrPool.Client.InspectContainer(resource.Container.ID)
		if err != nil {
			return fmt.Errorf("failed to inspect container: %w", err)
		}

		if container.State.StateString() == "exited" {
			if container.State.ExitCode == 0 {
				return nil
			} else {
				return fmt.Errorf("container was exited with code %d", container.State.ExitCode)
			}
		}
	}

	return fmt.Errorf("failed to wait until the container is exited")
}

func (o *oracle) start() error {
	suite := o.group.suite

	resource, err := suite.dkrPool.RunWithOptions(
		&dockertest.RunOptions{
			Name:       o.moniker,
			Repository: "ghcr.io/medibloc/panacea-doracle",
			Tag:        "pr-87",
			NetworkID:  suite.dkrNet.Network.ID,
			Mounts:     []string{fmt.Sprintf("%s/:/doracle", o.dir)},
			Cmd:        []string{"ego", "run", "/usr/bin/doracled", "start"},
		},
		noRestart,
		withSGXDevices,
	)
	if err != nil {
		return fmt.Errorf("failed to run container: %w", err)
	}

	o.dkrResource = resource
	return nil
}

func (o *oracle) stop() error {
	if o.dkrResource != nil {
		if err := o.group.suite.dkrPool.Purge(o.dkrResource); err != nil {
			return err
		}
		o.dkrResource = nil
	}

	return nil
}

func withSGXDevices(config *docker.HostConfig) {
	if config.Devices == nil {
		config.Devices = make([]docker.Device, 0)
	}

	config.Devices = append(config.Devices, []docker.Device{
		{
			PathOnHost:        "/dev/sgx_enclave",
			PathInContainer:   "/dev/sgx_enclave",
			CgroupPermissions: "rwm",
		},
		{
			PathOnHost:        "/dev/sgx_provision",
			PathInContainer:   "/dev/sgx_provision",
			CgroupPermissions: "rwm",
		},
	}...)
}
