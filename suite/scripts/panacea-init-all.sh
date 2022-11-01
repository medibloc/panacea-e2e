#!/bin/bash

set -uexo pipefail

BIN=/usr/bin/panacead
CHAIN_DIR=/root/chain

PERSISTENT_PEERS=""

for (( i=0; i < $NUM_VALIDATORS; i++ )); do
    MONIKER="$CHAIN_ID-val-$i"

    $BIN init $MONIKER --chain-id $CHAIN_ID --home $CHAIN_DIR/$MONIKER
    echo -e "${MNEMONIC}\n\n" | $BIN keys add val -i --account 0 --index $i --home $CHAIN_DIR/$MONIKER

    CONFIG_PATH=$CHAIN_DIR/$MONIKER/config/config.toml
    sed -i 's|^laddr = "tcp://.*:26656"$|laddr = "tcp://0.0.0.0:26656"|g' $CONFIG_PATH
    sed -i 's|^addr_book_strict = .*$|addr_book_strict = false|g' $CONFIG_PATH
    sed -i 's|^laddr = "tcp://.*:26657"$|laddr = "tcp://0.0.0.0:26657"|g' $CONFIG_PATH

    APP_CONF_PATH=$CHAIN_DIR/$MONIKER/config/app.toml
    sed -i 's|^minimum-gas-prices = .*$|minimum-gas-prices = "5umed"|g' $APP_CONF_PATH
    sed -i 's|^enable = false$|enable = true|g' $APP_CONF_PATH

    GENESIS_PATH=$CHAIN_DIR/$MONIKER/config/genesis.json
    sed -i 's|"voting_period": ".*"|"voting_period": "30s"|g' $GENESIS_PATH

    NODE_ID=$($BIN tendermint show-node-id --home $CHAIN_DIR/$MONIKER)
    if [ $i -eq 0 ]; then
        PERSISTENT_PEERS="$NODE_ID@$MONIKER:26656"
    else
        PERSISTENT_PEERS="$PERSISTENT_PEERS,$NODE_ID@$MONIKER:26656"
    fi
done

for (( i=0; i < $NUM_VALIDATORS; i++ )); do
    MONIKER="$CHAIN_ID-val-$i"

    CONFIG_PATH=$CHAIN_DIR/$MONIKER/config/config.toml
    sed -i 's|^persistent_peers = .*$|persistent_peers = "'"$PERSISTENT_PEERS"'"|g' $CONFIG_PATH
done

FIRST_MONIKER="$CHAIN_ID-val-0"

for (( i=0; i < $NUM_VALIDATORS; i++ )); do
    MONIKER="$CHAIN_ID-val-$i"

    $BIN add-genesis-account $(panacead keys show val -a --home $CHAIN_DIR/$MONIKER) $GEN_ACC_BALANCE --home $CHAIN_DIR/$FIRST_MONIKER
done

# add genesis oracle
$BIN add-genesis-oracle --oracle-account $(panacead keys show val -a --home $CHAIN_DIR/"$CHAIN_ID-val-0") --home $CHAIN_DIR/$FIRST_MONIKER

for (( i=0; i < $NUM_VALIDATORS; i++ )); do
    MONIKER="$CHAIN_ID-val-$i"

    if [ $i -ne 0 ]; then
        cp $CHAIN_DIR/$FIRST_MONIKER/config/genesis.json $CHAIN_DIR/$MONIKER/config/genesis.json
    fi

    mkdir -p $CHAIN_DIR/$MONIKER/config/gentx
    OUTPUT_PATH=$CHAIN_DIR/$MONIKER/config/gentx/gentx-$MONIKER.json
    $BIN gentx val \
        $STAKE \
        --commission-rate 0.1 \
        --commission-max-rate 0.2 \
        --commission-max-change-rate 0.01 \
        --min-self-delegation 1 \
        --chain-id $CHAIN_ID \
        --output-document $OUTPUT_PATH \
        --home $CHAIN_DIR/$MONIKER

    if [ $i -ne 0 ]; then
        cp $OUTPUT_PATH $CHAIN_DIR/$FIRST_MONIKER/config/gentx/
    fi
done

$BIN collect-gentxs --home $CHAIN_DIR/$FIRST_MONIKER

for (( i=1; i < $NUM_VALIDATORS; i++ )); do
    MONIKER="$CHAIN_ID-val-$i"

    cp $CHAIN_DIR/$FIRST_MONIKER/config/genesis.json $CHAIN_DIR/$MONIKER/config/genesis.json
done

