package suite

import "github.com/cosmos/go-bip39"

func newMnemonic() (string, error) {
	entropySeed, err := bip39.NewEntropy(256)
	if err != nil {
		return "", err
	}

	return bip39.NewMnemonic(entropySeed)
}
