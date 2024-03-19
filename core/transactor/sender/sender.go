package sender

import (
	"context"
	"sync"
	"time"

	"github.com/berachain/offchain-sdk/client/eth"
	"github.com/berachain/offchain-sdk/core/transactor/types"
	"github.com/berachain/offchain-sdk/log"

	coretypes "github.com/ethereum/go-ethereum/core/types"
)

// Sender struct holds the transaction replacement and retry policies.
type Sender struct {
	factory             Factory             // factory to sign new transactions
	txReplacementPolicy TxReplacementPolicy // policy to replace transactions
	retryPolicy         RetryPolicy         // policy to retry transactions

	sendingTxs sync.Map // msgs that are currently sending (or retrying)

	chain  eth.Client
	logger log.Logger
}

// New creates a new Sender with default replacement and exponential retry policies.
func New(factory Factory) *Sender {
	return &Sender{
		factory:             factory,
		txReplacementPolicy: &DefaultTxReplacementPolicy{nf: factory},
		retryPolicy:         &ExpoRetryPolicy{}, // TODO: choose from config.
	}
}

func (s *Sender) Setup(chain eth.Client, logger log.Logger) {
	s.chain = chain
	s.logger = logger
}

// If a msgID IsSending (true is returned), the preconfirmed state is "StateSending".
func (s *Sender) IsSending(msgID string) bool {
	_, ok := s.sendingTxs.Load(msgID)
	return ok
}

// SendTransaction sends a transaction using the Ethereum client. If the transaction fails to send,
// it retries based on the confi retry policy.
func (s *Sender) SendTransaction(
	ctx context.Context, tx *coretypes.Transaction, msgIDs []string,
) error {
	for _, msgID := range msgIDs {
		s.sendingTxs.Store(msgID, struct{}{})
	}

	// Try sending the transaction (with retry if applicable).
	err := s.retryTxWithPolicy(ctx, tx)

	for _, msgID := range msgIDs {
		s.sendingTxs.Delete(msgID)
	}

	return err
}

// retryTxWithPolicy (re)tries sending tx according to the retry policy. Specifically handles two
// common errors on sending a transaction (NonceTooLow, ReplaceUnderpriced) by replacing the tx
// appropriately.
func (s *Sender) retryTxWithPolicy(ctx context.Context, tx *coretypes.Transaction) error {
	for {
		// (Re)try sending the transaction.
		err := s.chain.SendTransaction(ctx, tx)

		// Check the policy to see if we should retry this transaction.
		retry, backoff := s.retryPolicy.Get(tx, err)
		if !retry {
			return err
		}
		time.Sleep(backoff) // Retry after recommended backoff.

		// Log relevant details about retrying the transaction.
		currTx, currGasPrice, currNonce := tx.Hash(), tx.GasPrice(), tx.Nonce()
		s.logger.Error("failed to send tx, retrying...", "hash", currTx, "err", err)

		// Get the replacement tx if necessary.
		tx = s.txReplacementPolicy.GetNew(tx, err)

		// Update the retry policy if the transaction has been changed and log.
		if newTx := tx.Hash(); newTx != currTx {
			s.logger.Debug(
				"retrying with diff gas and/or nonce",
				"old-gas", currGasPrice, "new-gas", tx.GasPrice(),
				"old-nonce", currNonce, "new-nonce", tx.Nonce(),
			)
			s.retryPolicy.UpdateTxModified(currTx, newTx)
		}

		// Use the factory to build and sign the new transaction.
		if tx, err = s.factory.BuildTransactionFromRequests(
			ctx, tx.Nonce(), types.NewTxRequestFromTx(tx),
		); err != nil {
			s.logger.Error("failed to sign replacement transaction", "err", err)
		}
	}
}
