package telegram

import (
	"math/big"
	"testing"

	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/crypto"
)

func TestBuildBridge2DepositsWithPermit_SignsAndRecovers(t *testing.T) {
	userPriv, err := crypto.GenerateKey()
	if err != nil {
		t.Fatalf("GenerateKey: %v", err)
	}
	userAddr := crypto.PubkeyToAddress(userPriv.PublicKey)
	spender := common.HexToAddress(hyperliquidBridgeAddress)

	amount := big.NewInt(12345)
	startNonce := big.NewInt(7)
	deadlineSec := uint64(1700000000)
	domainSep := common.HexToHash("0x08d11903f8419e68b1b8721bcbe2e9fc68569122a77ef18c216f10b3b5112c78")

	deposits, err := buildBridge2DepositsWithPermit(userPriv, userAddr, spender, amount, startNonce, deadlineSec, domainSep)
	if err != nil {
		t.Fatalf("buildBridge2DepositsWithPermit: %v", err)
	}
	if len(deposits) != 1 {
		t.Fatalf("expected 1 deposit, got %d", len(deposits))
	}
	dep := deposits[0]
	if dep.User != userAddr {
		t.Fatalf("unexpected user: %s", dep.User.Hex())
	}
	if dep.Usd != 12345 {
		t.Fatalf("unexpected usd: %d", dep.Usd)
	}
	if dep.Deadline != deadlineSec {
		t.Fatalf("unexpected deadline: %d", dep.Deadline)
	}
	if dep.Signature.V != 27 && dep.Signature.V != 28 {
		t.Fatalf("unexpected v: %d", dep.Signature.V)
	}

	digest, err := buildUSDCPermitDigest(
		userAddr,
		spender,
		amount,
		startNonce,
		new(big.Int).SetUint64(deadlineSec),
		domainSep,
	)
	if err != nil {
		t.Fatalf("buildUSDCPermitDigest: %v", err)
	}

	sig := make([]byte, 65)
	dep.Signature.R.FillBytes(sig[:32])
	dep.Signature.S.FillBytes(sig[32:64])
	sig[64] = dep.Signature.V - 27

	pub, err := crypto.SigToPub(digest.Bytes(), sig)
	if err != nil {
		t.Fatalf("SigToPub: %v", err)
	}
	recovered := crypto.PubkeyToAddress(*pub)
	if recovered != userAddr {
		t.Fatalf("recovered mismatch: got %s want %s", recovered.Hex(), userAddr.Hex())
	}
}

func TestBuildBridge2DepositsWithPermit_SplitsLargeAmount(t *testing.T) {
	userPriv, err := crypto.GenerateKey()
	if err != nil {
		t.Fatalf("GenerateKey: %v", err)
	}
	userAddr := crypto.PubkeyToAddress(userPriv.PublicKey)
	spender := common.HexToAddress(hyperliquidBridgeAddress)

	maxU64 := new(big.Int).SetUint64(^uint64(0))
	amount := new(big.Int).Add(maxU64, big.NewInt(1))
	startNonce := big.NewInt(0)
	deadlineSec := uint64(1700000000)
	domainSep := common.HexToHash("0x08d11903f8419e68b1b8721bcbe2e9fc68569122a77ef18c216f10b3b5112c78")

	deposits, err := buildBridge2DepositsWithPermit(userPriv, userAddr, spender, amount, startNonce, deadlineSec, domainSep)
	if err != nil {
		t.Fatalf("buildBridge2DepositsWithPermit: %v", err)
	}
	if len(deposits) != 2 {
		t.Fatalf("expected 2 deposits, got %d", len(deposits))
	}
	if deposits[0].Usd != ^uint64(0) {
		t.Fatalf("unexpected first chunk: %d", deposits[0].Usd)
	}
	if deposits[1].Usd != 1 {
		t.Fatalf("unexpected second chunk: %d", deposits[1].Usd)
	}

	for i, dep := range deposits {
		nonce := new(big.Int).Add(startNonce, big.NewInt(int64(i)))
		value := new(big.Int).SetUint64(dep.Usd)
		digest, err := buildUSDCPermitDigest(
			userAddr,
			spender,
			value,
			nonce,
			new(big.Int).SetUint64(deadlineSec),
			domainSep,
		)
		if err != nil {
			t.Fatalf("buildUSDCPermitDigest[%d]: %v", i, err)
		}
		sig := make([]byte, 65)
		dep.Signature.R.FillBytes(sig[:32])
		dep.Signature.S.FillBytes(sig[32:64])
		sig[64] = dep.Signature.V - 27

		pub, err := crypto.SigToPub(digest.Bytes(), sig)
		if err != nil {
			t.Fatalf("SigToPub[%d]: %v", i, err)
		}
		recovered := crypto.PubkeyToAddress(*pub)
		if recovered != userAddr {
			t.Fatalf("recovered mismatch[%d]: got %s want %s", i, recovered.Hex(), userAddr.Hex())
		}
	}
}
