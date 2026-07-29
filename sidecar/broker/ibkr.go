package broker

import (
	"fmt"
	"sync"

	ibkrclient "ibkr-cli/client"
	ibkrops "ibkr-cli/ops"
)

type IBKRAdapter struct {
	mu     sync.RWMutex
	client *ibkrclient.IBKRClient
	paper  bool
	creds  map[string]string
}

func NewIBKRAdapter() *IBKRAdapter {
	return &IBKRAdapter{paper: true}
}

func (a *IBKRAdapter) ID() string   { return "ibkr" }
func (a *IBKRAdapter) Name() string { return "Interactive Brokers" }

func (a *IBKRAdapter) Connected() bool {
	a.mu.RLock()
	defer a.mu.RUnlock()
	return a.client != nil
}

func (a *IBKRAdapter) Connect(creds map[string]string) error {
	a.mu.Lock()
	defer a.mu.Unlock()

	host := creds["host"]
	if host == "" {
		host = "localhost"
	}
	port := creds["port"]
	if port == "" {
		port = "5000"
	}
	accountID := creds["account_id"]

	c, err := ibkrclient.NewFromCreds(host, port, accountID, a.paper)
	if err != nil {
		return fmt.Errorf("ibkr: %w", err)
	}
	a.client = c
	a.creds = creds
	return nil
}

func (a *IBKRAdapter) Test() error {
	a.mu.RLock()
	defer a.mu.RUnlock()
	if a.client == nil {
		return fmt.Errorf("ibkr: not connected")
	}
	_, err := ibkrops.GetAccount(a.client)
	return err
}

func (a *IBKRAdapter) Disconnect() error {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.client = nil
	return nil
}

func (a *IBKRAdapter) Positions() ([]Position, error) {
	a.mu.RLock()
	c := a.client
	a.mu.RUnlock()
	if c == nil {
		return nil, fmt.Errorf("ibkr: not connected")
	}
	positions, err := ibkrops.GetPositions(c)
	if err != nil {
		return nil, err
	}
	out := make([]Position, len(positions))
	for i, p := range positions {
		side := "BUY"
		if p.Qty < 0 {
			side = "SELL"
		}
		qty := p.Qty
		if qty < 0 {
			qty = -qty
		}
		out[i] = Position{
			Symbol:      p.Symbol,
			Side:        side,
			Units:       qty,
			Amount:      p.MktValue,
			OpenRate:    p.AvgCost,
			CurrentRate: p.MktPrice,
			PnL:         p.UnrealizedPL,
		}
	}
	return out, nil
}

func (a *IBKRAdapter) Account() (AccountInfo, error) {
	a.mu.RLock()
	c := a.client
	a.mu.RUnlock()
	if c == nil {
		return AccountInfo{}, fmt.Errorf("ibkr: not connected")
	}
	acct, err := ibkrops.GetAccount(c)
	if err != nil {
		return AccountInfo{}, err
	}
	return AccountInfo{
		Equity:        acct.NetLiquidation,
		Cash:          acct.Cash,
		TotalInvested: acct.GrossPosition,
		TotalPnL:      acct.NetLiquidation - acct.Cash - acct.GrossPosition,
		Available:     acct.BuyingPower,
	}, nil
}

func (a *IBKRAdapter) Orders() ([]OrderInfo, error) {
	a.mu.RLock()
	c := a.client
	a.mu.RUnlock()
	if c == nil {
		return nil, fmt.Errorf("ibkr: not connected")
	}
	orders, err := ibkrops.GetOrders(c)
	if err != nil {
		return nil, err
	}
	out := make([]OrderInfo, len(orders))
	for i, o := range orders {
		out[i] = OrderInfo{
			OrderID: o.OrderID,
			Symbol:  o.Symbol,
			Side:    o.Side,
			Amount:  o.Qty,
			Rate:    o.Price,
			Status:  o.Status,
		}
	}
	return out, nil
}

func (a *IBKRAdapter) ListAccounts() ([]BrokerAccount, error) { return nil, nil }

func (a *IBKRAdapter) SelectAccount(accID string) error { return nil }

func (a *IBKRAdapter) IsPaper() bool {
	a.mu.RLock()
	defer a.mu.RUnlock()
	return a.paper
}

func (a *IBKRAdapter) Buy(symbol string, qty int, limitPrice, stopPrice float64) (string, string, error) {
	a.mu.RLock()
	c := a.client
	a.mu.RUnlock()
	if c == nil {
		return "", "", fmt.Errorf("ibkr: not connected")
	}
	if a.paper {
		return "PAPER-ENTRY", "PAPER-STOP", nil
	}
	var res ibkrops.OrderResult
	var err error
	if limitPrice > 0 {
		res, err = ibkrops.BuyLimit(c, symbol, qty, limitPrice)
	} else {
		res, err = ibkrops.BuyMarket(c, symbol, qty)
	}
	if err != nil {
		return "", "", err
	}
	return res.OrderID, "", nil
}

func (a *IBKRAdapter) Sell(symbol string, qty int) (string, error) {
	a.mu.RLock()
	c := a.client
	a.mu.RUnlock()
	if c == nil {
		return "", fmt.Errorf("ibkr: not connected")
	}
	if a.paper {
		return "PAPER-SELL", nil
	}
	res, err := ibkrops.SellMarket(c, symbol, qty)
	if err != nil {
		return "", err
	}
	return res.OrderID, nil
}

func (a *IBKRAdapter) SetPaper(paper bool) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.paper = paper
	if a.client != nil && a.creds != nil {
		host := a.creds["host"]
		if host == "" {
			host = "localhost"
		}
		port := a.creds["port"]
		if port == "" {
			port = "5000"
		}
		c, err := ibkrclient.NewFromCreds(host, port, a.creds["account_id"], paper)
		if err == nil {
			a.client = c
		}
	}
}
