package suite

import (
	"fmt"
	"io/ioutil"
	"testing"
	"time"

	govtypes "github.com/cosmos/cosmos-sdk/x/gov/types"
	"github.com/ory/dockertest/v3"
	"github.com/stretchr/testify/suite"
	tmrand "github.com/tendermint/tendermint/libs/rand"
)

type TestSuite struct {
	suite.Suite

	opts TestSuiteOptions

	valMnemonic string
	accMnemonic string
	Chain       *Chain
	oracleGroup *oracleGroup

	dkrPool *dockertest.Pool
	dkrNet  *dockertest.Network
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

	s.valMnemonic, err = NewMnemonic()
	s.Require().NoError(err)

	s.accMnemonic, err = NewMnemonic()
	s.Require().NoError(err)

	s.T().Logf("creating a chain...")
	s.Chain, err = newChain(s, testID, testDir)
	s.Require().NoError(err)

	s.T().Logf("initializing a chain...")
	s.Require().NoError(s.Chain.init())
	s.T().Logf("starting a chain...")
	s.Require().NoError(s.Chain.start())

	s.waitBlock(3)
	s.T().Logf("the chain is ready")

	s.T().Logf("creating a chain...")
	s.oracleGroup, err = newOracleGroup(s, testID, testDir)
	s.Require().NoError(err)

	s.T().Logf("initializing the first oracle...")
	proposalHostPath, err := s.oracleGroup.initAndProposeFirstOracle(s.Chain.Validators[0].DkrResource)
	s.Require().NoError(err)

	s.T().Logf("submitting a gov proposal for oracles...")
	err = s.Chain.Validators[0].submitGovParamChangeProposal(proposalHostPath)
	s.Require().NoError(err)

	s.T().Logf("voting on the gov proposal...")
	proposalID := 1
	for _, validator := range s.Chain.Validators {
		err := validator.voteGovProposal(proposalID, "yes")
		s.Require().NoError(err)
	}
	s.T().Logf("waiting for the gov proposal to be passed...")
	s.waitGovProposalPassed(proposalID)

	s.T().Logf("starting the first oracle...")
	err = s.oracleGroup.oracles[0].start()
	s.Require().NoError(err)
	s.T().Logf("starting remaining oracles...")
	err = s.oracleGroup.initAndStartRemainingOracles(s.Chain.Validators[0].DkrResource)
	s.Require().NoError(err)
}

func (s *TestSuite) TearDownTest() {
	s.Require().NoError(s.oracleGroup.close())
	s.Require().NoError(s.Chain.close())
	s.Require().NoError(s.dkrPool.RemoveNetwork(s.dkrNet))
}

func (s *TestSuite) waitBlock(height int64) {
	endpoint := fmt.Sprintf("http://%s", s.Chain.Validators[0].DkrResource.GetHostPort("1317/tcp"))

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

func (s *TestSuite) waitGovProposalPassed(proposalID int) {
	endpoint := fmt.Sprintf("http://%s", s.Chain.Validators[0].DkrResource.GetHostPort("1317/tcp"))

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
		fmt.Sprintf("failed to wait gov proposal-%d passed", proposalID),
	)
}
