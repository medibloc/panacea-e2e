package suite

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io/ioutil"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/cosmos/cosmos-sdk/server"
	srvconfig "github.com/cosmos/cosmos-sdk/server/config"
	sdk "github.com/cosmos/cosmos-sdk/types"
	genutiltypes "github.com/cosmos/cosmos-sdk/x/genutil/types"
	govtypes "github.com/cosmos/cosmos-sdk/x/gov/types"
	"github.com/ory/dockertest/v3"
	"github.com/ory/dockertest/v3/docker"
	"github.com/spf13/viper"
	"github.com/stretchr/testify/suite"
	tmconfig "github.com/tendermint/tendermint/config"
	tmjson "github.com/tendermint/tendermint/libs/json"
	rpchttp "github.com/tendermint/tendermint/rpc/client/http"
)

type TestSuite struct {
	suite.Suite

	opts TestSuiteOptions

	mnemonic string
	Chain    *Chain

	dkrPool      *dockertest.Pool
	dkrNet       *dockertest.Network
	ValResources map[string][]*dockertest.Resource
}

type TestSuiteOptions struct {
	GenesisAccBalance sdk.Coin
	ValidatorStakes   []sdk.Coin
}

func (o *TestSuiteOptions) validate() error {
	if o.GenesisAccBalance.IsZero() {
		return fmt.Errorf("genesis account balance shouldn't be zero")
	}

	if len(o.ValidatorStakes) == 0 {
		return fmt.Errorf("at least one validator should be created")
	}

	for _, stake := range o.ValidatorStakes {
		if stake.Amount.GT(o.GenesisAccBalance.Amount) {
			return fmt.Errorf("validator stake cannot be greater than genesis account balance")
		}
	}

	return nil
}

func NewTestSuite(opts TestSuiteOptions) TestSuite {
	return TestSuite{
		opts: opts,
	}
}

func Run(t *testing.T, s suite.TestingSuite) {
	suite.Run(t, s)
}

func (s *TestSuite) SetupSuite() {
	err := s.opts.validate()
	s.Require().NoError(err)

	s.dkrPool, err = dockertest.NewPool("")
	s.Require().NoError(err)
}

func (s *TestSuite) SetupTest() {
	s.T().Log("setting up Panacea e2e test...")

	var err error

	s.mnemonic, err = newMnemonic()
	s.Require().NoError(err)

	s.Chain, err = newChain()
	s.Require().NoError(err)

	s.dkrNet, err = s.dkrPool.CreateNetwork(s.Chain.ID)
	s.Require().NoError(err)

	s.ValResources = make(map[string][]*dockertest.Resource)

	s.T().Logf("starting Panacea e2e infra for the chain: chain-id:%s, datadir:%s", s.Chain.ID, s.Chain.dataDir)
	s.initNodes(s.Chain)
	s.initGenesis(s.Chain)
	s.initValidatorConfigs(s.Chain)
	s.runValidators(s.Chain, 0)

	s.runDOracle()
}

func (s *TestSuite) TearDownTest() {
	s.T().Log("tearing down Panacea e2e test suite...")

	for _, vr := range s.ValResources {
		for _, r := range vr {
			s.Require().NoError(s.dkrPool.Purge(r))
		}
	}

	s.Require().NoError(s.dkrPool.RemoveNetwork(s.dkrNet))

	s.Chain.cleanup()
}

func (s *TestSuite) initNodes(c *Chain) {
	s.Require().NoError(c.createAndInitValidators(len(s.opts.ValidatorStakes), s.mnemonic))

	// init a genesis file for the 1st validator
	val0ConfigDir := c.Validators[0].configDir()
	for _, val := range c.Validators {
		address := val.keyInfo.GetAddress()
		s.Require().NoError(
			addGenesisAccount(val0ConfigDir, "", s.opts.GenesisAccBalance, address),
		)
	}

	// copy the genesis file to the remaining validators
	for _, val := range c.Validators[1:] {
		_, err := copyFile(
			filepath.Join(val0ConfigDir, "config", "genesis.json"),
			filepath.Join(val.configDir(), "config", "genesis.json"),
		)
		s.Require().NoError(err)
	}
}

func (s *TestSuite) initGenesis(c *Chain) {
	serverCtx := server.NewDefaultContext()
	config := serverCtx.Config

	config.SetRoot(c.Validators[0].configDir())
	config.Moniker = c.Validators[0].moniker

	genFilePath := config.GenesisFile()
	appGenState, genDoc, err := genutiltypes.GenesisStateFromGenFile(genFilePath)
	s.Require().NoError(err)

	appGenState, err = overrideAppState(Cdc, appGenState)
	s.Require().NoError(err)

	var genUtilGenState genutiltypes.GenesisState
	s.Require().NoError(Cdc.UnmarshalJSON(appGenState[genutiltypes.ModuleName], &genUtilGenState))

	// generate genesis txs
	genTxs := make([]json.RawMessage, len(c.Validators))
	for i, val := range c.Validators {
		createValmsg, err := val.buildCreateValidatorMsg(s.opts.ValidatorStakes[i])
		s.Require().NoError(err)
		signedTx, err := val.signMsg(createValmsg)

		s.Require().NoError(err)

		txRaw, err := Cdc.MarshalJSON(signedTx)
		s.Require().NoError(err)

		genTxs[i] = txRaw
	}

	genUtilGenState.GenTxs = genTxs

	bz, err := Cdc.MarshalJSON(&genUtilGenState)
	s.Require().NoError(err)
	appGenState[genutiltypes.ModuleName] = bz

	bz, err = json.MarshalIndent(appGenState, "", "  ")
	s.Require().NoError(err)

	genDoc.AppState = bz

	bz, err = tmjson.MarshalIndent(genDoc, "", "  ")
	s.Require().NoError(err)

	// write the updated genesis file to each validator
	for _, val := range c.Validators {
		err = writeFile(filepath.Join(val.configDir(), "config", "genesis.json"), bz)
		s.Require().NoError(err)
	}
}

// initValidatorConfigs initializes the validator configs for the given chain.
func (s *TestSuite) initValidatorConfigs(c *Chain) {
	for i, val := range c.Validators {
		tmCfgPath := filepath.Join(val.configDir(), "config", "config.toml")

		vpr := viper.New()
		vpr.SetConfigFile(tmCfgPath)
		s.Require().NoError(vpr.ReadInConfig())

		valConfig := &tmconfig.Config{}
		s.Require().NoError(vpr.Unmarshal(valConfig))

		valConfig.P2P.ListenAddress = "tcp://0.0.0.0:26656"
		valConfig.P2P.AddrBookStrict = false
		valConfig.P2P.ExternalAddress = fmt.Sprintf("%s:%d", val.instanceName(), 26656)
		valConfig.RPC.ListenAddress = "tcp://0.0.0.0:26657"
		valConfig.StateSync.Enable = false
		valConfig.LogLevel = "info"

		var peers []string

		for j := 0; j < len(c.Validators); j++ {
			if i == j {
				continue
			}

			peer := c.Validators[j]
			peerID := fmt.Sprintf("%s@%s%d:26656", peer.nodeKey.ID(), peer.moniker, j)
			peers = append(peers, peerID)
		}

		valConfig.P2P.PersistentPeers = strings.Join(peers, ",")

		tmconfig.WriteConfigFile(tmCfgPath, valConfig)

		// set application configuration
		appCfgPath := filepath.Join(val.configDir(), "config", "app.toml")

		appConfig := srvconfig.DefaultConfig()
		appConfig.API.Enable = true
		appConfig.MinGasPrices = "5umed"

		srvconfig.WriteConfigFile(appCfgPath, appConfig)
	}
}

// runValidators runs the validators in the chain
func (s *TestSuite) runValidators(c *Chain, portOffset int) {
	s.T().Logf("starting Panacea %s validator containers...", c.ID)

	s.ValResources[c.ID] = make([]*dockertest.Resource, len(c.Validators))
	for i, val := range c.Validators {
		runOpts := &dockertest.RunOptions{
			Name:      val.instanceName(),
			NetworkID: s.dkrNet.Network.ID,
			Mounts: []string{
				fmt.Sprintf("%s/:/root/.panacea", val.configDir()),
			},
			Repository: "ghcr.io/medibloc/panacea-core",
			Tag:        "master", //TODO: use specific git commit tag
			Cmd:        []string{"panacead", "start"},
		}

		// expose the first validator for debugging and communication
		if val.index == 0 {
			runOpts.PortBindings = map[docker.Port][]docker.PortBinding{
				"1317/tcp":  {{HostIP: "", HostPort: fmt.Sprintf("%d", 1317+portOffset)}},
				"9090/tcp":  {{HostIP: "", HostPort: fmt.Sprintf("%d", 9090+portOffset)}},
				"26656/tcp": {{HostIP: "", HostPort: fmt.Sprintf("%d", 26656+portOffset)}},
				"26657/tcp": {{HostIP: "", HostPort: fmt.Sprintf("%d", 26657+portOffset)}},
			}
		}

		resource, err := s.dkrPool.RunWithOptions(runOpts, noRestart)
		s.Require().NoError(err)

		s.ValResources[c.ID][i] = resource
		s.T().Logf("started Panacea %s validator container: %s", c.ID, resource.Container.ID)
	}

	rpcClient, err := rpchttp.New("tcp://localhost:26657", "/websocket")
	s.Require().NoError(err)

	s.Require().Eventually(
		func() bool {
			ctx, cancel := context.WithTimeout(context.Background(), time.Second*5)
			defer cancel()

			status, err := rpcClient.Status(ctx)
			if err != nil {
				return false
			}

			// let the node produce a few blocks
			if status.SyncInfo.CatchingUp || status.SyncInfo.LatestBlockHeight < 3 {
				return false
			}

			return true
		},
		5*time.Minute,
		time.Second,
		"Panacea node failed to produce blocks",
	)
}

func (s *TestSuite) runDOracle() {
	tmpDir, err := ioutil.TempDir("", "panacea-e2e-doracle-")
	s.Require().NoError(err)

	s.T().Logf("starting doracle...: %s", tmpDir)

	endpoint := fmt.Sprintf("http://%s", s.ValResources[s.Chain.ID][0].GetHostPort("1317/tcp"))
	blockHash, blockHeight, err := queryLatestBlock(endpoint)
	s.Require().NoError(err)

	_, err = copyFile(
		filepath.Join("./scripts/", "doracle-bootstrap.sh"),
		filepath.Join(tmpDir, "doracle-bootstrap.sh"),
	)
	s.Require().NoError(err)

	r, err := s.dkrPool.RunWithOptions(
		&dockertest.RunOptions{
			Name:       fmt.Sprintf("doracle-%s-%d-tmp", s.Chain.ID, 0),
			Repository: "ghcr.io/medibloc/panacea-doracle",
			Tag:        "pr-87",
			NetworkID:  s.dkrNet.Network.ID,
			Mounts: []string{
				fmt.Sprintf("%s/:/doracle", tmpDir),
			},
			PortBindings: map[docker.Port][]docker.PortBinding{
				"8080/tcp": {{HostIP: "", HostPort: "8080"}},
			},
			Env: []string{
				fmt.Sprintf("ORACLE_MNEMONIC=%s", s.mnemonic),
				fmt.Sprintf("ORACLE_ACC_NUM=%d", 0),
				fmt.Sprintf("ORACLE_ACC_INDEX=%d", 0),
				fmt.Sprintf("CHAIN_ID=%s", s.Chain.ID),
				fmt.Sprintf("PANACEA_VAL_HOST=%s", s.ValResources[s.Chain.ID][0].Container.Name[1:]),
				fmt.Sprintf("TRUSTED_BLOCK_HASH=%s", blockHash),
				fmt.Sprintf("TRUSTED_BLOCK_HEIGHT=%d", blockHeight),
			},
			Entrypoint: []string{
				"sh",
				"-c",
				"chmod +x /doracle/doracle-bootstrap.sh && /doracle/doracle-bootstrap.sh",
			},
		},
		noRestart,
		withSGXDevices,
	)
	s.Require().NoError(err)
	defer func(r *dockertest.Resource) { //TODO: centralize this
		s.Require().NoError(s.dkrPool.Purge(r))
	}(r)

	s.Require().Eventually(
		func() bool {
			cont, err := s.dkrPool.Client.InspectContainer(r.Container.ID)
			if err != nil {
				s.T().Logf("failed to inspect container: %v", err)
				return false
			}
			s.T().Log(cont.State.StateString())
			return cont.State.StateString() == "exited"
		},
		time.Minute,
		5*time.Second,
		"failed to get the result of doracle-bootstrap.sh",
	)

	s.Require().Equal(0, r.Container.State.ExitCode)

	_, err = copyFile(
		filepath.Join(tmpDir, "oracle-proposal.json"),
		filepath.Join(s.Chain.Validators[0].configDir(), "oracle-proposal.json"),
	)
	s.Require().NoError(err)

	s.submitOracleGovProposal("/root/.panacea/oracle-proposal.json")
	proposalID := 1
	for valIdx := 0; valIdx < len(s.ValResources[s.Chain.ID]); valIdx++ {
		s.voteGovProposal(valIdx, proposalID, "yes")
	}
	s.waitGovProposalPassed(proposalID)

	r, err = s.dkrPool.RunWithOptions(
		&dockertest.RunOptions{
			Name:       fmt.Sprintf("doracle-%s-%d", s.Chain.ID, 0),
			Repository: "ghcr.io/medibloc/panacea-doracle",
			Tag:        "pr-87",
			NetworkID:  s.dkrNet.Network.ID,
			Mounts: []string{
				fmt.Sprintf("%s/:/doracle", tmpDir),
			},
			PortBindings: map[docker.Port][]docker.PortBinding{
				"8080/tcp": {{HostIP: "", HostPort: "8080"}},
			},
			Entrypoint: []string{"ego", "run", "/usr/bin/doracled", "start"},
		},
		noRestart,
		withSGXDevices,
	)
	s.Require().NoError(err)

	for valIdx := 1; valIdx < len(s.ValResources[s.Chain.ID]); valIdx++ {
		s.runMoreDOracle(valIdx)
	}
}

func noRestart(config *docker.HostConfig) {
	// in this case we don't want the nodes to restart on failure
	config.RestartPolicy = docker.RestartPolicy{
		Name: "no",
	}
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

func (s *TestSuite) submitOracleGovProposal(proposalPath string) {
	endpoint := fmt.Sprintf("http://%s", s.ValResources[s.Chain.ID][0].GetHostPort("1317/tcp"))
	senderAddress := s.Chain.Validators[0].keyInfo.GetAddress()

	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()

	s.T().Log("Executing tx gov submit-proposal")

	cmd := []string{
		"panacead",
		"tx",
		"gov",
		"submit-proposal",
		"param-change",
		proposalPath,
		fmt.Sprintf("--from=%s", senderAddress.String()),
		fmt.Sprintf("--fees=%s", "1000000umed"),
		fmt.Sprintf("--chain-id=%s", s.Chain.ID),
		"--keyring-backend=test",
		"--output=json",
		"-y",
	}

	s.executePanaceaTxCommand(ctx, cmd, 0, endpoint)
	s.T().Logf("Successfully submitted proposal %s", proposalPath)
}

func (s *TestSuite) voteGovProposal(valIdx, proposalID int, voteOption string) {
	endpoint := fmt.Sprintf("http://%s", s.ValResources[s.Chain.ID][0].GetHostPort("1317/tcp"))
	senderAddress := s.Chain.Validators[valIdx].keyInfo.GetAddress()

	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()

	s.T().Log("Executing tx gov vote")

	cmd := []string{
		"panacead",
		"tx",
		"gov",
		"vote",
		fmt.Sprintf("%d", proposalID),
		voteOption,
		fmt.Sprintf("--from=%s", senderAddress.String()),
		fmt.Sprintf("--fees=%s", "1000000umed"),
		fmt.Sprintf("--chain-id=%s", s.Chain.ID),
		"--keyring-backend=test",
		"--output=json",
		"-y",
	}

	s.executePanaceaTxCommand(ctx, cmd, valIdx, endpoint)
	s.T().Logf("Successfully voted; val:%d, proposal:%d, option:%s", valIdx, proposalID, voteOption)
}

func (s *TestSuite) executePanaceaTxCommand(ctx context.Context, cmd []string, valIdx int, endpoint string) {
	var (
		outBuf bytes.Buffer
		errBuf bytes.Buffer
		txResp sdk.TxResponse
	)

	s.Require().Eventually(
		func() bool {
			exec, err := s.dkrPool.Client.CreateExec(docker.CreateExecOptions{
				Context:      ctx,
				AttachStdout: true,
				AttachStderr: true,
				Container:    s.ValResources[s.Chain.ID][valIdx].Container.ID,
				User:         "root",
				Cmd:          cmd,
			})
			s.Require().NoError(err)

			err = s.dkrPool.Client.StartExec(exec.ID, docker.StartExecOptions{
				Context:      ctx,
				Detach:       false,
				OutputStream: &outBuf,
				ErrorStream:  &errBuf,
			})
			s.Require().NoError(err)

			s.Require().NoError(Cdc.UnmarshalJSON(outBuf.Bytes(), &txResp))
			return strings.Contains(txResp.String(), "code: 0")
		},
		5*time.Second,
		time.Second,
		"tx returned a non-zero code; stdout: %s, stderr :%s", outBuf.String(), errBuf.String(),
	)

	s.Require().Eventually(
		func() bool {
			return queryTx(endpoint, txResp.TxHash) == nil
		},
		time.Minute,
		5*time.Second,
		"stdout: %s, stderr: %s", outBuf.String(), errBuf.String(),
	)
}

func (s *TestSuite) waitGovProposalPassed(proposalID int) {
	endpoint := fmt.Sprintf("http://%s", s.ValResources[s.Chain.ID][0].GetHostPort("1317/tcp"))

	s.Require().Eventually(
		func() bool {
			resp, err := queryGovProposal(endpoint, proposalID)
			if err != nil {
				s.T().Logf("failed to query gov proposal: %v", err)
				return false
			}
			return resp.Proposal.Status == govtypes.StatusPassed
		},
		2*time.Minute,
		5*time.Second,
		"failed to wait until gov proposal is passed",
	)
}

func (s *TestSuite) runMoreDOracle(valIdx int) {
	tmpDir, err := ioutil.TempDir("", "panacea-e2e-doracle-")
	s.Require().NoError(err)

	s.T().Logf("starting doracle-%d...: %s", valIdx, tmpDir)

	endpoint := fmt.Sprintf("http://%s", s.ValResources[s.Chain.ID][0].GetHostPort("1317/tcp"))
	blockHash, blockHeight, err := queryLatestBlock(endpoint)
	s.Require().NoError(err)

	_, err = copyFile(
		filepath.Join("./scripts/", "doracle-init-register-start.sh"),
		filepath.Join(tmpDir, "doracle-init-register-start.sh"),
	)
	s.Require().NoError(err)

	_, err = s.dkrPool.RunWithOptions(
		&dockertest.RunOptions{
			Name:       fmt.Sprintf("doracle-%s-%d", s.Chain.ID, valIdx),
			Repository: "ghcr.io/medibloc/panacea-doracle",
			Tag:        "pr-87",
			NetworkID:  s.dkrNet.Network.ID,
			Mounts: []string{
				fmt.Sprintf("%s/:/doracle", tmpDir),
			},
			Env: []string{
				fmt.Sprintf("ORACLE_MNEMONIC=%s", s.mnemonic),
				fmt.Sprintf("ORACLE_ACC_NUM=%d", 0),
				fmt.Sprintf("ORACLE_ACC_INDEX=%d", valIdx),
				fmt.Sprintf("CHAIN_ID=%s", s.Chain.ID),
				fmt.Sprintf("PANACEA_VAL_HOST=%s", s.ValResources[s.Chain.ID][0].Container.Name[1:]),
				fmt.Sprintf("TRUSTED_BLOCK_HASH=%s", blockHash),
				fmt.Sprintf("TRUSTED_BLOCK_HEIGHT=%d", blockHeight),
			},
			Entrypoint: []string{
				"sh",
				"-c",
				"chmod +x /doracle/doracle-init-register-start.sh && /doracle/doracle-init-register-start.sh",
			},
		},
		noRestart,
		withSGXDevices,
	)
	s.Require().NoError(err)
}