package ethclient

import (
	"context"

	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/common/hexutil"
	"github.com/ethereum/go-ethereum/rpc"
)

// StoragesAt returns the values of keys in the contract storage of the given account.
// The block hash specifies the block at which the storage values are retrieved.
func (ec *Client) StoragesAt(ctx context.Context, account common.Address, keys []common.Hash, blockHash common.Hash) ([]byte, error) {
	results := make([]hexutil.Bytes, len(keys))
	reqs := make([]rpc.BatchElem, len(keys))

	for i := range reqs {
		reqs[i] = rpc.BatchElem{
			Method: "eth_getStorageAt",
			Args:   []interface{}{account, keys[i], rpc.BlockNumberOrHashWithHash(blockHash, false)},
			Result: &results[i],
		}
	}
	if err := ec.c.BatchCallContext(ctx, reqs); err != nil {
		return nil, err
	}

	output := make([]byte, common.HashLength*len(keys))
	for i := range reqs {
		if reqs[i].Error != nil {
			return nil, reqs[i].Error
		}
		copy(output[i*common.HashLength:], results[i])
	}

	return output, nil
}
