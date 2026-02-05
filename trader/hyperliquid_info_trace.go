package trader

import (
	"fmt"
	"log"
	"sync/atomic"
	"time"
)

var hlInfoReqSeq uint64
var hlInfoInflight int64

func startHLInfoTrace(endpoint, wallet string, testnet bool, caller string, wait time.Duration) (uint64, time.Time) {
	id := atomic.AddUint64(&hlInfoReqSeq, 1)
	inflight := atomic.AddInt64(&hlInfoInflight, 1)
	start := time.Now()

	callerPart := ""
	if caller != "" {
		callerPart = fmt.Sprintf(" caller=%s", caller)
	}
	waitPart := ""
	if wait > 0 {
		waitPart = fmt.Sprintf(" wait=%s", wait.Round(time.Millisecond))
	}

	log.Printf("🔎 [HL info] start id=%d endpoint=%s wallet=%s testnet=%t inflight=%d%s%s",
		id, endpoint, shortHexAddr(wallet), testnet, inflight, callerPart, waitPart)
	return id, start
}

func endHLInfoTrace(id uint64, endpoint, wallet string, testnet bool, start time.Time, err error) {
	dur := time.Since(start).Round(time.Millisecond)
	inflight := atomic.AddInt64(&hlInfoInflight, -1)
	if err != nil {
		log.Printf("🔎 [HL info] end id=%d endpoint=%s wallet=%s testnet=%t inflight=%d dur=%s status=err err=%v",
			id, endpoint, shortHexAddr(wallet), testnet, inflight, dur, err)
		return
	}
	log.Printf("🔎 [HL info] end id=%d endpoint=%s wallet=%s testnet=%t inflight=%d dur=%s status=ok",
		id, endpoint, shortHexAddr(wallet), testnet, inflight, dur)
}
