package dep

import (
	"fmt"
	"io"
	"net/http"
	"testing"

	sdk "github.com/cosmos/cosmos-sdk/types"
	banktypes "github.com/cosmos/cosmos-sdk/x/bank/types"
	"github.com/medibloc/panacea-e2e/suite"
)

type depTestSuite struct {
	suite.TestSuite
}

func TestDEP(t *testing.T) {
	suite.Run(t, &depTestSuite{
		suite.NewTestSuite(suite.TestSuiteOptions{
			GenesisAccBalance: sdk.NewCoin("umed", sdk.NewInt(1000000000000000)),
			ValidatorStakes: []sdk.Coin{
				sdk.NewCoin("umed", sdk.NewInt(100000000)),
				sdk.NewCoin("umed", sdk.NewInt(100000000)),
				sdk.NewCoin("umed", sdk.NewInt(100000000)),
				sdk.NewCoin("umed", sdk.NewInt(100000000)),
			},
		}),
	})
}

func (s *depTestSuite) TestFoo() {
	addr := s.Chain.Validators[0].Address()
	endpoint := fmt.Sprintf("http://%s", s.ValResources[s.Chain.ID][0].GetHostPort("1317/tcp"))
	balances, err := queryBalances(endpoint, addr.String())
	s.Require().NoError(err)

	s.Require().Equal(sdk.NewCoins(sdk.NewCoin("umed", sdk.NewInt(999999900000000))), balances)
}

func queryBalances(endpoint, addr string) (sdk.Coins, error) {
	resp, err := http.Get(fmt.Sprintf("%s/cosmos/bank/v1beta1/balances/%s", endpoint, addr))
	if err != nil {
		return nil, fmt.Errorf("failed to execute HTTP request: %w", err)
	}
	defer resp.Body.Close()

	bz, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}

	var balancesResp banktypes.QueryAllBalancesResponse
	if err := suite.Cdc.UnmarshalJSON(bz, &balancesResp); err != nil {
		return nil, err
	}

	return balancesResp.Balances, nil
}

