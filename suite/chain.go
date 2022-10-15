package suite

import (
	"fmt"
	"io/ioutil"
	"os"

	"github.com/cosmos/cosmos-sdk/codec"
	"github.com/cosmos/cosmos-sdk/crypto/keys/ed25519"
	"github.com/cosmos/cosmos-sdk/crypto/keys/secp256k1"
	cryptotypes "github.com/cosmos/cosmos-sdk/crypto/types"
	sdk "github.com/cosmos/cosmos-sdk/types"
	stakingtypes "github.com/cosmos/cosmos-sdk/x/staking/types"
	panacea "github.com/medibloc/panacea-core/v2/app"
	"github.com/medibloc/panacea-core/v2/app/params"
	tmrand "github.com/tendermint/tendermint/libs/rand"
)

const (
	keyringAppName    = "panacea-e2e"
	keyringPassphrase = "testpassphrase"
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
	dataDir    string
	ID         string
	Validators []*Validator
}

func newChain() (*Chain, error) {
	tmpDir, err := ioutil.TempDir("", "panacea-e2e-")
	if err != nil {
		return nil, err
	}

	return &Chain{
		ID:      "chain-" + tmrand.Str(6),
		dataDir: tmpDir,
	}, nil
}

func (c *Chain) cleanup() {
	os.RemoveAll(c.dataDir)
}

func (c *Chain) configDir() string {
	return fmt.Sprintf("%s/%s", c.dataDir, c.ID)
}

func (c *Chain) createAndInitValidators(count int, mnemonic string) error {
	for i := 0; i < count; i++ {
		val := c.createValidator(i)

		// generate genesis files
		if err := val.init(); err != nil {
			return err
		}

		c.Validators = append(c.Validators, val)

		// create keys
		if err := val.createKeyFromMnemonic("val", mnemonic); err != nil {
			return err
		}
		if err := val.createNodeKey(); err != nil {
			return err
		}
		if err := val.createConsensusKey(); err != nil {
			return err
		}
	}

	return nil
}

func (c *Chain) createValidator(index int) *Validator {
	return &Validator{
		chain:   c,
		index:   index,
		moniker: fmt.Sprintf("%s-val-%d", c.ID, index),
	}
}
