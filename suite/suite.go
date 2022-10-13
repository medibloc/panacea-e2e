package suite

import (
	"context"
	"encoding/json"
	"fmt"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/cosmos/cosmos-sdk/server"
	srvconfig "github.com/cosmos/cosmos-sdk/server/config"
	sdk "github.com/cosmos/cosmos-sdk/types"
	genutiltypes "github.com/cosmos/cosmos-sdk/x/genutil/types"
	"github.com/ory/dockertest/v3"
	"github.com/ory/dockertest/v3/docker"
	"github.com/spf13/viper"
	"github.com/stretchr/testify/suite"
	tmconfig "github.com/tendermint/tendermint/config"
	tmjson "github.com/tendermint/tendermint/libs/json"
	rpchttp "github.com/tendermint/tendermint/rpc/client/http"
)

const (
	umedDenom       = "umed"
	initBalanceStr  = "100000000000000umed"
	minGasPrice     = "5"
	blockTimeSec    = 6                                  // 5s of timeout_commit + 1s
	unbondingPeriod = 60 * 60 * 24 * 7 * 3 * time.Second // three weeks
)

var (
	stakeAmountCoin = sdk.NewCoin(umedDenom, sdk.NewInt(100000000000))
)

type TestSuite struct {
	suite.Suite

	Chain *Chain

	dkrPool      *dockertest.Pool
	dkrNet       *dockertest.Network
	ValResources map[string][]*dockertest.Resource
}

func Run(t *testing.T, s suite.TestingSuite) {
	suite.Run(t, s)
}

func (s *TestSuite) SetupSuite() {
	s.T().Log("setting up Panacea e2e test suite...")

	var err error
	s.Chain, err = newChain()
	s.Require().NoError(err)

	s.dkrPool, err = dockertest.NewPool("")
	s.Require().NoError(err)

	s.dkrNet, err = s.dkrPool.CreateNetwork(s.Chain.ID)
	s.Require().NoError(err)

	s.ValResources = make(map[string][]*dockertest.Resource)

	s.T().Logf("starting Panacea e2e infra for the chain: chain-id:%s, datadir:%s", s.Chain.ID, s.Chain.dataDir)
	s.initNodes(s.Chain)
	s.initGenesis(s.Chain)
	s.initValidatorConfigs(s.Chain)
	s.runValidators(s.Chain, 0)
}

func (s *TestSuite) TearDownSuite() {
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
	s.Require().NoError(c.createAndInitValidators(2))

	// init a genesis file for the 1st validator
	val0ConfigDir := c.Validators[0].configDir()
	for _, val := range c.Validators {
		address := val.keyInfo.GetAddress()
		s.Require().NoError(
			addGenesisAccount(val0ConfigDir, "", initBalanceStr, address),
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
		createValmsg, err := val.buildCreateValidatorMsg(stakeAmountCoin)
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
		appConfig.MinGasPrices = fmt.Sprintf("%s%s", minGasPrice, umedDenom)

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
				"6060/tcp":  {{HostIP: "", HostPort: fmt.Sprintf("%d", 6060+portOffset)}},
				"6061/tcp":  {{HostIP: "", HostPort: fmt.Sprintf("%d", 6061+portOffset)}},
				"6062/tcp":  {{HostIP: "", HostPort: fmt.Sprintf("%d", 6062+portOffset)}},
				"6063/tcp":  {{HostIP: "", HostPort: fmt.Sprintf("%d", 6063+portOffset)}},
				"6064/tcp":  {{HostIP: "", HostPort: fmt.Sprintf("%d", 6064+portOffset)}},
				"6065/tcp":  {{HostIP: "", HostPort: fmt.Sprintf("%d", 6065+portOffset)}},
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

func noRestart(config *docker.HostConfig) {
	// in this case we don't want the nodes to restart on failure
	config.RestartPolicy = docker.RestartPolicy{
		Name: "no",
	}
}
