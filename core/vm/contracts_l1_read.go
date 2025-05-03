package vm

import (
	"errors"
	"math/big"

	"github.com/ethereum/go-ethereum"
	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/core/types"
	"github.com/ethereum/go-ethereum/ethclient"
	"github.com/ethereum/go-ethereum/log"
	"github.com/ethereum/go-ethereum/params"
	"github.com/ethereum/go-ethereum/rpc"
)

type remoteStaticCall struct{}

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

func (c *remoteStaticCall) Run(ctx PrecompileContext, input []byte) ([]byte, error) {
	log.Info("running remote static call", "input", input)
	rpcUrl := ctx.GetL1ArchiveRpc()
	if rpcUrl == nil {
		log.Error("no L1 archive node RPC configured")
		return nil, errors.New("no L1 archive node RPC configured")
	}
	rpcClient, err := rpc.Dial(*rpcUrl)
	if err != nil {
		log.Error("failed to dial L1 archive node RPC", "error", err, "rpcUrl", *rpcUrl)
		return nil, err
	}
	ethClient := ethclient.NewClient(rpcClient)
	defer ethClient.Close()

	to, data, err := parseRemoteStaticCallInput(input)
	if err != nil {
		log.Error("failed to parse remote static call input", "error", err, "input", input)
		return nil, err
	}

	l1BlockHash := ctx.GetState(types.L1BlockAddr, types.L1BlockHashSlot)
	callArgs := ethereum.CallMsg{To: &to, Data: data}
	result, err := ethClient.CallContractAtHash(ctx, callArgs, l1BlockHash)
	if err != nil {
		log.Error("failed to call contract at hash", "error", err, "l1BlockHash", l1BlockHash, "to", to, "data", data)
		return nil, err
	}

	return result, nil
}

type l1SLoad struct{}

func (c *l1SLoad) RequiredGas(input []byte) uint64 {
	storageSlotsToLoad := (len(input) - common.HashLength) / common.HashLength
	storageSlotsToLoad = min(storageSlotsToLoad, params.L1SLoadMaxNumStorageSlots)

	return params.L1SLoadBaseGas + uint64(storageSlotsToLoad)*params.L1SLoadPerLoadGas
}

func (c *l1SLoad) Run(ctx PrecompileContext, input []byte) ([]byte, error) {
	rpcUrl := ctx.GetL1ArchiveRpc()
	if rpcUrl == nil {
		log.Error("no L1 archive node RPC configured")
		return nil, errors.New("no L1 archive node RPC configured")
	}
	rpcClient, err := rpc.Dial(*rpcUrl)
	if err != nil {
		log.Error("failed to dial L1 archive node RPC", "error", err, "rpcUrl", *rpcUrl)
		return nil, err
	}
	ethClient := ethclient.NewClient(rpcClient)
	defer ethClient.Close()

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

	bres, err := ethClient.BlockNumber(ctx)
	if err != nil {
		log.Error("failed to get latest L1 block number", "error", err)
		return nil, err
	}

	latestL1BlockNumber := new(big.Int).SetUint64(bres)
	res, err := ethClient.StoragesAt(ctx, contractAddress, contractStorageKeys, latestL1BlockNumber)
	if err != nil {
		log.Error("failed to call L1 archive node RPC", "error", err, "rpcUrl", *rpcUrl)
		return nil, err
	}

	return res, nil
}
