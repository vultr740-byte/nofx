package telegram

import (
	"context"
	"crypto/ecdsa"
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

const erc20ABIJSON = `[{"constant":true,"inputs":[{"name":"account","type":"address"}],"name":"balanceOf","outputs":[{"name":"","type":"uint256"}],"payable":false,"stateMutability":"view","type":"function"},{"constant":false,"inputs":[{"name":"recipient","type":"address"},{"name":"amount","type":"uint256"}],"name":"transfer","outputs":[{"name":"","type":"bool"}],"payable":false,"stateMutability":"nonpayable","type":"function"}]`

// ArbitrumService provides helpers for interacting with Arbitrum RPC
type ArbitrumService struct {
	client       *ethclient.Client
	rpcURL       string
	chainID      *big.Int
	usdcAddress  common.Address
	erc20ABI     abi.ABI
	bridgeAddr   common.Address
	callTimeout  time.Duration
	transferWait time.Duration
	// 可配置的gas费用设置
	gasTipCap    *big.Int // Priority fee (Gwei)
	gasFeeCap    *big.Int // Max fee (Gwei)
}

func NewArbitrumService(rpcURL string, chainID int64, usdcAddress string, bridgeAddress string) (*ArbitrumService, error) {
	if rpcURL == "" || usdcAddress == "" || bridgeAddress == "" {
		return nil, fmt.Errorf("缺少 Arbitrum 配置信息")
	}

	client, err := ethclient.Dial(rpcURL)
	if err != nil {
		return nil, fmt.Errorf("连接 Arbitrum RPC 失败: %w", err)
	}

	parsedABI, err := abi.JSON(strings.NewReader(erc20ABIJSON))
	if err != nil {
		return nil, fmt.Errorf("解析 ERC20 ABI 失败: %w", err)
	}

	// 初始化gas费用设置
	// 默认值：Priority 0 ETH, Base 0.01 Gwei
	gasTipCap := big.NewInt(0)               // 0 ETH priority fee
	gasFeeCap := big.NewInt(10_000_000)       // 0.01 Gwei base fee (默认值)

	return &ArbitrumService{
		client:       client,
		rpcURL:       rpcURL,
		chainID:      big.NewInt(chainID),
		usdcAddress:  common.HexToAddress(usdcAddress),
		erc20ABI:     parsedABI,
		bridgeAddr:   common.HexToAddress(bridgeAddress),
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

	data, err := s.erc20ABI.Pack("balanceOf", common.HexToAddress(address))
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

	values, err := s.erc20ABI.Unpack("balanceOf", result)
	if err != nil || len(values) == 0 {
		return nil, fmt.Errorf("解析 balanceOf 返回值失败: %w", err)
	}

	if balance, ok := values[0].(*big.Int); ok {
		return balance, nil
	}
	return nil, fmt.Errorf("balanceOf 返回值类型错误")
}

func (s *ArbitrumService) SendGas(privateKeyHex, toAddress string, amountWei *big.Int) (string, error) {
	if amountWei == nil || amountWei.Sign() <= 0 {
		return "", fmt.Errorf("发送 Gas 金额无效")
	}

	privKey, fromAddr, err := parsePrivateKey(privateKeyHex)
	if err != nil {
		return "", err
	}

	ctx, cancel := context.WithTimeout(context.Background(), s.callTimeout)
	defer cancel()

	nonce, err := s.client.PendingNonceAt(ctx, fromAddr)
	if err != nil {
		return "", fmt.Errorf("获取 nonce 失败: %w", err)
	}

	tipCap, feeCap := s.getGasCaps(ctx)

	log.Printf("💰 发送ETH转账: 金额=%s ETH, Gas费用(Base=Max=%.3f Gwei, Priority=0 Gwei)",
		new(big.Float).Quo(new(big.Float).SetInt(amountWei), big.NewFloat(1e18)).String(),
		new(big.Float).Quo(new(big.Float).SetInt(feeCap), big.NewFloat(1e9)).String())

	toAddr := common.HexToAddress(toAddress)
	tx := types.NewTx(&types.DynamicFeeTx{
		ChainID:   s.chainID,
		Nonce:     nonce,
		GasTipCap: tipCap,
		GasFeeCap: feeCap,
		Gas:       21000,
		To:        &toAddr,
		Value:     amountWei,
		Data:      nil,
	})

	signedTx, err := types.SignTx(tx, types.LatestSignerForChainID(s.chainID), privKey)
	if err != nil {
		return "", fmt.Errorf("签名 Gas 交易失败: %w", err)
	}

	if err := s.client.SendTransaction(ctx, signedTx); err != nil {
		return "", fmt.Errorf("发送 Gas 交易失败: %w", err)
	}

	time.Sleep(s.transferWait)
	return signedTx.Hash().Hex(), nil
}

func (s *ArbitrumService) TransferUSDC(privateKeyHex string, amount *big.Int) (string, error) {
	if amount == nil || amount.Sign() <= 0 {
		return "", fmt.Errorf("USDC 金额无效")
	}

	privKey, fromAddr, err := parsePrivateKey(privateKeyHex)
	if err != nil {
		return "", err
	}

	ctx, cancel := context.WithTimeout(context.Background(), s.callTimeout)
	defer cancel()

	nonce, err := s.client.PendingNonceAt(ctx, fromAddr)
	if err != nil {
		return "", fmt.Errorf("获取 nonce 失败: %w", err)
	}

	tipCap, feeCap := s.getGasCaps(ctx)

	data, err := s.erc20ABI.Pack("transfer", s.bridgeAddr, amount)
	if err != nil {
		return "", fmt.Errorf("编码 transfer 调用失败: %w", err)
	}

	gasLimit, err := s.client.EstimateGas(ctx, ethereum.CallMsg{
		From: fromAddr,
		To:   &s.usdcAddress,
		Data: data,
	})
	if err != nil || gasLimit == 0 {
		gasLimit = 60000
	} else {
		gasLimit += gasLimit / 5 // add ~20% safety buffer
	}

	gasCost := new(big.Int).Mul(feeCap, big.NewInt(int64(gasLimit)))
	balance, err := s.client.BalanceAt(ctx, fromAddr, nil)
	if err != nil {
		return "", fmt.Errorf("查询 ETH 余额失败: %w", err)
	}
	if balance.Cmp(gasCost) < 0 {
		return "", fmt.Errorf("账户 ETH 不足，需至少 %s wei，当前 %s wei", gasCost.String(), balance.String())
	}

	tx := types.NewTx(&types.DynamicFeeTx{
		ChainID:   s.chainID,
		Nonce:     nonce,
		GasTipCap: tipCap,
		GasFeeCap: feeCap,
		Gas:       gasLimit,
		To:        &s.usdcAddress,
		Value:     big.NewInt(0),
		Data:      data,
	})

	signedTx, err := types.SignTx(tx, types.LatestSignerForChainID(s.chainID), privKey)
	if err != nil {
		return "", fmt.Errorf("签名 USDC 交易失败: %w", err)
	}

	if err := s.client.SendTransaction(ctx, signedTx); err != nil {
		return "", fmt.Errorf("发送 USDC 交易失败: %w", err)
	}

	time.Sleep(s.transferWait)
	return signedTx.Hash().Hex(), nil
}

// getGasCaps 动态获取网络Base Fee，设置Max Fee = Base Fee
func (s *ArbitrumService) getGasCaps(ctx context.Context) (*big.Int, *big.Int) {
	// 获取最新区块的 Base Fee
	header, err := s.client.HeaderByNumber(ctx, nil)
	var baseFee *big.Int
	if err != nil || header == nil || header.BaseFee == nil || header.BaseFee.Sign() <= 0 {
		// 获取失败时使用默认值：0.01 Gwei = 10,000,000 Wei
		log.Printf("⚠️ 获取区块Base Fee失败，使用默认值: %v", err)
		baseFee = big.NewInt(10_000_000)  // 0.01 Gwei
	} else {
		baseFee = header.BaseFee
	}

	tipCap := big.NewInt(0)        // Priority Fee = 0 (不给矿工小费)
	feeCap := new(big.Int).Set(baseFee)  // Max Fee = Base Fee

	log.Printf("🔧 Gas费用设置: Priority=0 Gwei, Max=%.3f Gwei (实时Base Fee)",
		new(big.Float).Quo(new(big.Float).SetInt(feeCap), big.NewFloat(1e9)).String())

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

// EstimateUSDCTransferCost roughly calculates gas cost for a transfer
func (s *ArbitrumService) EstimateUSDCTransferCost(fromAddress string, amount *big.Int) (*big.Int, error) {
	if amount == nil || amount.Sign() <= 0 {
		return nil, fmt.Errorf("USDC 金额无效")
	}

	ctx, cancel := context.WithTimeout(context.Background(), s.callTimeout)
	defer cancel()

	feeCap, err := s.client.SuggestGasPrice(ctx)
	if err != nil {
		feeCap = big.NewInt(13_500_000)
	}

	baseFee, err := s.client.SuggestGasPrice(ctx)
	if err == nil && feeCap.Cmp(baseFee) < 0 {
		feeCap = baseFee
	}

	data, err := s.erc20ABI.Pack("transfer", s.bridgeAddr, amount)
	if err != nil {
		return nil, fmt.Errorf("编码 transfer 调用失败: %w", err)
	}

	fromAddr := common.HexToAddress(fromAddress)
	gasLimit, err := s.client.EstimateGas(ctx, ethereum.CallMsg{
		From: fromAddr,
		To:   &s.usdcAddress,
		Data: data,
	})
	if err != nil || gasLimit == 0 {
		gasLimit = 60000
	} else {
		gasLimit += gasLimit / 5
	}

	gasCost := new(big.Int).Mul(feeCap, big.NewInt(int64(gasLimit)))
	return gasCost, nil
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
