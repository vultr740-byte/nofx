package telegram

import (
	"context"
	"crypto/ecdsa"
	"errors"
	"fmt"
	"log"
	"math/big"
	"strings"
	"time"

	"github.com/ethereum/go-ethereum"
	"github.com/ethereum/go-ethereum/accounts/abi"
	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/core/types"
	"github.com/ethereum/go-ethereum/crypto"
	"github.com/ethereum/go-ethereum/ethclient"
)

const usdcABIJSON = `[
  {"constant":true,"inputs":[{"name":"account","type":"address"}],"name":"balanceOf","outputs":[{"name":"","type":"uint256"}],"payable":false,"stateMutability":"view","type":"function"},
  {"constant":true,"inputs":[{"name":"owner","type":"address"}],"name":"nonces","outputs":[{"name":"","type":"uint256"}],"payable":false,"stateMutability":"view","type":"function"},
  {"constant":true,"inputs":[],"name":"DOMAIN_SEPARATOR","outputs":[{"name":"","type":"bytes32"}],"payable":false,"stateMutability":"view","type":"function"}
]`

const bridge2ABIJSON = `[
  {
    "inputs": [
      {
        "components": [
          {"internalType":"address","name":"user","type":"address"},
          {"internalType":"uint64","name":"usd","type":"uint64"},
          {"internalType":"uint64","name":"deadline","type":"uint64"},
          {
            "components": [
              {"internalType":"uint256","name":"r","type":"uint256"},
              {"internalType":"uint256","name":"s","type":"uint256"},
              {"internalType":"uint8","name":"v","type":"uint8"}
            ],
            "internalType":"struct Signature",
            "name":"signature",
            "type":"tuple"
          }
        ],
        "internalType":"struct DepositWithPermit[]",
        "name":"deposits",
        "type":"tuple[]"
      }
    ],
    "name":"batchedDepositWithPermit",
    "outputs": [],
    "stateMutability":"nonpayable",
    "type":"function"
  }
]`

var (
	permitTypeHash = common.HexToHash("0x6e71edae12b1b97f4d1f60370fef10105fa2faae0126114a169c64845d6126c9")
	transferTopic  = crypto.Keccak256Hash([]byte("Transfer(address,address,uint256)"))
)

// ArbitrumService provides helpers for interacting with Arbitrum RPC
type ArbitrumService struct {
	client       *ethclient.Client
	rpcURL       string
	chainID      *big.Int
	usdcAddress  common.Address
	usdcABI      abi.ABI
	bridgeAddr   common.Address
	bridge2ABI   abi.ABI
	callTimeout  time.Duration
	transferWait time.Duration
	// 可配置的gas费用设置
	gasTipCap *big.Int // Priority fee (Gwei)
	gasFeeCap *big.Int // Max fee (Gwei)
}

func NewArbitrumService(rpcURL string, chainID int64, usdcAddress string, bridgeAddress string) (*ArbitrumService, error) {
	if rpcURL == "" || usdcAddress == "" || bridgeAddress == "" {
		return nil, fmt.Errorf("缺少 Arbitrum 配置信息")
	}

	client, err := ethclient.Dial(rpcURL)
	if err != nil {
		return nil, fmt.Errorf("连接 Arbitrum RPC 失败: %w", err)
	}

	parsedUSDCABI, err := abi.JSON(strings.NewReader(usdcABIJSON))
	if err != nil {
		return nil, fmt.Errorf("解析 USDC ABI 失败: %w", err)
	}
	parsedBridge2ABI, err := abi.JSON(strings.NewReader(bridge2ABIJSON))
	if err != nil {
		return nil, fmt.Errorf("解析 Bridge2 ABI 失败: %w", err)
	}

	// 初始化gas费用设置
	// 默认值：Priority 0 ETH, Base 0.01 Gwei
	gasTipCap := big.NewInt(0)          // 0 ETH priority fee
	gasFeeCap := big.NewInt(10_000_000) // 0.01 Gwei base fee (默认值)

	return &ArbitrumService{
		client:       client,
		rpcURL:       rpcURL,
		chainID:      big.NewInt(chainID),
		usdcAddress:  common.HexToAddress(usdcAddress),
		usdcABI:      parsedUSDCABI,
		bridgeAddr:   common.HexToAddress(bridgeAddress),
		bridge2ABI:   parsedBridge2ABI,
		callTimeout:  10 * time.Second,
		transferWait: 2 * time.Second,
		gasTipCap:    gasTipCap,
		gasFeeCap:    gasFeeCap,
	}, nil
}

func (s *ArbitrumService) Close() {
	if s.client != nil {
		s.client.Close()
	}
}

func (s *ArbitrumService) GetETHBalance(address string) (*big.Int, error) {
	ctx, cancel := context.WithTimeout(context.Background(), s.callTimeout)
	defer cancel()

	balance, err := s.client.BalanceAt(ctx, common.HexToAddress(address), nil)
	if err != nil {
		return nil, fmt.Errorf("获取 ETH 余额失败: %w", err)
	}
	return balance, nil
}

func (s *ArbitrumService) GetUSDCBalance(address string) (*big.Int, error) {
	ctx, cancel := context.WithTimeout(context.Background(), s.callTimeout)
	defer cancel()

	data, err := s.usdcABI.Pack("balanceOf", common.HexToAddress(address))
	if err != nil {
		return nil, fmt.Errorf("编码 balanceOf 调用失败: %w", err)
	}

	result, err := s.client.CallContract(ctx, ethereum.CallMsg{
		To:   &s.usdcAddress,
		Data: data,
	}, nil)
	if err != nil {
		return nil, fmt.Errorf("调用 balanceOf 失败: %w", err)
	}

	values, err := s.usdcABI.Unpack("balanceOf", result)
	if err != nil || len(values) == 0 {
		return nil, fmt.Errorf("解析 balanceOf 返回值失败: %w", err)
	}

	if balance, ok := values[0].(*big.Int); ok {
		return balance, nil
	}
	return nil, fmt.Errorf("balanceOf 返回值类型错误")
}

type bridge2Signature struct {
	R *big.Int `abi:"r"`
	S *big.Int `abi:"s"`
	V uint8    `abi:"v"`
}

type bridge2DepositWithPermit struct {
	User      common.Address   `abi:"user"`
	Usd       uint64           `abi:"usd"`
	Deadline  uint64           `abi:"deadline"`
	Signature bridge2Signature `abi:"signature"`
}

func (s *ArbitrumService) DepositUSDCToBridgeWithPermit(
	ctx context.Context,
	sponsorPrivateKeyHex string,
	userPrivateKeyHex string,
	walletAddr string,
	amount *big.Int,
	deadlineSec uint64,
) (string, error) {
	if s.client == nil {
		return "", fmt.Errorf("Arbitrum client 未初始化")
	}
	if strings.TrimSpace(sponsorPrivateKeyHex) == "" {
		return "", fmt.Errorf("缺少 sponsor 私钥")
	}
	if strings.TrimSpace(userPrivateKeyHex) == "" {
		return "", fmt.Errorf("缺少用户私钥")
	}
	if strings.TrimSpace(walletAddr) == "" {
		return "", fmt.Errorf("缺少用户地址")
	}
	if amount == nil || amount.Sign() <= 0 {
		return "", fmt.Errorf("USDC 金额无效")
	}
	if deadlineSec == 0 {
		return "", fmt.Errorf("deadline 无效")
	}

	userPriv, userAddrFromKey, err := parsePrivateKey(userPrivateKeyHex)
	if err != nil {
		return "", err
	}
	expectedUserAddr := common.HexToAddress(walletAddr)
	if userAddrFromKey != expectedUserAddr {
		return "", fmt.Errorf("用户私钥地址不匹配: key=%s wallet=%s", userAddrFromKey.Hex(), expectedUserAddr.Hex())
	}

	baseNonce, domainSep, err := s.getUSDCPermitContext(ctx, expectedUserAddr)
	if err != nil {
		return "", err
	}

	deposits, err := buildBridge2DepositsWithPermit(userPriv, expectedUserAddr, s.bridgeAddr, amount, baseNonce, deadlineSec, domainSep)
	if err != nil {
		return "", err
	}

	callData, err := s.bridge2ABI.Pack("batchedDepositWithPermit", deposits)
	if err != nil {
		return "", fmt.Errorf("编码 Bridge2 batchedDepositWithPermit 失败: %w", err)
	}

	return s.sendBridge2Tx(ctx, sponsorPrivateKeyHex, callData)
}

func (s *ArbitrumService) WaitForReceipt(ctx context.Context, txHash string, timeout time.Duration) (*types.Receipt, error) {
	if s.client == nil {
		return nil, fmt.Errorf("Arbitrum client 未初始化")
	}
	h := common.HexToHash(strings.TrimSpace(txHash))
	if h == (common.Hash{}) {
		return nil, fmt.Errorf("txHash 无效")
	}
	if timeout <= 0 {
		timeout = 2 * time.Minute
	}

	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	ticker := time.NewTicker(2 * time.Second)
	defer ticker.Stop()

	for {
		receipt, err := s.client.TransactionReceipt(ctx, h)
		if err == nil {
			return receipt, nil
		}
		if errors.Is(err, ethereum.NotFound) {
			select {
			case <-ctx.Done():
				return nil, fmt.Errorf("等待交易确认超时: %s", h.Hex())
			case <-ticker.C:
				continue
			}
		}
		return nil, fmt.Errorf("查询交易回执失败: %w", err)
	}
}

func (s *ArbitrumService) SumUSDCTokenTransfers(receipt *types.Receipt, from common.Address, to common.Address) *big.Int {
	sum := big.NewInt(0)
	if receipt == nil {
		return sum
	}
	for _, lg := range receipt.Logs {
		if lg == nil || lg.Address != s.usdcAddress {
			continue
		}
		if len(lg.Topics) < 3 || lg.Topics[0] != transferTopic {
			continue
		}
		fromTopic := common.BytesToAddress(lg.Topics[1].Bytes()[12:])
		toTopic := common.BytesToAddress(lg.Topics[2].Bytes()[12:])
		if fromTopic != from || toTopic != to {
			continue
		}
		if len(lg.Data) != 32 {
			continue
		}
		v := new(big.Int).SetBytes(lg.Data)
		sum.Add(sum, v)
	}
	return sum
}

// getGasCaps 动态获取网络Base Fee，设置Max Fee = Base Fee
func (s *ArbitrumService) getGasCaps(ctx context.Context) (*big.Int, *big.Int) {
	// 获取最新区块的 Base Fee
	header, err := s.client.HeaderByNumber(ctx, nil)
	var baseFee *big.Int
	if err != nil || header == nil || header.BaseFee == nil || header.BaseFee.Sign() <= 0 {
		// 获取失败时使用默认值：0.01 Gwei = 10,000,000 Wei
		log.Printf("⚠️ 获取区块Base Fee失败，使用默认值: %v", err)
		baseFee = big.NewInt(10_000_000) // 0.01 Gwei
	} else {
		baseFee = header.BaseFee
	}

	tipCap := big.NewInt(0) // Priority Fee = 0 (不给矿工小费)

	// 在 baseFee 基础上添加 2% 缓冲
	buffer := new(big.Int).Div(baseFee, big.NewInt(50)) // 2% = baseFee / 50
	feeCap := new(big.Int).Add(baseFee, buffer)         // Max Fee = Base Fee + 2% 缓冲

	log.Printf("🔧 Gas费用设置: Priority=0 Gwei, Max=%s Gwei (Base Fee %s Gwei + 2%% 缓冲)",
		new(big.Float).Quo(new(big.Float).SetInt(feeCap), big.NewFloat(1e9)).String(),
		new(big.Float).Quo(new(big.Float).SetInt(baseFee), big.NewFloat(1e9)).String())

	return tipCap, feeCap
}

func parsePrivateKey(hexKey string) (*ecdsa.PrivateKey, common.Address, error) {
	trimmed := strings.TrimPrefix(strings.TrimSpace(hexKey), "0x")
	privKey, err := crypto.HexToECDSA(trimmed)
	if err != nil {
		return nil, common.Address{}, fmt.Errorf("解析私钥失败: %w", err)
	}
	addr := crypto.PubkeyToAddress(privKey.PublicKey)
	return privKey, addr, nil
}

// CalcWeiFromETH converts an ETH float value to Wei as big.Int
func CalcWeiFromETH(amount float64) *big.Int {
	if amount <= 0 {
		return big.NewInt(0)
	}
	f := new(big.Float).SetFloat64(amount)
	weiFactor := new(big.Float).SetFloat64(1e18)
	value := new(big.Float).Mul(f, weiFactor)
	wei := new(big.Int)
	value.Int(wei)
	return wei
}

func (s *ArbitrumService) getUSDCPermitContext(ctx context.Context, owner common.Address) (*big.Int, common.Hash, error) {
	callCtx, cancel := context.WithTimeout(ctx, s.callTimeout)
	defer cancel()

	nonceData, err := s.usdcABI.Pack("nonces", owner)
	if err != nil {
		return nil, common.Hash{}, fmt.Errorf("编码 USDC nonces 调用失败: %w", err)
	}
	nonceResult, err := s.client.CallContract(callCtx, ethereum.CallMsg{To: &s.usdcAddress, Data: nonceData}, nil)
	if err != nil {
		return nil, common.Hash{}, fmt.Errorf("调用 USDC nonces 失败: %w", err)
	}
	nonceVals, err := s.usdcABI.Unpack("nonces", nonceResult)
	if err != nil || len(nonceVals) == 0 {
		return nil, common.Hash{}, fmt.Errorf("解析 USDC nonces 返回值失败: %w", err)
	}
	nonce, ok := nonceVals[0].(*big.Int)
	if !ok || nonce == nil {
		return nil, common.Hash{}, fmt.Errorf("USDC nonces 返回值类型错误")
	}

	dsData, err := s.usdcABI.Pack("DOMAIN_SEPARATOR")
	if err != nil {
		return nil, common.Hash{}, fmt.Errorf("编码 USDC DOMAIN_SEPARATOR 调用失败: %w", err)
	}
	dsResult, err := s.client.CallContract(callCtx, ethereum.CallMsg{To: &s.usdcAddress, Data: dsData}, nil)
	if err != nil {
		return nil, common.Hash{}, fmt.Errorf("调用 USDC DOMAIN_SEPARATOR 失败: %w", err)
	}
	dsVals, err := s.usdcABI.Unpack("DOMAIN_SEPARATOR", dsResult)
	if err != nil || len(dsVals) == 0 {
		return nil, common.Hash{}, fmt.Errorf("解析 USDC DOMAIN_SEPARATOR 返回值失败: %w", err)
	}
	switch v := dsVals[0].(type) {
	case [32]byte:
		return new(big.Int).Set(nonce), common.BytesToHash(v[:]), nil
	case common.Hash:
		return new(big.Int).Set(nonce), v, nil
	default:
		return nil, common.Hash{}, fmt.Errorf("USDC DOMAIN_SEPARATOR 返回值类型错误: %T", dsVals[0])
	}
}

func buildBridge2DepositsWithPermit(
	userPriv *ecdsa.PrivateKey,
	user common.Address,
	spender common.Address,
	amount *big.Int,
	startNonce *big.Int,
	deadlineSec uint64,
	domainSep common.Hash,
) ([]bridge2DepositWithPermit, error) {
	if amount == nil || amount.Sign() <= 0 {
		return nil, fmt.Errorf("USDC 金额无效")
	}
	if startNonce == nil || startNonce.Sign() < 0 {
		return nil, fmt.Errorf("nonce 无效")
	}
	if deadlineSec == 0 {
		return nil, fmt.Errorf("deadline 无效")
	}

	maxU64 := new(big.Int).SetUint64(^uint64(0))
	remaining := new(big.Int).Set(amount)
	nonce := new(big.Int).Set(startNonce)
	deadlineBig := new(big.Int).SetUint64(deadlineSec)

	var deposits []bridge2DepositWithPermit
	for remaining.Sign() > 0 {
		chunk := new(big.Int)
		if remaining.Cmp(maxU64) > 0 {
			chunk.Set(maxU64)
		} else {
			chunk.Set(remaining)
		}
		if chunk.Sign() <= 0 {
			break
		}
		if chunk.BitLen() > 64 {
			return nil, fmt.Errorf("金额超出 uint64 范围")
		}
		usd := chunk.Uint64()

		digest, err := buildUSDCPermitDigest(user, spender, chunk, nonce, deadlineBig, domainSep)
		if err != nil {
			return nil, err
		}
		sig, err := crypto.Sign(digest.Bytes(), userPriv)
		if err != nil {
			return nil, fmt.Errorf("签名 permit 失败: %w", err)
		}
		if len(sig) != 65 {
			return nil, fmt.Errorf("签名 permit 长度异常: %d", len(sig))
		}

		r := new(big.Int).SetBytes(sig[:32])
		s := new(big.Int).SetBytes(sig[32:64])
		v := uint8(sig[64]) + 27

		deposits = append(deposits, bridge2DepositWithPermit{
			User:     user,
			Usd:      usd,
			Deadline: deadlineSec,
			Signature: bridge2Signature{
				R: r,
				S: s,
				V: v,
			},
		})

		remaining.Sub(remaining, chunk)
		nonce.Add(nonce, big.NewInt(1))
	}

	if len(deposits) == 0 {
		return nil, fmt.Errorf("未生成任何 permit deposit")
	}
	return deposits, nil
}

func buildUSDCPermitDigest(
	owner common.Address,
	spender common.Address,
	value *big.Int,
	nonce *big.Int,
	deadline *big.Int,
	domainSep common.Hash,
) (common.Hash, error) {
	bytes32Type, err := abi.NewType("bytes32", "", nil)
	if err != nil {
		return common.Hash{}, err
	}
	addressType, err := abi.NewType("address", "", nil)
	if err != nil {
		return common.Hash{}, err
	}
	uint256Type, err := abi.NewType("uint256", "", nil)
	if err != nil {
		return common.Hash{}, err
	}

	args := abi.Arguments{
		{Type: bytes32Type},
		{Type: addressType},
		{Type: addressType},
		{Type: uint256Type},
		{Type: uint256Type},
		{Type: uint256Type},
	}
	enc, err := args.Pack(permitTypeHash, owner, spender, value, nonce, deadline)
	if err != nil {
		return common.Hash{}, fmt.Errorf("编码 permit struct 失败: %w", err)
	}
	structHash := crypto.Keccak256Hash(enc)

	prefix := []byte{0x19, 0x01}
	b := make([]byte, 0, 2+32+32)
	b = append(b, prefix...)
	b = append(b, domainSep.Bytes()...)
	b = append(b, structHash.Bytes()...)
	return crypto.Keccak256Hash(b), nil
}

func (s *ArbitrumService) sendBridge2Tx(ctx context.Context, sponsorPrivateKeyHex string, data []byte) (string, error) {
	privKey, fromAddr, err := parsePrivateKey(sponsorPrivateKeyHex)
	if err != nil {
		return "", err
	}

	callCtx, cancel := context.WithTimeout(ctx, s.callTimeout)
	defer cancel()

	nonce, err := s.client.PendingNonceAt(callCtx, fromAddr)
	if err != nil {
		return "", fmt.Errorf("获取 sponsor nonce 失败: %w", err)
	}

	tipCap, feeCap := s.getGasCaps(callCtx)

	toAddr := s.bridgeAddr
	gasLimit, err := s.client.EstimateGas(callCtx, ethereum.CallMsg{
		From: fromAddr,
		To:   &toAddr,
		Data: data,
	})
	if err != nil || gasLimit == 0 {
		gasLimit = 350000
		log.Printf("⚠️ Bridge2 batchedDepositWithPermit Gas 估算失败，使用默认值: %v", err)
	} else {
		gasLimit += gasLimit / 5 // ~20% buffer
	}

	tx := types.NewTx(&types.DynamicFeeTx{
		ChainID:   s.chainID,
		Nonce:     nonce,
		GasTipCap: tipCap,
		GasFeeCap: feeCap,
		Gas:       gasLimit,
		To:        &toAddr,
		Value:     big.NewInt(0),
		Data:      data,
	})

	signedTx, err := types.SignTx(tx, types.LatestSignerForChainID(s.chainID), privKey)
	if err != nil {
		return "", fmt.Errorf("签名 Bridge2 交易失败: %w", err)
	}
	if err := s.client.SendTransaction(callCtx, signedTx); err != nil {
		return "", fmt.Errorf("发送 Bridge2 交易失败: %w", err)
	}

	time.Sleep(s.transferWait)
	return signedTx.Hash().Hex(), nil
}
