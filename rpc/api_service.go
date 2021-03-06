// Copyright (C) 2017 go-nebulas authors
//
// This file is part of the go-nebulas library.
//
// the go-nebulas library is free software: you can redistribute it and/or modify
// it under the terms of the GNU General Public License as published by
// the Free Software Foundation, either version 3 of the License, or
// (at your option) any later version.
//
// the go-nebulas library is distributed in the hope that it will be useful,
// but WITHOUT ANY WARRANTY; without even the implied warranty of
// MERCHANTABILITY or FITNESS FOR A PARTICULAR PURPOSE.  See the
// GNU General Public License for more details.
//
// You should have received a copy of the GNU General Public License
// along with the go-nebulas library.  If not, see <http://www.gnu.org/licenses/>.
//

package rpc

import (
	"errors"
	"fmt"

	"encoding/json"

	"github.com/gogo/protobuf/proto"
	"github.com/nebulasio/go-nebulas/core"
	"github.com/nebulasio/go-nebulas/core/pb"
	"github.com/nebulasio/go-nebulas/crypto/hash"
	"github.com/nebulasio/go-nebulas/net/p2p"
	"github.com/nebulasio/go-nebulas/rpc/pb"
	"github.com/nebulasio/go-nebulas/util"
	"github.com/nebulasio/go-nebulas/util/byteutils"
	"github.com/nebulasio/go-nebulas/util/logging"
	"github.com/sirupsen/logrus"
	"golang.org/x/net/context"

	nnet "github.com/nebulasio/go-nebulas/net"
)

// APIService implements the RPC API service interface.
type APIService struct {
	server GRPCServer
}

// GetNebState is the RPC API handler.
func (s *APIService) GetNebState(ctx context.Context, req *rpcpb.NonParamsRequest) (*rpcpb.GetNebStateResponse, error) {
	logging.VLog().WithFields(logrus.Fields{
		"api": "/v1/user/nebstate",
	}).Info("Rpc request.")
	metricsRPCCounter.Mark(1)

	neb := s.server.Neblet()

	tail := neb.BlockChain().TailBlock()

	resp := &rpcpb.GetNebStateResponse{}
	resp.ChainId = neb.BlockChain().ChainID()
	resp.Tail = tail.Hash().String()
	resp.Height = tail.Height()
	resp.Coinbase = tail.Coinbase().String()
	resp.Synchronized = neb.NetManager().Node().IsSynchronizing()
	resp.PeerCount = uint32(neb.NetManager().Node().PeersCount())
	resp.ProtocolVersion = p2p.NebProtocolID
	resp.Version = neb.Config().App.Version

	return resp, nil
}

// NodeInfo is the PRC API handler
func (s *APIService) NodeInfo(ctx context.Context, req *rpcpb.NonParamsRequest) (*rpcpb.NodeInfoResponse, error) {
	logging.VLog().WithFields(logrus.Fields{
		"api": "/v1/user/nodeinfo",
	}).Info("Rpc request.")
	metricsRPCCounter.Mark(1)

	neb := s.server.Neblet()
	resp := &rpcpb.NodeInfoResponse{}
	node := neb.NetManager().Node()
	resp.Id = node.ID()
	resp.ChainId = node.Config().ChainID
	resp.BucketSize = int32(node.Config().Bucketsize)
	resp.Version = uint32(node.Config().Version)
	resp.StreamStoreSize = int32(node.Config().StreamStoreSize)
	resp.StreamStoreExtendSize = int32(node.Config().StreamStoreExtendSize)
	resp.RelayCacheSize = int32(node.Config().RelayCacheSize)
	resp.PeerCount = uint32(node.PeersCount())
	resp.ProtocolVersion = p2p.NebProtocolID

	for k, v := range node.RouteTable().Peers() {
		routeTable := &rpcpb.RouteTable{}
		routeTable.Id = k.Pretty()
		routeTable.Address = make([]string, len(v))

		for i, addr := range v {
			routeTable.Address[i] = addr.String()
		}
		resp.RouteTable = append(resp.RouteTable, routeTable)
	}

	return resp, nil
}

// Accounts is the RPC API handler.
func (s *APIService) Accounts(ctx context.Context, req *rpcpb.NonParamsRequest) (*rpcpb.AccountsResponse, error) {
	logging.VLog().WithFields(logrus.Fields{
		"api": "/v1/user/accounts",
	}).Info("Rpc request.")
	metricsRPCCounter.Mark(1)

	neb := s.server.Neblet()

	accs := neb.AccountManager().Accounts()

	resp := new(rpcpb.AccountsResponse)
	addrs := make([]string, len(accs))
	for index, addr := range accs {
		addrs[index] = addr.String()
	}
	resp.Addresses = addrs
	return resp, nil
}

// GetAccountState is the RPC API handler.
func (s *APIService) GetAccountState(ctx context.Context, req *rpcpb.GetAccountStateRequest) (*rpcpb.GetAccountStateResponse, error) {
	logging.VLog().WithFields(logrus.Fields{
		"address": req.Address,
		"height":  req.Height,
		"api":     "/v1/user/accountstate",
	}).Info("Rpc request.")
	metricsRPCCounter.Mark(1)

	neb := s.server.Neblet()

	addr, err := core.AddressParse(req.Address)
	if err != nil {
		metricsAccountStateFailed.Mark(1)
		return nil, err
	}

	block := neb.BlockChain().TailBlock()
	if req.Height > 0 {
		block = neb.BlockChain().GetBlockOnCanonicalChainByHeight(req.Height)
		if block == nil {
			metricsAccountStateFailed.Mark(1)
			return nil, errors.New("block not found")
		}
	}

	balance := block.GetBalance(addr.Bytes())
	nonce := block.GetNonce(addr.Bytes())

	metricsAccountStateSuccess.Mark(1)
	return &rpcpb.GetAccountStateResponse{Balance: balance.String(), Nonce: fmt.Sprintf("%d", nonce)}, nil
}

// SendTransaction is the RPC API handler.
func (s *APIService) SendTransaction(ctx context.Context, req *rpcpb.TransactionRequest) (*rpcpb.SendTransactionResponse, error) {
	logging.VLog().WithFields(logrus.Fields{
		"api": "/v1/user/transaction",
	}).Info("Rpc request.")
	metricsRPCCounter.Mark(1)

	return s.sendTransaction(req)
}

// Call is the RPC API handler.
func (s *APIService) Call(ctx context.Context, req *rpcpb.TransactionRequest) (*rpcpb.CallResponse, error) {
	logging.VLog().WithFields(logrus.Fields{
		"api": "/v1/user/call",
	}).Info("Rpc request.")
	metricsRPCCounter.Mark(1)

	neb := s.server.Neblet()
	tx, err := parseTransaction(neb, req)
	if err != nil {
		return nil, err
	}
	result, err := neb.BlockChain().Call(tx)
	if err != nil {
		return nil, err
	}
	return &rpcpb.CallResponse{Result: result}, nil
}

func (s *APIService) sendTransaction(req *rpcpb.TransactionRequest) (*rpcpb.SendTransactionResponse, error) {
	neb := s.server.Neblet()
	tail := neb.BlockChain().TailBlock()
	addr, err := core.AddressParse(req.From)
	if err != nil {
		metricsSendTxFailed.Mark(1)
		return nil, err
	}
	if req.Nonce <= tail.GetNonce(addr.Bytes()) {
		metricsSendTxFailed.Mark(1)
		return nil, errors.New("nonce is invalid")
	}

	tx, err := parseTransaction(neb, req)
	if err != nil {
		metricsSendTxFailed.Mark(1)
		return nil, err
	}
	if err := neb.AccountManager().SignTransaction(tx.From(), tx); err != nil {
		metricsSendTxFailed.Mark(1)
		return nil, err
	}
	if err := neb.BlockChain().TransactionPool().PushAndBroadcast(tx); err != nil {
		metricsSendTxFailed.Mark(1)
		return nil, err
	}
	if tx.Type() == core.TxPayloadDeployType {
		address, _ := core.NewContractAddressFromHash(hash.Sha3256(tx.From().Bytes(), byteutils.FromUint64(tx.Nonce())))
		metricsSendTxSuccess.Mark(1)
		return &rpcpb.SendTransactionResponse{Txhash: tx.Hash().String(), ContractAddress: address.String()}, nil
	}

	metricsSendTxSuccess.Mark(1)
	return &rpcpb.SendTransactionResponse{Txhash: tx.Hash().String()}, nil
}

func parseTransaction(neb Neblet, reqTx *rpcpb.TransactionRequest) (*core.Transaction, error) {
	fromAddr, err := core.AddressParse(reqTx.From)
	if err != nil {
		return nil, err
	}
	toAddr, err := core.AddressParse(reqTx.To)
	if err != nil {
		return nil, err
	}

	value := util.NewUint128FromString(reqTx.Value)
	gasPrice := util.NewUint128FromString(reqTx.GasPrice)
	gasLimit := util.NewUint128FromString(reqTx.GasLimit)

	var (
		payloadType string
		payload     []byte
	)
	if reqTx.Contract != nil && len(reqTx.Contract.Source) > 0 {
		payloadType = core.TxPayloadDeployType
		payload, err = core.NewDeployPayload(reqTx.Contract.Source, reqTx.Contract.SourceType, reqTx.Contract.Args).ToBytes()
	} else if reqTx.Contract != nil && len(reqTx.Contract.Function) > 0 {
		payloadType = core.TxPayloadCallType
		payload, err = core.NewCallPayload(reqTx.Contract.Function, reqTx.Contract.Args).ToBytes()
	} else if reqTx.Candidate != nil {
		payloadType = core.TxPayloadCandidateType
		payload, err = core.NewCandidatePayload(reqTx.Candidate.Action).ToBytes()
	} else if reqTx.Delegate != nil {
		payloadType = core.TxPayloadDelegateType
		payload, err = core.NewDelegatePayload(reqTx.Delegate.Action, reqTx.Delegate.Delegatee).ToBytes()
	} else {
		payloadType = core.TxPayloadBinaryType
	}
	if err != nil {
		return nil, err
	}

	tx := core.NewTransaction(neb.BlockChain().ChainID(), fromAddr, toAddr, value, reqTx.Nonce, payloadType, payload, gasPrice, gasLimit)
	return tx, nil
}

// SendRawTransaction submit the signed transaction raw data to txpool
func (s *APIService) SendRawTransaction(ctx context.Context, req *rpcpb.SendRawTransactionRequest) (*rpcpb.SendTransactionResponse, error) {
	logging.VLog().WithFields(logrus.Fields{
		"api": "/v1/user/rawtransaction",
	}).Info("Rpc request.")
	metricsRPCCounter.Mark(1)

	// Validate and sign the tx, then submit it to the tx pool.
	neb := s.server.Neblet()

	pbTx := new(corepb.Transaction)
	if err := proto.Unmarshal(req.GetData(), pbTx); err != nil {
		metricsSendRawTxFailed.Mark(1)
		return nil, err
	}
	tx := new(core.Transaction)
	if err := tx.FromProto(pbTx); err != nil {
		metricsSendRawTxFailed.Mark(1)
		return nil, err
	}

	if err := neb.BlockChain().TransactionPool().PushAndBroadcast(tx); err != nil {
		metricsSendRawTxFailed.Mark(1)
		return nil, err
	}

	if tx.Type() == core.TxPayloadDeployType {
		metricsSendRawTxSuccess.Mark(1)
		address, _ := core.NewContractAddressFromHash(hash.Sha3256(tx.From().Bytes(), byteutils.FromUint64(tx.Nonce())))
		return &rpcpb.SendTransactionResponse{Txhash: tx.Hash().String(), ContractAddress: address.String()}, nil
	}

	metricsSendRawTxSuccess.Mark(1)
	return &rpcpb.SendTransactionResponse{Txhash: tx.Hash().String()}, nil
}

// GetBlockByHash get block info by the block hash
func (s *APIService) GetBlockByHash(ctx context.Context, req *rpcpb.GetBlockByHashRequest) (*rpcpb.BlockResponse, error) {
	logging.VLog().WithFields(logrus.Fields{
		"hash": req.Hash,
		"api":  "/v1/user/getBlockByHash",
	}).Info("Rpc request.")
	metricsRPCCounter.Mark(1)

	neb := s.server.Neblet()

	bhash, err := byteutils.FromHex(req.GetHash())
	if err != nil {
		return nil, err
	}

	block := neb.BlockChain().GetBlock(bhash)

	return s.toBlockResponse(block, req.FullTransaction)
}

// GetBlockByHeight get block info by the block hash
func (s *APIService) GetBlockByHeight(ctx context.Context, req *rpcpb.GetBlockByHeightRequest) (*rpcpb.BlockResponse, error) {
	logging.VLog().WithFields(logrus.Fields{
		"height": req.Height,
		"api":    "/v1/user/getBlockByHash",
	}).Info("Rpc request.")
	metricsRPCCounter.Mark(1)

	neb := s.server.Neblet()

	block := neb.BlockChain().GetBlockOnCanonicalChainByHeight(req.Height)

	return s.toBlockResponse(block, req.FullTransaction)
}

func (s *APIService) toBlockResponse(block *core.Block, fullTransaction bool) (*rpcpb.BlockResponse, error) {
	if block == nil {
		return nil, errors.New("block not found")
	}

	resp := &rpcpb.BlockResponse{
		Hash:       block.Hash().String(),
		ParentHash: block.ParentHash().String(),
		Height:     block.Height(),
		Nonce:      block.Nonce(),
		Coinbase:   block.Coinbase().String(),
		Miner:      block.Miner().String(),
		Timestamp:  block.Timestamp(),
		ChainId:    block.ChainID(),
		StateRoot:  block.StateRoot().String(),
		TxsRoot:    block.TxsRoot().String(),
		EventsRoot: block.EventsRoot().String(),
	}

	// dpos context
	dposContextResp := &rpcpb.DposContext{
		DynastyRoot:     byteutils.Hex(block.DposContext().DynastyRoot),
		NextDynastyRoot: byteutils.Hex(block.DposContext().NextDynastyRoot),
		DelegateRoot:    byteutils.Hex(block.DposContext().DelegateRoot),
		CandidateRoot:   byteutils.Hex(block.DposContext().CandidateRoot),
		VoteRoot:        byteutils.Hex(block.DposContext().VoteRoot),
		MintCntRoot:     byteutils.Hex(block.DposContext().MintCntRoot),
	}
	resp.DposContext = dposContextResp

	// add block transactions
	txs := []*rpcpb.TransactionResponse{}
	for _, v := range block.Transactions() {
		var tx *rpcpb.TransactionResponse
		if fullTransaction {
			tx, _ = s.toTransactionResponse(v)
		} else {
			tx = &rpcpb.TransactionResponse{Hash: v.Hash().String()}
		}
		txs = append(txs, tx)
	}

	return resp, nil
}

// BlockDump is the RPC API handler.
func (s *APIService) BlockDump(ctx context.Context, req *rpcpb.BlockDumpRequest) (*rpcpb.BlockDumpResponse, error) {
	logging.VLog().WithFields(logrus.Fields{
		"count": req.Count,
		"api":   "/v1/user/blockdump",
	}).Info("Rpc request.")
	metricsRPCCounter.Mark(1)

	neb := s.server.Neblet()
	data := neb.BlockChain().Dump(int(req.Count))
	return &rpcpb.BlockDumpResponse{Data: data}, nil
}

// LatestIrreversibleBlock is the RPC API handler.
func (s *APIService) LatestIrreversibleBlock(ctx context.Context, req *rpcpb.NonParamsRequest) (*rpcpb.BlockResponse, error) {
	logging.VLog().WithFields(logrus.Fields{
		"api": "/v1/user/lib",
	}).Info("Rpc request.")
	metricsRPCCounter.Mark(1)

	neb := s.server.Neblet()
	block := neb.BlockChain().LatestIrreversibleBlock()

	return s.toBlockResponse(block, false)
}

// GetTransactionReceipt get transaction info by the transaction hash
func (s *APIService) GetTransactionReceipt(ctx context.Context, req *rpcpb.GetTransactionByHashRequest) (*rpcpb.TransactionResponse, error) {
	logging.VLog().WithFields(logrus.Fields{
		"hash": req.Hash,
		"api":  "/v1/user/getTransactionReceipt",
	}).Info("Rpc request.")
	metricsRPCCounter.Mark(1)

	neb := s.server.Neblet()
	bhash, _ := byteutils.FromHex(req.GetHash())
	tx := neb.BlockChain().GetTransaction(bhash)
	if tx == nil {
		return nil, errors.New("transaction not found")
	}

	return s.toTransactionResponse(tx)
}

func (s *APIService) toTransactionResponse(tx *core.Transaction) (*rpcpb.TransactionResponse, error) {
	var status uint32
	neb := s.server.Neblet()
	events, _ := neb.BlockChain().TailBlock().FetchEvents(tx.Hash())

	if events == nil {
		status = 2
	} else {
		for _, v := range events {
			// TODO: transaction execution topic need change later.
			if v.Topic == core.TopicExecuteTxSuccess {
				status = 1
				break
			} else if v.Topic == core.TopicExecuteTxFailed {
				status = 0
				break
			}
		}
	}

	resp := &rpcpb.TransactionResponse{
		ChainId:   tx.ChainID(),
		Hash:      tx.Hash().String(),
		From:      tx.From().String(),
		To:        tx.To().String(),
		Value:     tx.Value().String(),
		Nonce:     tx.Nonce(),
		Timestamp: tx.Timestamp(),
		Type:      tx.Type(),
		Data:      tx.Data(),
		GasPrice:  tx.GasPrice().String(),
		GasLimit:  tx.GasLimit().String(),
		Status:    status,
	}

	if tx.Type() == core.TxPayloadDeployType {
		contractAddr, err := tx.GenerateContractAddress()
		if err != nil {
			return nil, err
		}
		resp.ContractAddress = contractAddr.String()
	}
	return resp, nil
}

// Subscribe ..
func (s *APIService) Subscribe(req *rpcpb.SubscribeRequest, gs rpcpb.ApiService_SubscribeServer) error {
	logging.VLog().WithFields(logrus.Fields{
		"topic": req.Topic,
		"api":   "/v1/user/subscribe",
	}).Info("Rpc request.")
	metricsRPCCounter.Mark(1)

	neb := s.server.Neblet()

	chainEventCh := make(chan *core.Event, 128)
	emitter := neb.EventEmitter()
	for _, v := range req.Topic {
		emitter.Register(v, chainEventCh)
	}

	defer (func() {
		for _, v := range req.Topic {
			emitter.Deregister(v, chainEventCh)
		}
	})()

	netEventCh := make(chan nnet.Message, 128)
	net := neb.NetManager()
	net.Register(nnet.NewSubscriber(s, netEventCh, core.MessageTypeNewBlock))
	net.Register(nnet.NewSubscriber(s, netEventCh, core.MessageTypeNewTx))
	defer net.Deregister(nnet.NewSubscriber(s, netEventCh, core.MessageTypeNewBlock))
	defer net.Deregister(nnet.NewSubscriber(s, netEventCh, core.MessageTypeNewTx))

	var err error
	for {
		select {
		case event := <-chainEventCh:
			err = gs.Send(&rpcpb.SubscribeResponse{MsgType: event.Topic, Data: event.Data})
			if err != nil {
				return err
			}
		case event := <-netEventCh:
			switch event.MessageType() {
			case core.MessageTypeNewBlock:
				block := new(core.Block)
				pbblock := new(corepb.Block)
				if err := proto.Unmarshal(event.Data().([]byte), pbblock); err != nil {
					return err
				}
				if err := block.FromProto(pbblock); err != nil {
					return err
				}
				blockjson, err := json.Marshal(block)
				if err != nil {
					return err
				}
				err = gs.Send(&rpcpb.SubscribeResponse{MsgType: event.MessageType(), Data: string(blockjson)})
			case core.MessageTypeNewTx:
				tx := new(core.Transaction)
				pbTx := new(corepb.Transaction)
				if err := proto.Unmarshal(event.Data().([]byte), pbTx); err != nil {
					return err
				}
				if err := tx.FromProto(pbTx); err != nil {
					return err
				}
				txjson, err := json.Marshal(tx)
				if err != nil {
					return err
				}
				err = gs.Send(&rpcpb.SubscribeResponse{MsgType: event.MessageType(), Data: string(txjson)})
			}
		}
	}
}

// GetGasPrice get gas price from chain.
func (s *APIService) GetGasPrice(ctx context.Context, req *rpcpb.NonParamsRequest) (*rpcpb.GasPriceResponse, error) {
	logging.VLog().WithFields(logrus.Fields{
		"api": "/v1/user/getGasPrice",
	}).Info("Rpc request.")
	metricsRPCCounter.Mark(1)

	neb := s.server.Neblet()
	gasPrice := neb.BlockChain().GasPrice()
	return &rpcpb.GasPriceResponse{GasPrice: gasPrice.String()}, nil
}

// EstimateGas Compute the smart contract gas consumption.
func (s *APIService) EstimateGas(ctx context.Context, req *rpcpb.TransactionRequest) (*rpcpb.GasResponse, error) {
	logging.VLog().WithFields(logrus.Fields{
		"api": "/v1/user/estimateGas",
	}).Info("Rpc request.")
	metricsRPCCounter.Mark(1)

	neb := s.server.Neblet()
	tx, err := parseTransaction(neb, req)
	if err != nil {
		return nil, err
	}
	estimateGas, err := neb.BlockChain().EstimateGas(tx)
	if err != nil {
		return nil, err
	}
	return &rpcpb.GasResponse{Gas: estimateGas.String()}, nil
}

// GetGasUsed Compute the transaction gasused.
func (s *APIService) GetGasUsed(ctx context.Context, req *rpcpb.HashRequest) (*rpcpb.GasResponse, error) {
	logging.VLog().WithFields(logrus.Fields{
		"api": "/v1/user/GetGasUsed",
	}).Info("Rpc request.")
	metricsRPCCounter.Mark(1)

	neb := s.server.Neblet()
	hash, err := byteutils.FromHex(req.GetHash())
	if err != nil {
		return nil, err
	}

	tx := neb.BlockChain().GetTransaction(hash)
	if tx == nil {
		return nil, errors.New("transaction not found")
	}

	gas, err := neb.BlockChain().EstimateGas(tx)
	if err != nil {
		return nil, err
	}

	return &rpcpb.GasResponse{Gas: gas.String()}, nil
}

// GetEventsByHash return events by tx hash.
func (s *APIService) GetEventsByHash(ctx context.Context, req *rpcpb.HashRequest) (*rpcpb.EventsResponse, error) {
	logging.VLog().WithFields(logrus.Fields{
		"api": "/v1/user/getEventsByHash",
	}).Info("Rpc request.")
	metricsRPCCounter.Mark(1)

	neb := s.server.Neblet()
	bhash, _ := byteutils.FromHex(req.GetHash())
	tx, err := neb.BlockChain().TailBlock().GetTransaction(bhash)
	if err != nil {
		return nil, err
	}
	if tx != nil {
		result, err := neb.BlockChain().TailBlock().FetchEvents(tx.Hash())
		if err != nil {
			return nil, err
		}
		events := []*rpcpb.Event{}
		for _, v := range result {
			event := &rpcpb.Event{Topic: v.Topic, Data: v.Data}
			events = append(events, event)
		}

		return &rpcpb.EventsResponse{Events: events}, nil
	}

	return nil, nil

}
