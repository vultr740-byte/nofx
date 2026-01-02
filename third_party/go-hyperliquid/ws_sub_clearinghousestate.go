package hyperliquid

import "fmt"

type ClearinghouseStateSubscriptionParams struct {
	User string
	Dex  *string
}

type AllDexsClearinghouseStateSubscriptionParams struct {
	User string
}

func (w *WebsocketClient) ClearinghouseState(
	params ClearinghouseStateSubscriptionParams,
	callback func(ClearinghouseState, error),
) (*Subscription, error) {
	payload := remoteClearinghouseStateSubscriptionPayload{
		Type: ChannelClearinghouseState,
		User: params.User,
		Dex:  params.Dex,
	}

	return w.subscribe(payload, func(msg any) {
		state, ok := msg.(ClearinghouseState)
		if !ok {
			callback(ClearinghouseState{}, fmt.Errorf("invalid message type"))
			return
		}

		callback(state, nil)
	})
}

func (w *WebsocketClient) AllDexsClearinghouseState(
	params AllDexsClearinghouseStateSubscriptionParams,
	callback func(AllDexsClearinghouseState, error),
) (*Subscription, error) {
	payload := remoteAllDexsClearinghouseStateSubscriptionPayload{
		Type: ChannelAllDexsClearinghouseState,
		User: params.User,
	}

	return w.subscribe(payload, func(msg any) {
		state, ok := msg.(AllDexsClearinghouseState)
		if !ok {
			callback(AllDexsClearinghouseState{}, fmt.Errorf("invalid message type"))
			return
		}

		callback(state, nil)
	})
}
