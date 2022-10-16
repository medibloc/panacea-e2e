package suite

import (
	"fmt"
	"io/ioutil"
	"testing"
	"time"

	"github.com/ory/dockertest/v3"
	"github.com/stretchr/testify/suite"
	tmrand "github.com/tendermint/tendermint/libs/rand"
)

type TestSuite struct {
	suite.Suite

	opts TestSuiteOptions

	mnemonic string
	Chain    *Chain

	dkrPool *dockertest.Pool
	dkrNet  *dockertest.Network
	// OralceDkrResources    []*dockertest.Resource
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
	var err error
	s.dkrPool, err = dockertest.NewPool("")
	s.Require().NoError(err)
}

func (s *TestSuite) SetupTest() {
	var err error

	testID := tmrand.Str(6)
	testDir, err := ioutil.TempDir("", fmt.Sprintf("panacea-e2e-%s", testID))
	s.Require().NoError(err)

	s.T().Logf("setting up Panacea e2e test; testID:%s, testDir:%s", testID, testDir)

	s.dkrNet, err = s.dkrPool.CreateNetwork(testID)
	s.Require().NoError(err)

	s.mnemonic, err = newMnemonic()
	s.Require().NoError(err)

	s.Chain, err = newChain(s, testID, testDir)
	s.Require().NoError(err)

	s.Require().NoError(s.Chain.init())
	s.Require().NoError(s.Chain.start())

	s.waitBlock(3)
}

func (s *TestSuite) TearDownTest() {
	s.Require().NoError(s.Chain.close())
	s.Require().NoError(s.dkrPool.RemoveNetwork(s.dkrNet))
}

func (s *TestSuite) waitBlock(height int64) {
	endpoint := fmt.Sprintf("http://%s", s.Chain.validators[0].dkrResource.GetHostPort("1317/tcp"))

	s.Require().Eventually(
		func() bool {
			_, latestHeight, err := queryLatestBlock(endpoint)
			if err != nil {
				s.T().Logf("failed to query latest block: %v", err)
				return false
			}
			return latestHeight >= height
		},
		2*time.Minute,
		5*time.Second,
		fmt.Sprintf("failed to wait block: %d", height),
	)
}

// func (s *TestSuite) runDOracle() {
// 	tmpDir, err := ioutil.TempDir("", "panacea-e2e-doracle-")
// 	s.Require().NoError(err)

// 	s.T().Logf("starting doracle...: %s", tmpDir)

// 	endpoint := fmt.Sprintf("http://%s", s.ValidatorDkrResources[s.Chain.ID][0].GetHostPort("1317/tcp"))
// 	blockHash, blockHeight, err := queryLatestBlock(endpoint)
// 	s.Require().NoError(err)

// 	_, err = copyFile(
// 		filepath.Join("./scripts/", "doracle-bootstrap.sh"),
// 		filepath.Join(tmpDir, "doracle-bootstrap.sh"),
// 	)
// 	s.Require().NoError(err)

// 	r, err := s.dkrPool.RunWithOptions(
// 		&dockertest.RunOptions{
// 			Name:       fmt.Sprintf("doracle-%s-%d-tmp", s.Chain.ID, 0),
// 			Repository: "ghcr.io/medibloc/panacea-doracle",
// 			Tag:        "pr-87",
// 			NetworkID:  s.dkrNet.Network.ID,
// 			Mounts: []string{
// 				fmt.Sprintf("%s/:/doracle", tmpDir),
// 			},
// 			PortBindings: map[docker.Port][]docker.PortBinding{
// 				"8080/tcp": {{HostIP: "", HostPort: "8080"}},
// 			},
// 			Env: []string{
// 				fmt.Sprintf("ORACLE_MNEMONIC=%s", s.mnemonic),
// 				fmt.Sprintf("ORACLE_ACC_NUM=%d", 0),
// 				fmt.Sprintf("ORACLE_ACC_INDEX=%d", 0),
// 				fmt.Sprintf("CHAIN_ID=%s", s.Chain.ID),
// 				fmt.Sprintf("PANACEA_VAL_HOST=%s", s.ValidatorDkrResources[s.Chain.ID][0].Container.Name[1:]),
// 				fmt.Sprintf("TRUSTED_BLOCK_HASH=%s", blockHash),
// 				fmt.Sprintf("TRUSTED_BLOCK_HEIGHT=%d", blockHeight),
// 			},
// 			Entrypoint: []string{
// 				"sh",
// 				"-c",
// 				"chmod +x /doracle/doracle-bootstrap.sh && /doracle/doracle-bootstrap.sh",
// 			},
// 		},
// 		noRestart,
// 		withSGXDevices,
// 	)
// 	s.Require().NoError(err)
// 	defer func(r *dockertest.Resource) { //TODO: centralize this
// 		s.Require().NoError(s.dkrPool.Purge(r))
// 	}(r)

// 	s.Require().Eventually(
// 		func() bool {
// 			cont, err := s.dkrPool.Client.InspectContainer(r.Container.ID)
// 			if err != nil {
// 				s.T().Logf("failed to inspect container: %v", err)
// 				return false
// 			}
// 			s.T().Log(cont.State.StateString())
// 			return cont.State.StateString() == "exited"
// 		},
// 		time.Minute,
// 		5*time.Second,
// 		"failed to get the result of doracle-bootstrap.sh",
// 	)

// 	s.Require().Equal(0, r.Container.State.ExitCode)

// 	_, err = copyFile(
// 		filepath.Join(tmpDir, "oracle-proposal.json"),
// 		filepath.Join(s.Chain.Validators[0].configDir(), "oracle-proposal.json"),
// 	)
// 	s.Require().NoError(err)

// 	s.submitOracleGovProposal("/root/.panacea/oracle-proposal.json")
// 	proposalID := 1
// 	for valIdx := 0; valIdx < len(s.ValidatorDkrResources[s.Chain.ID]); valIdx++ {
// 		s.voteGovProposal(valIdx, proposalID, "yes")
// 	}
// 	s.waitGovProposalPassed(proposalID)

// 	r, err = s.dkrPool.RunWithOptions(
// 		&dockertest.RunOptions{
// 			Name:       fmt.Sprintf("doracle-%s-%d", s.Chain.ID, 0),
// 			Repository: "ghcr.io/medibloc/panacea-doracle",
// 			Tag:        "pr-87",
// 			NetworkID:  s.dkrNet.Network.ID,
// 			Mounts: []string{
// 				fmt.Sprintf("%s/:/doracle", tmpDir),
// 			},
// 			PortBindings: map[docker.Port][]docker.PortBinding{
// 				"8080/tcp": {{HostIP: "", HostPort: "8080"}},
// 			},
// 			Entrypoint: []string{"ego", "run", "/usr/bin/doracled", "start"},
// 		},
// 		noRestart,
// 		withSGXDevices,
// 	)
// 	s.Require().NoError(err)

// 	for valIdx := 1; valIdx < len(s.ValidatorDkrResources[s.Chain.ID]); valIdx++ {
// 		s.runMoreDOracle(valIdx)
// 	}
// }

// func noRestart(config *docker.HostConfig) {
// 	// in this case we don't want the nodes to restart on failure
// 	config.RestartPolicy = docker.RestartPolicy{
// 		Name: "no",
// 	}
// }

// func withSGXDevices(config *docker.HostConfig) {
// 	if config.Devices == nil {
// 		config.Devices = make([]docker.Device, 0)
// 	}

// 	config.Devices = append(config.Devices, []docker.Device{
// 		{
// 			PathOnHost:        "/dev/sgx_enclave",
// 			PathInContainer:   "/dev/sgx_enclave",
// 			CgroupPermissions: "rwm",
// 		},
// 		{
// 			PathOnHost:        "/dev/sgx_provision",
// 			PathInContainer:   "/dev/sgx_provision",
// 			CgroupPermissions: "rwm",
// 		},
// 	}...)
// }

// func (s *TestSuite) submitOracleGovProposal(proposalPath string) {
// 	endpoint := fmt.Sprintf("http://%s", s.ValidatorDkrResources[s.Chain.ID][0].GetHostPort("1317/tcp"))
// 	senderAddress := s.Chain.Validators[0].keyInfo.GetAddress()

// 	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
// 	defer cancel()

// 	s.T().Log("Executing tx gov submit-proposal")

// 	cmd := []string{
// 		"panacead",
// 		"tx",
// 		"gov",
// 		"submit-proposal",
// 		"param-change",
// 		proposalPath,
// 		fmt.Sprintf("--from=%s", senderAddress.String()),
// 		fmt.Sprintf("--fees=%s", "1000000umed"),
// 		fmt.Sprintf("--chain-id=%s", s.Chain.ID),
// 		"--keyring-backend=test",
// 		"--output=json",
// 		"-y",
// 	}

// 	s.executePanaceaTxCommand(ctx, cmd, 0, endpoint)
// 	s.T().Logf("Successfully submitted proposal %s", proposalPath)
// }

// func (s *TestSuite) voteGovProposal(valIdx, proposalID int, voteOption string) {
// 	endpoint := fmt.Sprintf("http://%s", s.ValidatorDkrResources[s.Chain.ID][0].GetHostPort("1317/tcp"))
// 	senderAddress := s.Chain.Validators[valIdx].keyInfo.GetAddress()

// 	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
// 	defer cancel()

// 	s.T().Log("Executing tx gov vote")

// 	cmd := []string{
// 		"panacead",
// 		"tx",
// 		"gov",
// 		"vote",
// 		fmt.Sprintf("%d", proposalID),
// 		voteOption,
// 		fmt.Sprintf("--from=%s", senderAddress.String()),
// 		fmt.Sprintf("--fees=%s", "1000000umed"),
// 		fmt.Sprintf("--chain-id=%s", s.Chain.ID),
// 		"--keyring-backend=test",
// 		"--output=json",
// 		"-y",
// 	}

// 	s.executePanaceaTxCommand(ctx, cmd, valIdx, endpoint)
// 	s.T().Logf("Successfully voted; val:%d, proposal:%d, option:%s", valIdx, proposalID, voteOption)
// }

// func (s *TestSuite) executePanaceaTxCommand(ctx context.Context, cmd []string, valIdx int, endpoint string) {
// 	var (
// 		outBuf bytes.Buffer
// 		errBuf bytes.Buffer
// 		txResp sdk.TxResponse
// 	)

// 	s.Require().Eventually(
// 		func() bool {
// 			exec, err := s.dkrPool.Client.CreateExec(docker.CreateExecOptions{
// 				Context:      ctx,
// 				AttachStdout: true,
// 				AttachStderr: true,
// 				Container:    s.ValidatorDkrResources[s.Chain.ID][valIdx].Container.ID,
// 				User:         "root",
// 				Cmd:          cmd,
// 			})
// 			s.Require().NoError(err)

// 			err = s.dkrPool.Client.StartExec(exec.ID, docker.StartExecOptions{
// 				Context:      ctx,
// 				Detach:       false,
// 				OutputStream: &outBuf,
// 				ErrorStream:  &errBuf,
// 			})
// 			s.Require().NoError(err)

// 			s.Require().NoError(Cdc.UnmarshalJSON(outBuf.Bytes(), &txResp))
// 			return strings.Contains(txResp.String(), "code: 0")
// 		},
// 		5*time.Second,
// 		time.Second,
// 		"tx returned a non-zero code; stdout: %s, stderr :%s", outBuf.String(), errBuf.String(),
// 	)

// 	s.Require().Eventually(
// 		func() bool {
// 			return queryTx(endpoint, txResp.TxHash) == nil
// 		},
// 		time.Minute,
// 		5*time.Second,
// 		"stdout: %s, stderr: %s", outBuf.String(), errBuf.String(),
// 	)
// }

// func (s *TestSuite) waitGovProposalPassed(proposalID int) {
// 	endpoint := fmt.Sprintf("http://%s", s.ValidatorDkrResources[s.Chain.ID][0].GetHostPort("1317/tcp"))

// 	s.Require().Eventually(
// 		func() bool {
// 			resp, err := queryGovProposal(endpoint, proposalID)
// 			if err != nil {
// 				s.T().Logf("failed to query gov proposal: %v", err)
// 				return false
// 			}
// 			return resp.Proposal.Status == govtypes.StatusPassed
// 		},
// 		2*time.Minute,
// 		5*time.Second,
// 		"failed to wait until gov proposal is passed",
// 	)
// }

// func (s *TestSuite) runMoreDOracle(valIdx int) {
// 	tmpDir, err := ioutil.TempDir("", "panacea-e2e-doracle-")
// 	s.Require().NoError(err)

// 	s.T().Logf("starting doracle-%d...: %s", valIdx, tmpDir)

// 	endpoint := fmt.Sprintf("http://%s", s.ValidatorDkrResources[s.Chain.ID][0].GetHostPort("1317/tcp"))
// 	blockHash, blockHeight, err := queryLatestBlock(endpoint)
// 	s.Require().NoError(err)

// 	_, err = copyFile(
// 		filepath.Join("./scripts/", "doracle-init-register-start.sh"),
// 		filepath.Join(tmpDir, "doracle-init-register-start.sh"),
// 	)
// 	s.Require().NoError(err)

// 	_, err = s.dkrPool.RunWithOptions(
// 		&dockertest.RunOptions{
// 			Name:       fmt.Sprintf("doracle-%s-%d", s.Chain.ID, valIdx),
// 			Repository: "ghcr.io/medibloc/panacea-doracle",
// 			Tag:        "pr-87",
// 			NetworkID:  s.dkrNet.Network.ID,
// 			Mounts: []string{
// 				fmt.Sprintf("%s/:/doracle", tmpDir),
// 			},
// 			Env: []string{
// 				fmt.Sprintf("ORACLE_MNEMONIC=%s", s.mnemonic),
// 				fmt.Sprintf("ORACLE_ACC_NUM=%d", 0),
// 				fmt.Sprintf("ORACLE_ACC_INDEX=%d", valIdx),
// 				fmt.Sprintf("CHAIN_ID=%s", s.Chain.ID),
// 				fmt.Sprintf("PANACEA_VAL_HOST=%s", s.ValidatorDkrResources[s.Chain.ID][0].Container.Name[1:]),
// 				fmt.Sprintf("TRUSTED_BLOCK_HASH=%s", blockHash),
// 				fmt.Sprintf("TRUSTED_BLOCK_HEIGHT=%d", blockHeight),
// 			},
// 			Entrypoint: []string{
// 				"sh",
// 				"-c",
// 				"chmod +x /doracle/doracle-init-register-start.sh && /doracle/doracle-init-register-start.sh",
// 			},
// 		},
// 		noRestart,
// 		withSGXDevices,
// 	)
// 	s.Require().NoError(err)
// }
