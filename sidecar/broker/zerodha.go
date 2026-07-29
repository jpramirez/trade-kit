package broker

import (
	"fmt"
	"sync"

	zerodhaclient "zerodha-cli/client"
	zerodhaops "zerodha-cli/ops"
)

type ZerodhaAdapter struct {
	mu     sync.RWMutex
	client *zerodhaclient.ZerodhaClient
	paper  bool
	creds  map[string]string
}

func NewZerodhaAdapter() *ZerodhaAdapter {
	return &ZerodhaAdapter{paper: true}
}

func (a *ZerodhaAdapter) ID() string   { return "zerodha" }
func (a *ZerodhaAdapter) Name() string { return "Zerodha (Kite)" }

func (a *ZerodhaAdapter) Connected() bool {
	a.mu.RLock()
	defer a.mu.RUnlock()
	return a.client != nil
}

func (a *ZerodhaAdapter) Connect(creds map[string]string) error {
	a.mu.Lock()
	defer a.mu.Unlock()

	apiKey := creds["api_key"]
	accessToken := creds["access_token"]
	if apiKey == "" || accessToken == "" {
		return fmt.Errorf("zerodha: api_key and access_token are required")
	}

	c, err := zerodhaclient.NewFromCreds(apiKey, accessToken, a.paper)
	if err != nil {
		return fmt.Errorf("zerodha: %w", err)
	}
	a.client = c
	a.creds = creds
	return nil
}

func (a *ZerodhaAdapter) Test() error {
	a.mu.RLock()
	defer a.mu.RUnlock()
	if a.client == nil {
		return fmt.Errorf("zerodha: not connected")
	}
	_, err := zerodhaops.GetAccount(a.client)
	return err
}

func (a *ZerodhaAdapter) Disconnect() error {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.client = nil
	return nil
}

func (a *ZerodhaAdapter) Positions() ([]Position, error) {
	a.mu.RLock()
	c := a.client
	a.mu.RUnlock()
	if c == nil {
		return nil, fmt.Errorf("zerodha: not connected")
	}
	positions, err := zerodhaops.GetPositions(c)
	if err != nil {
		return nil, err
	}
	out := make([]Position, len(positions))
	for i, p := range positions {
		side := "BUY"
		if p.Quantity < 0 {
			side = "SELL"
		}
		qty := p.Quantity
		if qty < 0 {
			qty = -qty
		}
		out[i] = Position{
			Symbol:      p.Symbol,
			Side:        side,
			Units:       float64(qty),
			OpenRate:    p.AvgPrice,
			CurrentRate: p.LastPrice,
			PnL:         p.PnL,
		}
	}
	return out, nil
}

func (a *ZerodhaAdapter) Account() (AccountInfo, error) {
	a.mu.RLock()
	c := a.client
	a.mu.RUnlock()
	if c == nil {
		return AccountInfo{}, fmt.Errorf("zerodha: not connected")
	}
	acct, err := zerodhaops.GetAccount(c)
	if err != nil {
		return AccountInfo{}, err
	}
	return AccountInfo{
		Equity:    acct.Net,
		Cash:      acct.Cash,
		Available: acct.Cash,
	}, nil
}

func (a *ZerodhaAdapter) Orders() ([]OrderInfo, error) {
	a.mu.RLock()
	c := a.client
	a.mu.RUnlock()
	if c == nil {
		return nil, fmt.Errorf("zerodha: not connected")
	}
	orders, err := zerodhaops.GetOrders(c)
	if err != nil {
		return nil, err
	}
	out := make([]OrderInfo, len(orders))
	for i, o := range orders {
		out[i] = OrderInfo{
			OrderID: o.OrderID,
			Symbol:  o.Symbol,
			Side:    o.TransactionType,
			Amount:  float64(o.Quantity),
			Rate:    o.Price,
			Status:  o.Status,
		}
	}
	return out, nil
}

func (a *ZerodhaAdapter) ListAccounts() ([]BrokerAccount, error) { return nil, nil }

func (a *ZerodhaAdapter) SelectAccount(accID string) error { return nil }

func (a *ZerodhaAdapter) IsPaper() bool {
	a.mu.RLock()
	defer a.mu.RUnlock()
	return a.paper
}

func (a *ZerodhaAdapter) Buy(symbol string, qty int, limitPrice, stopPrice float64) (string, string, error) {
	a.mu.RLock()
	c := a.client
	a.mu.RUnlock()
	if c == nil {
		return "", "", fmt.Errorf("zerodha: not connected")
	}
	if a.paper {
		return "PAPER-ENTRY", "PAPER-STOP", nil
	}
	var res zerodhaops.OrderResult
	var err error
	if limitPrice > 0 {
		res, err = zerodhaops.BuyLimit(c, symbol, qty, limitPrice)
	} else {
		res, err = zerodhaops.BuyMarket(c, symbol, qty)
	}
	if err != nil {
		return "", "", err
	}
	return res.OrderID, "", nil
}

func (a *ZerodhaAdapter) Sell(symbol string, qty int) (string, error) {
	a.mu.RLock()
	c := a.client
	a.mu.RUnlock()
	if c == nil {
		return "", fmt.Errorf("zerodha: not connected")
	}
	if a.paper {
		return "PAPER-SELL", nil
	}
	res, err := zerodhaops.SellMarket(c, symbol, qty)
	if err != nil {
		return "", err
	}
	return res.OrderID, nil
}

func (a *ZerodhaAdapter) SetPaper(paper bool) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.paper = paper
	if a.client != nil && a.creds != nil {
		c, err := zerodhaclient.NewFromCreds(a.creds["api_key"], a.creds["access_token"], paper)
		if err == nil {
			a.client = c
		}
	}
}
