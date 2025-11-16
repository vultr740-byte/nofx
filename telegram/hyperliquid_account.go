package telegram

import (
	"encoding/hex"
	"fmt"

	"github.com/ethereum/go-ethereum/crypto"
)

func generateHyperliquidAccount() (agentKey, walletAddr string, err error) {
	privateKey, err := crypto.GenerateKey()
	if err != nil {
		return "", "", fmt.Errorf("生成私钥失败: %w", err)
	}
	agentKey = hex.EncodeToString(privateKey.D.Bytes())

	privateKeyECDSA, err := crypto.ToECDSA(privateKey.D.Bytes())
	if err != nil {
		return "", "", fmt.Errorf("转换私钥格式失败: %w", err)
	}
	address := crypto.PubkeyToAddress(privateKeyECDSA.PublicKey)
	walletAddr = address.Hex()

	return agentKey, walletAddr, nil
}
