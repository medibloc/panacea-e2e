#!/bin/bash

set -euxo pipefail

/usr/bin/doracled init

rm -f /home_mnt/.doracle/config.toml
tee /home_mnt/.doracle/config.toml <<EOF
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

ego run doracled gen-oracle-key
