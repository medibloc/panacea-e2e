package dep

import (
	"fmt"
	"io"
	"net/http"
	"testing"

	sdk "github.com/cosmos/cosmos-sdk/types"
	banktypes "github.com/cosmos/cosmos-sdk/x/bank/types"
	"github.com/medibloc/panacea-core/v2/types/assets"
	doracleacc "github.com/medibloc/panacea-doracle/panacea"
	"github.com/medibloc/panacea-e2e/suite"
)

type depTestSuite struct {
	suite.TestSuite
}

func TestDEP(t *testing.T) {
	suite.Run(t, &depTestSuite{
		suite.NewTestSuite(suite.TestSuiteOptions{
			// genesis validator
			GenValBalance:  "1000000000000000umed", // 1b MED
			NumValidators:  2,
			ValidatorStake: "100000000umed", // 100 MED

			// genesis account
			GenAccBalance: "1000000000000umed", // 1m MED
			NumAccounts:   2,
		}),
	})
}

func (s *depTestSuite) TestSendCoin() {
	endpoint := fmt.Sprintf("http://%s", s.Chain.Validators[0].DkrResource.GetHostPort("1317/tcp"))
	val0 := s.Chain.Validators[0]
	addr0 := val0.GetAddress()

	newAccMnemonic, err := suite.NewMnemonic()
	s.Require().NoError(err)

	newAcc, err := doracleacc.NewOracleAccount(newAccMnemonic, uint32(0), uint32(0))
	s.Require().NoError(err)

	balance, err := queryBalances(endpoint, newAcc.GetAddress())
	s.Require().NoError(err)
	s.Equal(sdk.NewCoins(sdk.NewCoin(assets.MicroMedDenom, sdk.ZeroInt())), balance)

	err = val0.SendCoin(addr0, newAcc.GetAddress(), "100000000umed")
	s.Require().NoError(err)

	balance, err = queryBalances(endpoint, newAcc.GetAddress())
	s.Require().NoError(err)
	s.Equal(sdk.NewCoins(sdk.NewCoin(assets.MicroMedDenom, sdk.NewInt(100000000))), balance)

	bal1, err := queryBalances(endpoint, s.Chain.Accounts[0].GetAddress())
	s.Require().Equal(sdk.NewCoins(sdk.NewCoin(assets.MicroMedDenom, sdk.NewInt(1000000000000))), bal1)

	bal2, err := queryBalances(endpoint, s.Chain.Accounts[1].GetAddress())
	s.Require().Equal(sdk.NewCoins(sdk.NewCoin(assets.MicroMedDenom, sdk.NewInt(1000000000000))), bal2)
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
