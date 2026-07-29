package broker

import (
	"fmt"
	"sync"

	etoroclient "etoro-cli/client"
	etoroops "etoro-cli/ops"
)

// EtoroAdapter wraps etoro-cli client and ops into the BrokerAdapter interface.
type EtoroAdapter struct {
	mu     sync.RWMutex
	client *etoroclient.EtoroClient
	paper  bool
	creds  map[string]string
}

// NewEtoroAdapter creates a disconnected eToro adapter.
func NewEtoroAdapter() *EtoroAdapter {
	return &EtoroAdapter{paper: true}
}

func (a *EtoroAdapter) ID() string   { return "etoro" }
func (a *EtoroAdapter) Name() string { return "eToro" }

func (a *EtoroAdapter) Connected() bool {
	a.mu.RLock()
	defer a.mu.RUnlock()
	return a.client != nil
}

func (a *EtoroAdapter) Connect(creds map[string]string) error {
	a.mu.Lock()
	defer a.mu.Unlock()

	apiKey := creds["api_key"]
	userKey := creds["user_key"]

	if apiKey == "" || userKey == "" {
		return fmt.Errorf("etoro: api_key and user_key are required")
	}

	c, err := etoroclient.NewFromCreds(apiKey, userKey, a.paper)
	if err != nil {
		return fmt.Errorf("etoro: connect: %w", err)
	}
	a.client = c
	a.creds = creds
	return nil
}

func (a *EtoroAdapter) Test() error {
	a.mu.RLock()
	c := a.client
	a.mu.RUnlock()
	if c == nil {
		return fmt.Errorf("etoro: not connected")
	}
	_, err := etoroops.GetAccount(c, "USD")
	return err
}

func (a *EtoroAdapter) Disconnect() error {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.client = nil
	return nil
}

func (a *EtoroAdapter) Positions() ([]Position, error) {
	a.mu.RLock()
	c := a.client
	a.mu.RUnlock()
	if c == nil {
		return nil, fmt.Errorf("etoro: not connected")
	}

	positions, err := etoroops.GetPositions(c)
	if err != nil {
		return nil, err
	}

	out := make([]Position, len(positions))
	for i, p := range positions {
		side := "BUY"
		if !p.IsBuy {
			side = "SELL"
		}
		out[i] = Position{
			Symbol:      p.Symbol,
			Side:        side,
			Units:       p.Units,
			Amount:      p.Amount,
			OpenRate:    p.OpenRate,
			CurrentRate: p.CurrentRate,
			PnL:         p.PnL,
			PnLPct:      p.PnLPct,
			StopLoss:    p.StopLoss,
			TakeProfit:  p.TakeProfit,
		}
	}
	return out, nil
}

func (a *EtoroAdapter) Account() (AccountInfo, error) {
	a.mu.RLock()
	c := a.client
	a.mu.RUnlock()
	if c == nil {
		return AccountInfo{}, fmt.Errorf("etoro: not connected")
	}

	acct, err := etoroops.GetAccount(c, "USD")
	if err != nil {
		return AccountInfo{}, err
	}

	return AccountInfo{
		Equity:        acct.Equity,
		Cash:          acct.Cash,
		TotalInvested: acct.TotalInvested,
		TotalPnL:      acct.TotalPnL,
		Available:     acct.AvailableBalance,
	}, nil
}

func (a *EtoroAdapter) Orders() ([]OrderInfo, error) {
	a.mu.RLock()
	c := a.client
	a.mu.RUnlock()
	if c == nil {
		return nil, fmt.Errorf("etoro: not connected")
	}

	orders, err := etoroops.GetOrders(c)
	if err != nil {
		return nil, err
	}

	out := make([]OrderInfo, len(orders))
	for i, o := range orders {
		side := "BUY"
		if !o.IsBuy {
			side = "SELL"
		}
		out[i] = OrderInfo{
			OrderID: o.OrderID,
			Symbol:  o.Symbol,
			Side:    side,
			Amount:  o.Amount,
			Rate:    o.Rate,
			Status:  o.Status,
		}
	}
	return out, nil
}

func (a *EtoroAdapter) ListAccounts() ([]BrokerAccount, error) { return nil, nil }

func (a *EtoroAdapter) SelectAccount(accID string) error { return nil }

func (a *EtoroAdapter) IsPaper() bool {
	a.mu.RLock()
	defer a.mu.RUnlock()
	return a.paper
}

func (a *EtoroAdapter) Buy(symbol string, qty int, limitPrice, stopPrice float64) (string, string, error) {
	a.mu.RLock()
	c := a.client
	a.mu.RUnlock()
	if c == nil {
		return "", "", fmt.Errorf("etoro: not connected")
	}
	if a.paper {
		return "PAPER-ENTRY", "PAPER-STOP", nil
	}

	res, err := etoroops.BuyWithStops(c, symbol, float64(qty), limitPrice, stopPrice, 0)
	if err != nil {
		return "", "", err
	}
	return res.OrderID, "", nil
}

func (a *EtoroAdapter) Sell(symbol string, qty int) (string, error) {
	a.mu.RLock()
	c := a.client
	a.mu.RUnlock()
	if c == nil {
		return "", fmt.Errorf("etoro: not connected")
	}
	if a.paper {
		return "PAPER-SELL", nil
	}
	results, err := etoroops.SellBySymbol(c, symbol, float64(qty))
	if err != nil {
		return "", err
	}
	if len(results) > 0 {
		return results[0].PositionID, nil
	}
	return "", nil
}

func (a *EtoroAdapter) SetPaper(paper bool) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.paper = paper
	// Reconnect with new mode if connected.
	if a.client != nil && a.creds != nil {
		c, err := etoroclient.NewFromCreds(a.creds["api_key"], a.creds["user_key"], paper)
		if err == nil {
			a.client = c
		}
	}
}
