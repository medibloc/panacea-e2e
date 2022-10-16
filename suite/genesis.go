package suite

import (
	"encoding/json"
	"fmt"
	"os"
	"time"

	"github.com/cosmos/cosmos-sdk/codec"
	"github.com/cosmos/cosmos-sdk/server"
	sdk "github.com/cosmos/cosmos-sdk/types"
	authtypes "github.com/cosmos/cosmos-sdk/x/auth/types"
	banktypes "github.com/cosmos/cosmos-sdk/x/bank/types"
	crisistypes "github.com/cosmos/cosmos-sdk/x/crisis/types"
	distrtypes "github.com/cosmos/cosmos-sdk/x/distribution/types"
	"github.com/cosmos/cosmos-sdk/x/genutil"
	genutiltypes "github.com/cosmos/cosmos-sdk/x/genutil/types"
	govtypes "github.com/cosmos/cosmos-sdk/x/gov/types"
	minttypes "github.com/cosmos/cosmos-sdk/x/mint/types"
	slashingtypes "github.com/cosmos/cosmos-sdk/x/slashing/types"
	stakingtypes "github.com/cosmos/cosmos-sdk/x/staking/types"
	"github.com/medibloc/panacea-core/v2/types/assets"
	tmtypes "github.com/tendermint/tendermint/types"
)

func getGenDoc(path string) (*tmtypes.GenesisDoc, error) {
	serverCtx := server.NewDefaultContext()
	config := serverCtx.Config
	config.SetRoot(path)

	genFile := config.GenesisFile()
	doc := &tmtypes.GenesisDoc{}

	if _, err := os.Stat(genFile); err != nil {
		if !os.IsNotExist(err) {
			return nil, err
		}
	} else {
		var err error

		doc, err = tmtypes.GenesisDocFromFile(genFile)
		if err != nil {
			return nil, fmt.Errorf("failed to read genesis doc from file: %w", err)
		}
	}

	return doc, nil
}

func addGenesisAccount(path, moniker string, amount sdk.Coin, accAddr sdk.AccAddress) error {
	serverCtx := server.NewDefaultContext()
	config := serverCtx.Config

	config.SetRoot(path)
	config.Moniker = moniker

	balances := banktypes.Balance{Address: accAddr.String(), Coins: sdk.NewCoins(amount)}
	genAccount := authtypes.NewBaseAccount(accAddr, nil, 0, 0)

	genFile := config.GenesisFile()
	appState, genDoc, err := genutiltypes.GenesisStateFromGenFile(genFile)
	if err != nil {
		return fmt.Errorf("failed to unmarshal genesis state: %w", err)
	}

	authGenState := authtypes.GetGenesisStateFromAppState(Cdc, appState)

	accs, err := authtypes.UnpackAccounts(authGenState.Accounts)
	if err != nil {
		return fmt.Errorf("failed to get accounts from any: %w", err)
	}

	if accs.Contains(accAddr) {
		return fmt.Errorf("failed to add account to genesis state; account already exists: %s", accAddr)
	}

	// Add the new account to the set of genesis accounts and sanitize the
	// accounts afterwards.
	accs = append(accs, genAccount)
	accs = authtypes.SanitizeGenesisAccounts(accs)

	genAccs, err := authtypes.PackAccounts(accs)
	if err != nil {
		return fmt.Errorf("failed to convert accounts into any's: %w", err)
	}

	authGenState.Accounts = genAccs

	authGenStateBz, err := Cdc.MarshalJSON(&authGenState)
	if err != nil {
		return fmt.Errorf("failed to marshal auth genesis state: %w", err)
	}

	appState[authtypes.ModuleName] = authGenStateBz

	bankGenState := banktypes.GetGenesisStateFromAppState(Cdc, appState)
	bankGenState.Balances = append(bankGenState.Balances, balances)
	bankGenState.Balances = banktypes.SanitizeGenesisBalances(bankGenState.Balances)

	bankGenStateBz, err := Cdc.MarshalJSON(bankGenState)
	if err != nil {
		return fmt.Errorf("failed to marshal bank genesis state: %w", err)
	}
	appState[banktypes.ModuleName] = bankGenStateBz

	// Refactor to separate method
	amnt := sdk.NewInt(10000)
	quorum, _ := sdk.NewDecFromStr("0.000000000000000001")
	threshold, _ := sdk.NewDecFromStr("0.000000000000000001")

	govState := govtypes.NewGenesisState(1,
		govtypes.NewDepositParams(sdk.NewCoins(sdk.NewCoin("photon", amnt)), 10*time.Minute),
		govtypes.NewVotingParams(15*time.Second),
		govtypes.NewTallyParams(quorum, threshold, govtypes.DefaultVetoThreshold),
	)

	govGenStateBz, err := Cdc.MarshalJSON(govState)
	if err != nil {
		return fmt.Errorf("failed to marshal gov genesis state: %w", err)
	}
	appState[govtypes.ModuleName] = govGenStateBz

	appStateJSON, err := json.Marshal(appState)
	if err != nil {
		return fmt.Errorf("failed to marshal application genesis state: %w", err)
	}

	genDoc.AppState = appStateJSON
	return genutil.ExportGenesisFile(genDoc, genFile)
}

const (
	blockTimeSec    = 6                                  // 5s of timeout_commit + 1s
	unbondingPeriod = 60 * 60 * 24 * 7 * 3 * time.Second // three weeks
)

// TODO: make this function in panacea-core public
// overrideAppState overrides some parameters in the genesis doc to the panacea-specific values.
func overrideAppState(cdc codec.JSONCodec, appState map[string]json.RawMessage) (map[string]json.RawMessage, error) {
	var stakingGenState stakingtypes.GenesisState
	if err := cdc.UnmarshalJSON(appState[stakingtypes.ModuleName], &stakingGenState); err != nil {
		return nil, err
	}
	stakingGenState.Params.UnbondingTime = unbondingPeriod
	stakingGenState.Params.MaxValidators = 50
	stakingGenState.Params.BondDenom = assets.MicroMedDenom
	appState[stakingtypes.ModuleName] = cdc.MustMarshalJSON(&stakingGenState)

	var mintGenState minttypes.GenesisState
	if err := cdc.UnmarshalJSON(appState[minttypes.ModuleName], &mintGenState); err != nil {
		return nil, err
	}
	mintGenState.Minter = minttypes.InitialMinter(sdk.NewDecWithPrec(7, 2)) // 7% inflation
	mintGenState.Params.MintDenom = assets.MicroMedDenom
	mintGenState.Params.InflationRateChange = sdk.NewDecWithPrec(3, 2) // 3%
	mintGenState.Params.InflationMin = sdk.NewDecWithPrec(7, 2)        // 7%
	mintGenState.Params.InflationMax = sdk.NewDecWithPrec(10, 2)       // 10%
	mintGenState.Params.BlocksPerYear = uint64(60*60*24*365) / uint64(blockTimeSec)
	appState[minttypes.ModuleName] = cdc.MustMarshalJSON(&mintGenState)

	var distrGenState distrtypes.GenesisState
	if err := cdc.UnmarshalJSON(appState[distrtypes.ModuleName], &distrGenState); err != nil {
		return nil, err
	}
	distrGenState.Params.CommunityTax = sdk.ZeroDec()
	appState[distrtypes.ModuleName] = cdc.MustMarshalJSON(&distrGenState)

	var govGenState govtypes.GenesisState
	if err := cdc.UnmarshalJSON(appState[govtypes.ModuleName], &govGenState); err != nil {
		return nil, err
	}
	minDepositTokens := sdk.TokensFromConsensusPower(1, sdk.DefaultPowerReduction) // 1 MED
	govGenState.DepositParams.MinDeposit = sdk.Coins{sdk.NewCoin(assets.MicroMedDenom, minDepositTokens)}
	govGenState.DepositParams.MaxDepositPeriod = 30 * time.Second
	govGenState.VotingParams.VotingPeriod = 30 * time.Second
	appState[govtypes.ModuleName] = cdc.MustMarshalJSON(&govGenState)

	var crisisGenState crisistypes.GenesisState
	if err := cdc.UnmarshalJSON(appState[crisistypes.ModuleName], &crisisGenState); err != nil {
		return nil, err
	}
	crisisGenState.ConstantFee = sdk.NewCoin(assets.MicroMedDenom, sdk.NewInt(1000000000000)) // Spend 1,000,000 MED for invariants check
	appState[crisistypes.ModuleName] = cdc.MustMarshalJSON(&crisisGenState)

	var slashingGenState slashingtypes.GenesisState
	if err := cdc.UnmarshalJSON(appState[slashingtypes.ModuleName], &slashingGenState); err != nil {
		return nil, err
	}
	slashingGenState.Params.SignedBlocksWindow = 10000
	slashingGenState.Params.MinSignedPerWindow = sdk.NewDecWithPrec(5, 2)
	slashingGenState.Params.SlashFractionDoubleSign = sdk.NewDecWithPrec(5, 2) // 5%
	slashingGenState.Params.SlashFractionDowntime = sdk.NewDecWithPrec(1, 4)   // 0.01%
	appState[slashingtypes.ModuleName] = cdc.MustMarshalJSON(&slashingGenState)

	return appState, nil
}
