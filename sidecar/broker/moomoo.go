package broker

import (
	"fmt"
	"strconv"
	"sync"

	mooclient "moomoo-cli/client"
)

// MoomooAdapter wraps moomoo-cli client into the BrokerAdapter interface.
type MoomooAdapter struct {
	mu     sync.RWMutex
	client *mooclient.Client
	paper  bool
	creds  map[string]string
}

// NewMoomooAdapter creates a disconnected Moomoo adapter.
func NewMoomooAdapter() *MoomooAdapter {
	return &MoomooAdapter{paper: true}
}

func (a *MoomooAdapter) ID() string   { return "moomoo" }
func (a *MoomooAdapter) Name() string { return "Moomoo (Futu)" }

func (a *MoomooAdapter) Connected() bool {
	a.mu.RLock()
	defer a.mu.RUnlock()
	return a.client != nil
}

func (a *MoomooAdapter) Connect(creds map[string]string) error {
	a.mu.Lock()
	defer a.mu.Unlock()

	host := creds["host"]
	if host == "" {
		host = "127.0.0.1"
	}
	port, _ := strconv.Atoi(creds["port"])
	if port == 0 {
		port = 11111
	}
	accID, _ := strconv.ParseInt(creds["acc_id"], 10, 64)

	secFirm, _ := strconv.Atoi(creds["security_firm"])
	if secFirm == 0 {
		secFirm = 3 // default: FutuSG
	}
	trdMarket, _ := strconv.Atoi(creds["trd_market"])
	if trdMarket == 0 {
		trdMarket = 6 // default: SG
	}

	cfg := mooclient.Config{
		Host:         host,
		Port:         port,
		TradePass:    creds["trade_password"],
		AccID:        accID,
		SecurityFirm: secFirm,
		TrdMarket:    trdMarket,
	}

	c, err := mooclient.Connect(cfg, a.paper)
	if err != nil {
		return fmt.Errorf("moomoo: %w", err)
	}
	a.client = c
	a.creds = creds
	return nil
}

func (a *MoomooAdapter) ListAccounts() ([]BrokerAccount, error) {
	a.mu.RLock()
	c := a.client
	a.mu.RUnlock()
	if c == nil {
		return nil, fmt.Errorf("moomoo: not connected")
	}
	accounts, err := c.ListAccounts()
	if err != nil {
		return nil, err
	}
	out := make([]BrokerAccount, len(accounts))
	for i, a := range accounts {
		env := "simulate"
		if a.TrdEnv == 1 {
			env = "real"
		}
		var markets []string
		for _, m := range a.Markets {
			markets = append(markets, mooclient.TrdMarketName(m))
		}
		out[i] = BrokerAccount{
			AccID:   fmt.Sprintf("%d", a.AccID),
			Env:     env,
			Type:    a.AccType,
			Markets: markets,
		}
	}
	return out, nil
}

func (a *MoomooAdapter) SelectAccount(accID string) error {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.client == nil {
		return fmt.Errorf("moomoo: not connected")
	}
	accounts, err := a.client.ListAccounts()
	if err != nil {
		return err
	}
	for _, acc := range accounts {
		if fmt.Sprintf("%d", acc.AccID) == accID {
			mkt := 0
			if len(acc.Markets) > 0 {
				mkt = acc.Markets[0]
			}
			a.client.SwitchAccount(acc.AccID, acc.TrdEnv, mkt)
			return nil
		}
	}
	return fmt.Errorf("moomoo: account %s not found", accID)
}

func (a *MoomooAdapter) Test() error {
	a.mu.RLock()
	defer a.mu.RUnlock()
	if a.client == nil {
		return fmt.Errorf("moomoo: not connected")
	}
	_, err := a.client.AccountInfo()
	return err
}

func (a *MoomooAdapter) Disconnect() error {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.client != nil {
		a.client.Close()
		a.client = nil
	}
	return nil
}

func (a *MoomooAdapter) Positions() ([]Position, error) {
	a.mu.RLock()
	c := a.client
	a.mu.RUnlock()
	if c == nil {
		return nil, fmt.Errorf("moomoo: not connected")
	}

	positions, err := c.Positions()
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
			PnL:         p.UnrealPnL,
			PnLPct:      pnlPct,
		}
	}
	return out, nil
}

func (a *MoomooAdapter) Account() (AccountInfo, error) {
	a.mu.RLock()
	c := a.client
	a.mu.RUnlock()
	if c == nil {
		return AccountInfo{}, fmt.Errorf("moomoo: not connected")
	}

	acct, err := c.AccountInfo()
	if err != nil {
		return AccountInfo{}, err
	}

	return AccountInfo{
		Equity:        acct.NetAssets,
		Cash:          acct.Cash,
		TotalInvested: acct.MarketValue,
		TotalPnL:      acct.NetAssets - acct.Cash - acct.MarketValue,
		Available:     acct.BuyingPower,
	}, nil
}

func (a *MoomooAdapter) Orders() ([]OrderInfo, error) {
	a.mu.RLock()
	c := a.client
	a.mu.RUnlock()
	if c == nil {
		return nil, fmt.Errorf("moomoo: not connected")
	}

	orders, err := c.Orders()
	if err != nil {
		return nil, err
	}

	out := make([]OrderInfo, len(orders))
	for i, o := range orders {
		out[i] = OrderInfo{
			OrderID: o.OrderID,
			Symbol:  o.Symbol,
			Side:    o.Side,
			Amount:  float64(o.Qty),
			Rate:    o.Price,
			Status:  o.Status,
		}
	}
	return out, nil
}

func (a *MoomooAdapter) IsPaper() bool {
	a.mu.RLock()
	defer a.mu.RUnlock()
	return a.paper
}

func (a *MoomooAdapter) Buy(symbol string, qty int, limitPrice, stopPrice float64) (string, string, error) {
	a.mu.RLock()
	c := a.client
	a.mu.RUnlock()
	if c == nil {
		return "", "", fmt.Errorf("moomoo: not connected")
	}
	if a.paper {
		return "PAPER-ENTRY", "PAPER-STOP", nil
	}

	orderType := "MKT"
	if limitPrice > 0 {
		orderType = "LMT"
	}
	res, err := c.PlaceOrder(symbol, "BUY", orderType, int64(qty), limitPrice, stopPrice, "DAY")
	if err != nil {
		return "", "", err
	}
	return res.OrderID, "", nil
}

func (a *MoomooAdapter) Sell(symbol string, qty int) (string, error) {
	a.mu.RLock()
	c := a.client
	a.mu.RUnlock()
	if c == nil {
		return "", fmt.Errorf("moomoo: not connected")
	}
	if a.paper {
		return "PAPER-SELL", nil
	}
	res, err := c.PlaceOrder(symbol, "SELL", "MKT", int64(qty), 0, 0, "DAY")
	if err != nil {
		return "", err
	}
	return res.OrderID, nil
}

func (a *MoomooAdapter) SetPaper(paper bool) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.paper = paper
	// Moomoo requires reconnect to change mode.
	if a.client != nil && a.creds != nil {
		a.client.Close()
		a.client = nil
	}
}
