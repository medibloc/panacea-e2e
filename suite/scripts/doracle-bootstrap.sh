#!/bin/bash

set -euxo pipefail

ego run /usr/bin/doracled init

CONFIG_PATH=/doracle/.doracle/config.toml
rm -f $CONFIG_PATH && touch $CONFIG_PATH
tee $CONFIG_PATH <<EOF
log-level = "info"
oracle-mnemonic = "$ORACLE_MNEMONIC"
oracle-acc-num = "$ORACLE_ACC_NUM"
oracle-acc-index = "$ORACLE_ACC_INDEX"
listen_addr = "0.0.0.0:8080"
data_dir = "data"
oracle_priv_key_file = "oracle_priv_key.sealed"
oracle_pub_key_file = "oracle_pub_key.json"
node_priv_key_file = "node_priv_key.sealed"

[panacea]
chain-id = "$CHAIN_ID"
grpc-addr = "http://$PANACEA_VAL_HOST:9090"
rpc-addr = "tcp://$PANACEA_VAL_HOST:26657"
default-gas-limit = "400000"
default-fee-amount = "2000000umed"
light-client-primary-addr = "tcp://$PANACEA_VAL_HOST:26657"
light-client-witness-addrs= "tcp://$PANACEA_VAL_HOST:26657"
EOF

ego run /usr/bin/doracled gen-oracle-key \
    --trusted-block-hash $TRUSTED_BLOCK_HASH \
    --trusted-block-height $TRUSTED_BLOCK_HEIGHT

ORACLE_PUBKEY=$(cat /doracle/.doracle/oracle_pub_key.json | jq -r '.public_key_base64')
UNIQUE_ID=$(ego uniqueid /usr/bin/doracled)

PROPOSAL_PATH=/doracle/oracle-proposal.json
rm -f $PROPOSAL_PATH && touch $PROPOSAL_PATH
tee $PROPOSAL_PATH <<EOF
{
  "title": "oracle module param change",
  "description": "UniqueID and OraclePublicKey",
  "changes": [
    {
      "subspace": "oracle",
      "key": "UniqueID",
      "value": "$UNIQUE_ID"
    },
    {
      "subspace": "oracle",
      "key": "OraclePublicKey",
      "value": "$ORACLE_PUBKEY"
    }
  ],
  "deposit": "100000000000umed"
}
EOF

chmod a=rwx $PROPOSAL_PATH