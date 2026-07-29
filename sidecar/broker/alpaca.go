package broker

import (
	"fmt"
	"strconv"
	"sync"

	alpacaclient "alpaca-cli/client"
	alpacaops "alpaca-cli/ops"
)

// AlpacaAdapter wraps alpaca-cli client and ops into the BrokerAdapter interface.
type AlpacaAdapter struct {
	mu     sync.RWMutex
	client *alpacaclient.AlpacaClient
	paper  bool
	creds  map[string]string
}

// NewAlpacaAdapter creates a disconnected Alpaca adapter.
func NewAlpacaAdapter() *AlpacaAdapter {
	return &AlpacaAdapter{paper: true}
}

func (a *AlpacaAdapter) ID() string   { return "alpaca" }
func (a *AlpacaAdapter) Name() string { return "Alpaca Markets" }

func (a *AlpacaAdapter) Connected() bool {
	a.mu.RLock()
	defer a.mu.RUnlock()
	return a.client != nil
}

func (a *AlpacaAdapter) Connect(creds map[string]string) error {
	a.mu.Lock()
	defer a.mu.Unlock()

	keyID := creds["key_id"]
	secretKey := creds["secret_key"]
	if keyID == "" || secretKey == "" {
		return fmt.Errorf("alpaca: key_id and secret_key are required")
	}

	paper := a.paper
	if v, ok := creds["paper"]; ok {
		paper, _ = strconv.ParseBool(v)
	}

	c, err := alpacaclient.NewFromCreds(keyID, secretKey, paper)
	if err != nil {
		return fmt.Errorf("alpaca: %w", err)
	}
	a.client = c
	a.creds = creds
	return nil
}

func (a *AlpacaAdapter) Test() error {
	a.mu.RLock()
	defer a.mu.RUnlock()
	if a.client == nil {
		return fmt.Errorf("alpaca: not connected")
	}
	_, err := alpacaops.GetAccount(a.client)
	return err
}

func (a *AlpacaAdapter) Disconnect() error {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.client = nil
	return nil
}

func (a *AlpacaAdapter) Positions() ([]Position, error) {
	a.mu.RLock()
	c := a.client
	a.mu.RUnlock()
	if c == nil {
		return nil, fmt.Errorf("alpaca: not connected")
	}

	positions, err := alpacaops.GetPositions(c)
	if err != nil {
		return nil, err
	}

	out := make([]Position, len(positions))
	for i, p := range positions {
		out[i] = Position{
			Symbol:      p.Symbol,
			Side:        p.Side,
			Units:       p.Qty,
			Amount:      p.MarketValue,
			OpenRate:    p.AvgEntryPrice,
			CurrentRate: p.CurrentPrice,
			PnL:         p.UnrealizedPL,
			PnLPct:      p.UnrealizedPct * 100,
		}
	}
	return out, nil
}

func (a *AlpacaAdapter) Account() (AccountInfo, error) {
	a.mu.RLock()
	c := a.client
	a.mu.RUnlock()
	if c == nil {
		return AccountInfo{}, fmt.Errorf("alpaca: not connected")
	}

	acct, err := alpacaops.GetAccount(c)
	if err != nil {
		return AccountInfo{}, err
	}

	return AccountInfo{
		Equity:        acct.Equity,
		Cash:          acct.Cash,
		TotalInvested: acct.LongMarketValue,
		TotalPnL:      acct.Equity - acct.Cash - acct.LongMarketValue,
		Available:     acct.BuyingPower,
	}, nil
}

func (a *AlpacaAdapter) Orders() ([]OrderInfo, error) {
	a.mu.RLock()
	c := a.client
	a.mu.RUnlock()
	if c == nil {
		return nil, fmt.Errorf("alpaca: not connected")
	}

	orders, err := alpacaops.GetOrders(c)
	if err != nil {
		return nil, err
	}

	out := make([]OrderInfo, len(orders))
	for i, o := range orders {
		rate := 0.0
		if o.LimitPrice != "" {
			rate, _ = strconv.ParseFloat(o.LimitPrice, 64)
		}
		qty := 0.0
		if o.Qty != "" {
			qty, _ = strconv.ParseFloat(o.Qty, 64)
		}
		out[i] = OrderInfo{
			OrderID: o.ID,
			Symbol:  o.Symbol,
			Side:    o.Side,
			Amount:  qty,
			Rate:    rate,
			Status:  o.Status,
		}
	}
	return out, nil
}

func (a *AlpacaAdapter) ListAccounts() ([]BrokerAccount, error) { return nil, nil }

func (a *AlpacaAdapter) SelectAccount(accID string) error { return nil }

func (a *AlpacaAdapter) IsPaper() bool {
	a.mu.RLock()
	defer a.mu.RUnlock()
	return a.paper
}

func (a *AlpacaAdapter) Buy(symbol string, qty int, limitPrice, stopPrice float64) (string, string, error) {
	a.mu.RLock()
	c := a.client
	a.mu.RUnlock()
	if c == nil {
		return "", "", fmt.Errorf("alpaca: not connected")
	}
	if a.paper {
		return "PAPER-ENTRY", "PAPER-STOP", nil
	}

	if stopPrice > 0 {
		res, err := alpacaops.BuyWithStops(c, symbol, qty, limitPrice, stopPrice, 0)
		if err != nil {
			return "", "", err
		}
		return res.OrderID, "", nil
	}

	var res alpacaops.OrderResult
	var err error
	if limitPrice > 0 {
		res, err = alpacaops.BuyLimit(c, symbol, qty, limitPrice)
	} else {
		res, err = alpacaops.BuyMarket(c, symbol, qty)
	}
	if err != nil {
		return "", "", err
	}
	return res.OrderID, "", nil
}

func (a *AlpacaAdapter) Sell(symbol string, qty int) (string, error) {
	a.mu.RLock()
	c := a.client
	a.mu.RUnlock()
	if c == nil {
		return "", fmt.Errorf("alpaca: not connected")
	}
	if a.paper {
		return "PAPER-SELL", nil
	}
	res, err := alpacaops.SellMarket(c, symbol, qty)
	if err != nil {
		return "", err
	}
	return res.OrderID, nil
}

func (a *AlpacaAdapter) SetPaper(paper bool) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.paper = paper
	if a.client != nil && a.creds != nil {
		c, err := alpacaclient.NewFromCreds(a.creds["key_id"], a.creds["secret_key"], paper)
		if err == nil {
			a.client = c
		}
	}
}
