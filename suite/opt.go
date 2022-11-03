package suite

type TestSuiteOptions struct {
	// genesis validator
	GenValBalance  string
	NumValidators  int
	ValidatorStake string

	// genesis account
	GenAccBalance string
	NumAccounts   int
}
