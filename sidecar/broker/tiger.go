package broker

import (
	"fmt"
	"sync"

	tigerclient "tiger-cli/client"
	tigerops "tiger-cli/ops"
)

// TigerAdapter wraps tiger-cli client and ops into the BrokerAdapter interface.
type TigerAdapter struct {
	mu     sync.RWMutex
	client *tigerclient.TigerClient
	paper  bool
	creds  map[string]string
}

// NewTigerAdapter creates a disconnected Tiger adapter.
func NewTigerAdapter() *TigerAdapter {
	return &TigerAdapter{paper: true}
}

func (a *TigerAdapter) ID() string   { return "tiger" }
func (a *TigerAdapter) Name() string { return "Tiger Brokers" }

func (a *TigerAdapter) Connected() bool {
	a.mu.RLock()
	defer a.mu.RUnlock()
	return a.client != nil
}

func (a *TigerAdapter) Connect(creds map[string]string) error {
	a.mu.Lock()
	defer a.mu.Unlock()

	tigerID := creds["tiger_id"]
	account := creds["account"]
	privateKey := creds["private_key_pk8"]
	tradePass := creds["trade_password"]

	if tigerID == "" || account == "" || privateKey == "" {
		return fmt.Errorf("tiger: tiger_id, account, and private_key_pk8 are required")
	}

	c, err := tigerclient.NewFromCreds(tigerID, account, privateKey, tradePass, a.paper)
	if err != nil {
		return fmt.Errorf("tiger: connect: %w", err)
	}
	a.client = c
	a.creds = creds
	return nil
}

func (a *TigerAdapter) Test() error {
	a.mu.RLock()
	defer a.mu.RUnlock()
	if a.client == nil {
		return fmt.Errorf("tiger: not connected")
	}
	_, err := tigerops.GetAccount(a.client)
	return err
}

func (a *TigerAdapter) Disconnect() error {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.client = nil
	return nil
}

func (a *TigerAdapter) Positions() ([]Position, error) {
	a.mu.RLock()
	c := a.client
	a.mu.RUnlock()
	if c == nil {
		return nil, fmt.Errorf("tiger: not connected")
	}

	positions, err := tigerops.GetPositions(c)
	if err != nil {
		return nil, err
	}

	out := make([]Position, len(positions))
	for i, p := range positions {
		side := "BUY"
		if p.Shares < 0 {
			side = "SELL"
		}
		var pnlPct float64
		if p.AvgCost > 0 && p.Shares != 0 {
			pnlPct = (p.MarketPrice - p.AvgCost) / p.AvgCost * 100
		}
		out[i] = Position{
			Symbol:      p.Symbol,
			Side:        side,
			Units:       float64(p.Shares),
			Amount:      p.MarketValue,
			OpenRate:    p.AvgCost,
			CurrentRate: p.MarketPrice,
			PnL:         p.UnrealizedPnL,
			PnLPct:      pnlPct,
		}
	}
	return out, nil
}

func (a *TigerAdapter) Account() (AccountInfo, error) {
	a.mu.RLock()
	c := a.client
	a.mu.RUnlock()
	if c == nil {
		return AccountInfo{}, fmt.Errorf("tiger: not connected")
	}

	acct, err := tigerops.GetAccount(c)
	if err != nil {
		return AccountInfo{}, err
	}

	return AccountInfo{
		Equity:        acct.NetLiquidation,
		Cash:          acct.Cash,
		TotalInvested: acct.GrossPositionValue,
		TotalPnL:      acct.NetLiquidation - acct.Cash - acct.GrossPositionValue,
		Available:     acct.BuyingPower,
	}, nil
}

func (a *TigerAdapter) Orders() ([]OrderInfo, error) {
	a.mu.RLock()
	c := a.client
	a.mu.RUnlock()
	if c == nil {
		return nil, fmt.Errorf("tiger: not connected")
	}

	orders, err := tigerops.GetOrders(c)
	if err != nil {
		return nil, err
	}

	out := make([]OrderInfo, len(orders))
	for i, o := range orders {
		out[i] = OrderInfo{
			OrderID: o.ID,
			Symbol:  o.Symbol,
			Side:    o.Action,
			Amount:  float64(o.Quantity),
			Rate:    o.LimitPrice,
			Status:  o.Status,
		}
	}
	return out, nil
}

func (a *TigerAdapter) ListAccounts() ([]BrokerAccount, error) { return nil, nil }

func (a *TigerAdapter) SelectAccount(accID string) error { return nil }

func (a *TigerAdapter) IsPaper() bool {
	a.mu.RLock()
	defer a.mu.RUnlock()
	return a.paper
}

func (a *TigerAdapter) Buy(symbol string, qty int, limitPrice, stopPrice float64) (string, string, error) {
	a.mu.RLock()
	c := a.client
	a.mu.RUnlock()
	if c == nil {
		return "", "", fmt.Errorf("tiger: not connected")
	}
	if a.paper {
		return "PAPER-ENTRY", "PAPER-STOP", nil
	}

	var entry tigerops.OrderResult
	var err error
	if limitPrice > 0 {
		entry, err = tigerops.BuyLimit(c, symbol, qty, limitPrice)
	} else {
		entry, err = tigerops.BuyMarket(c, symbol, qty)
	}
	if err != nil {
		return "", "", err
	}

	var stopID string
	if stopPrice > 0 {
		stop, err := tigerops.SetStopLoss(c, symbol, qty, stopPrice)
		if err != nil {
			return entry.OrderID, "", fmt.Errorf("entry placed (%s) but stop failed: %w", entry.OrderID, err)
		}
		stopID = stop.OrderID
	}
	return entry.OrderID, stopID, nil
}

func (a *TigerAdapter) Sell(symbol string, qty int) (string, error) {
	a.mu.RLock()
	c := a.client
	a.mu.RUnlock()
	if c == nil {
		return "", fmt.Errorf("tiger: not connected")
	}
	if a.paper {
		return "PAPER-SELL", nil
	}
	res, err := tigerops.SellMarket(c, symbol, qty)
	if err != nil {
		return "", err
	}
	return res.OrderID, nil
}

func (a *TigerAdapter) SetPaper(paper bool) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.paper = paper
	// Reconnect with new mode if currently connected.
	if a.client != nil && a.creds != nil {
		c, err := tigerclient.NewFromCreds(
			a.creds["tiger_id"], a.creds["account"],
			a.creds["private_key_pk8"], a.creds["trade_password"], paper,
		)
		if err == nil {
			a.client = c
		}
	}
}
