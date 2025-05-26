package vm

import (
	"context"
	"errors"

	"github.com/ethereum/go-ethereum"
	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/core/types"
	"github.com/ethereum/go-ethereum/ethclient"
	"github.com/ethereum/go-ethereum/log"
	"github.com/ethereum/go-ethereum/params"
)

type l1Syncer struct {
	l1RPCClient *ethclient.Client
	stateDb     StateDB
}

// GetLatestL1BlockHash returns the latest L1 block hash from [L1Block] system contract.
// The hash stored in the L1Block system contract represents the latest L1 block height
// that has been confirmed by L2, ensuring consistency between L1 and L2 state.
//
// [L1Block]: https://github.com/ethereum-optimism/optimism/blob/636ad830f50b/packages/contracts-bedrock/src/L2/L1Block.sol
func (l *l1Syncer) GetLatestL1BlockHash() common.Hash {
	return l.stateDb.GetState(types.L1BlockAddr, types.L1BlockHashSlot)
}

type remoteStaticCall struct {
	l1Syncer *l1Syncer
}

func parseRemoteStaticCallInput(input []byte) (common.Address, []byte, error) {
	// input = abi.encode(address to, bytes memory data)
	// bytes memory data = bytes32(pointer) | uint256(numBytes) | bytes padded to length of multiple of 32

	to := common.BytesToAddress(input[:32])
	offset := 32
	// _ = input[offset : offset+32] // pointer, we don't need this for our purposes
	offset += 32
	numBytes := input[offset : offset+32]
	numBytesAsNum := common.BytesToHash(numBytes).Big()
	if !numBytesAsNum.IsInt64() {
		return common.Address{}, nil, errors.New("bytes array not encoded properly")
	}
	offset += 32
	data := input[offset:][:numBytesAsNum.Int64()]
	return to, data, nil
}

func (c *remoteStaticCall) RequiredGas(input []byte) uint64 {
	_, data, err := parseRemoteStaticCallInput(input)
	if err != nil {
		return 0
	}

	return params.RemoteStaticCallGas * uint64(len(data))
}

func (c *remoteStaticCall) Run(input []byte) ([]byte, error) {
	log.Info("running remote static call", "input", input)
	to, data, err := parseRemoteStaticCallInput(input)
	if err != nil {
		log.Error("failed to parse remote static call input", "error", err, "input", input)
		return nil, err
	}

	l1BlockHash := c.l1Syncer.GetLatestL1BlockHash()
	callArgs := ethereum.CallMsg{To: &to, Data: data}
	result, err := c.l1Syncer.l1RPCClient.CallContractAtHash(context.Background(), callArgs, l1BlockHash)
	if err != nil {
		log.Error("failed to call contract at hash", "error", err, "l1BlockHash", l1BlockHash, "to", to, "data", data)
		return nil, err
	}

	return result, nil
}

type l1SLoad struct {
	l1Syncer *l1Syncer
}

func (c *l1SLoad) RequiredGas(input []byte) uint64 {
	storageSlotsToLoad := (len(input) - common.HashLength) / common.HashLength
	storageSlotsToLoad = min(storageSlotsToLoad, params.L1SLoadMaxNumStorageSlots)

	return params.L1SLoadBaseGas + uint64(storageSlotsToLoad)*params.L1SLoadPerLoadGas
}

func (c *l1SLoad) Run(input []byte) ([]byte, error) {
	ethClient := c.l1Syncer.l1RPCClient

	// (padding + address)(32 bytes) + 32 bytes * numStorageKeys
	if len(input) < 2*common.HashLength {
		log.Error("L1SLOAD input too short", "input", input)
		return nil, errors.New("L1SLOAD input too short")
	}

	// countOfStorageKeys := (len(input) - common.AddressLength) / common.HashLength
	// isAtLeastOneSKeyToRead := countOfStorageKeys > 0
	// allStorageKeys32Bytes := countOfStorageKeys*common.HashLength == len(input)-common.AddressLength
	countOfStorageKeys := len(input)/common.HashLength - 1
	isAtLeastOneSKeyToRead := countOfStorageKeys > 0
	allStorageKeys32Bytes := countOfStorageKeys*common.HashLength == len(input)-common.HashLength

	if !isAtLeastOneSKeyToRead || !allStorageKeys32Bytes {
		log.Error("L1SLOAD input invalid", "input", input, "countOfStorageKeys", countOfStorageKeys, "isAtLeastOneSKeyToRead", isAtLeastOneSKeyToRead, "allStorageKeys32Bytes", allStorageKeys32Bytes)
		return nil, errors.New("L1SLOAD input invalid")
	}

	// to, data, err := parseRemoteStaticCallInput(input)
	contractAddress := common.BytesToAddress(input[:common.HashLength])
	data := input[common.HashLength:]
	contractStorageKeys := make([]common.Hash, countOfStorageKeys)
	for i := 0; i < countOfStorageKeys; i++ {
		contractStorageKeys[i] = common.BytesToHash(data[i*common.HashLength : (i+1)*common.HashLength])
	}

	latestL1BlockHash := c.l1Syncer.GetLatestL1BlockHash()
	res, err := ethClient.StoragesAt(context.Background(), contractAddress, contractStorageKeys, latestL1BlockHash)
	if err != nil {
		log.Error("failed to call L1 archive node RPC", "error", err, "latestL1BlockHash", latestL1BlockHash)
		return nil, err
	}

	return res, nil
}
