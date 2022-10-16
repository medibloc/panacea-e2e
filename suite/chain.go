package suite

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/cosmos/cosmos-sdk/codec"
	"github.com/cosmos/cosmos-sdk/crypto/keys/ed25519"
	"github.com/cosmos/cosmos-sdk/crypto/keys/secp256k1"
	cryptotypes "github.com/cosmos/cosmos-sdk/crypto/types"
	sdk "github.com/cosmos/cosmos-sdk/types"
	stakingtypes "github.com/cosmos/cosmos-sdk/x/staking/types"
	panacea "github.com/medibloc/panacea-core/v2/app"
	"github.com/medibloc/panacea-core/v2/app/params"
	"github.com/ory/dockertest/v3"
	"github.com/ory/dockertest/v3/docker"
)

var (
	encodingConfig params.EncodingConfig
	Cdc            codec.Codec
)

func init() {
	panacea.SetConfig()

	encodingConfig = panacea.MakeEncodingConfig()
	encodingConfig.InterfaceRegistry.RegisterImplementations(
		(*sdk.Msg)(nil),
		&stakingtypes.MsgCreateValidator{},
	)
	encodingConfig.InterfaceRegistry.RegisterImplementations(
		(*cryptotypes.PubKey)(nil),
		&secp256k1.PubKey{},
		&ed25519.PubKey{},
	)
	Cdc = encodingConfig.Marshaler
}

type Chain struct {
	suite      *TestSuite
	dir        string
	ID         string
	validators []*validator
}

type validator struct {
	chain       *Chain
	dir         string
	index       int
	moniker     string
	dkrResource *dockertest.Resource
}

func newChain(suite *TestSuite, testID, testDir string) (*Chain, error) {
	chainDir := filepath.Join(testDir, "chain")
	if err := os.MkdirAll(chainDir, os.ModePerm); err != nil {
		return nil, err
	}

	chain := &Chain{
		suite:      suite,
		ID:         "chain-" + testID,
		dir:        chainDir,
		validators: make([]*validator, 0),
	}

	for i := 0; i < suite.opts.NumValidators; i++ {
		moniker := fmt.Sprintf("%s-val-%d", chain.ID, i)
		valDir := filepath.Join(chain.dir, moniker)
		if err := os.MkdirAll(valDir, os.ModePerm); err != nil {
			return nil, err
		}

		chain.validators = append(chain.validators, &validator{
			chain:   chain,
			dir:     valDir,
			index:   i,
			moniker: moniker,
		})
	}

	return chain, nil
}

func (c *Chain) close() error {
	for _, validator := range c.validators {
		if err := validator.stop(); err != nil {
			return err
		}
	}

	os.RemoveAll(c.dir)

	return nil
}

func (c *Chain) init() error {
	suite := c.suite

	scriptName := "panacea-init-all.sh"
	_, err := copyFile(
		filepath.Join("./scripts", scriptName),
		filepath.Join(c.dir, scriptName),
	)
	if err != nil {
		return fmt.Errorf("failed to copy file: %w", err)
	}

	r, err := suite.dkrPool.RunWithOptions(
		&dockertest.RunOptions{
			Repository: "ghcr.io/medibloc/panacea-core",
			Tag:        "master",
			NetworkID:  suite.dkrNet.Network.ID,
			Mounts:     []string{fmt.Sprintf("%s/:/root/chain", c.dir)},
			Cmd:        []string{"bash", fmt.Sprintf("/root/chain/%s", scriptName)},
			Env: []string{
				fmt.Sprintf("CHAIN_ID=%s", c.ID),
				fmt.Sprintf("NUM_VALIDATORS=%d", suite.opts.NumValidators),
				fmt.Sprintf("MNEMONIC=%s", suite.mnemonic),
				fmt.Sprintf("GEN_ACC_BALANCE=%s", suite.opts.GenesisAccBalance),
				fmt.Sprintf("STAKE=%s", suite.opts.ValidatorStake),
			},
		},
		noRestart,
	)
	if err != nil {
		return fmt.Errorf("failed to run container: %w", err)
	}
	defer func() {
		suite.dkrPool.Purge(r)
	}()

	for i := 0; i < 60; i++ {
		time.Sleep(1 * time.Second)

		container, err := suite.dkrPool.Client.InspectContainer(r.Container.ID)
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

func (c *Chain) start() error {
	for _, validator := range c.validators {
		if err := validator.start(); err != nil {
			return fmt.Errorf("failed to start validator-%d: %w", validator.index, err)
		}
	}

	return nil
}

func (v *validator) start() error {
	suite := v.chain.suite

	resource, err := suite.dkrPool.RunWithOptions(
		&dockertest.RunOptions{
			Name:       v.moniker,
			Repository: "ghcr.io/medibloc/panacea-core",
			Tag:        "master",
			NetworkID:  suite.dkrNet.Network.ID,
			Mounts:     []string{fmt.Sprintf("%s/:/root/.panacea", v.dir)},
			Cmd:        []string{"/usr/bin/panacead", "start"},
		},
		noRestart,
	)
	if err != nil {
		return fmt.Errorf("failed to run container: %w", err)
	}

	v.dkrResource = resource
	return nil
}

func (v *validator) stop() error {
	if v.dkrResource != nil {
		if err := v.chain.suite.dkrPool.Purge(v.dkrResource); err != nil {
			return err
		}
		v.dkrResource = nil
	}

	return nil
}

func (v *validator) submitGovParamChangeProposal(proposalHostPath string) error {
	proposalFilename := "proposal.json"
	_, err := copyFile(
		proposalHostPath,
		filepath.Join(v.dir, proposalFilename),
	)
	if err != nil {
		return fmt.Errorf("failed to copy file: %w", err)
	}

	cmd := []string{
		"/usr/bin/panacead",
		"tx",
		"gov",
		"submit-proposal",
		"param-change",
		fmt.Sprintf("/root/.panacea/%s", proposalFilename),
		"--from=val",
		"--fees=1000000umed",
		fmt.Sprintf("--chain-id=%s", v.chain.ID),
		"--output=json",
		"-y",
	}
	return v.executeTxCmd(cmd)
}

func (v *validator) voteGovProposal(proposalID int, voteOpt string) error {
	cmd := []string{
		"/usr/bin/panacead",
		"tx",
		"gov",
		"vote",
		fmt.Sprintf("%d", proposalID),
		voteOpt,
		"--from=val",
		"--fees=1000000umed",
		fmt.Sprintf("--chain-id=%s", v.chain.ID),
		"--output=json",
		"-y",
	}
	return v.executeTxCmd(cmd)
}

func (v *validator) executeTxCmd(cmd []string) error {
	ctx := context.Background()

	suite := v.chain.suite

	exec, err := suite.dkrPool.Client.CreateExec(docker.CreateExecOptions{
		Context:      ctx,
		AttachStdout: true,
		AttachStderr: true,
		Container:    v.dkrResource.Container.ID,
		User:         "root",
		Cmd:          cmd,
	})
	if err != nil {
		return fmt.Errorf("failed to create exec cmd in container: %w", err)
	}

	var outBuf, errBuf bytes.Buffer
	err = suite.dkrPool.Client.StartExec(exec.ID, docker.StartExecOptions{
		Context:      ctx,
		Detach:       false,
		OutputStream: &outBuf,
		ErrorStream:  &errBuf,
	})
	if err != nil {
		return fmt.Errorf("failed to start exec cmd in container: %w", err)
	}

	var txResp sdk.TxResponse
	if err := Cdc.UnmarshalJSON(outBuf.Bytes(), &txResp); err != nil {
		return fmt.Errorf("failed to unmarshal tx resp: %w", err)
	}

	endpoint := fmt.Sprintf("http://%s", v.dkrResource.GetHostPort("1317/tcp"))
	for i := 0; i < 10; i++ {
		code, err := queryTxRespCode(endpoint, txResp.TxHash)
		if err != nil {
			suite.T().Logf("failed to queryTxRespCode: %s, err:%v", txResp.TxHash, err)
		}

		if code == 0 {
			return nil
		}
	}

	return fmt.Errorf("failed to wait tx success")
}

func noRestart(config *docker.HostConfig) {
	// in this case we don't want the nodes to restart on failure
	config.RestartPolicy = docker.RestartPolicy{
		Name: "no",
	}
}
