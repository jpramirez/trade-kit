// Package client implements a minimal Futu OpenD TCP client in pure Go.
//
// OpenD listens on localhost:11111 by default. Messages use a 44-byte header
// followed by a JSON body (proto_fmt_type=1). No protobuf dependency needed.
//
// Packet layout (from Futu SDK MESSAGE_HEAD_FMT = "<1s1sI2B2I20s8s"):
//   [0:1]   'F'
//   [1:2]   'T'
//   [2:6]   proto_id       (uint32 LE)
//   [6]     proto_fmt_type (uint8) — 0=protobuf, 1=JSON
//   [7]     proto_ver      (uint8)
//   [8:12]  serial_no      (uint32 LE)
//   [12:16] body_len       (uint32 LE)
//   [16:36] sha1           (20 bytes) — zeros accepted by OpenD
//   [36:44] reserved       (8 bytes)
//   [44:]   body
package client

import (
	"crypto/md5"
	"crypto/sha1"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"net"
	"os"
	"strconv"
	"strings"
	"sync/atomic"
	"time"
)

// Proto IDs
const (
	protoInitConnect       = 1001
	protoGetAccList        = 2001
	protoUnlockTrade       = 2005
	protoGetPositionList   = 2102
	protoGetAccInfo        = 2101
	protoGetOrderList      = 2111
	protoPlaceOrder        = 2202
	protoModifyOrder       = 2205
)

const (
	headerSize       = 44
	bodyTypeJSON byte = 1 // proto_fmt_type: 0=protobuf, 1=JSON
	trdEnvReal       = 1

	// SecurityFirm enum (from Trd_Common.proto)
	secFirmUnknown = 0
	secFirmFutuHK  = 1
	secFirmFutuUS  = 2
	secFirmFutuSG  = 3
	secFirmFutuAU  = 4
	secFirmFutuCA  = 5
	secFirmFutuMY  = 6
	secFirmFutuJP  = 7

	// TrdMarket enum (from Trd_Common.proto)
	trdMarketHK = 1
	trdMarketUS = 2
	trdMarketCN = 3
	trdMarketSG = 6
)

var magic = [2]byte{0x46, 0x54} // "FT"

// Client is a connected, authenticated Futu OpenD session.
type Client struct {
	conn         net.Conn
	paper        bool
	accID        int64
	trdEnv       int // 0=simulate, 1=real (matches the discovered account)
	connID       uint64
	userID       uint64
	securityFirm int
	trdMarket    int
	serial       uint32
}

// Config holds OpenD connection parameters loaded from environment / .env file.
type Config struct {
	Host         string
	Port         int
	TradePass    string // 6-digit PIN
	AccID        int64
	SecurityFirm int // 0=unknown, 1=HK, 2=US, 3=SG, 4=AU, 5=CA, 6=MY, 7=JP
	TrdMarket    int // 1=HK, 2=US, 3=CN, 6=SG (default: SG)
}

// LoadConfig reads MOOMOO_HOST, MOOMOO_PORT, TRADE_PASSWORD, ACC_ID
// from the environment (or a .env file next to the binary).
func LoadConfig() Config {
	loadEnvFile()
	host := getenv("MOOMOO_HOST", "127.0.0.1")
	port, _ := strconv.Atoi(getenv("MOOMOO_PORT", "11111"))
	if port == 0 {
		port = 11111
	}
	accID, _ := strconv.ParseInt(getenv("ACC_ID", "0"), 10, 64)
	secFirm, _ := strconv.Atoi(getenv("SECURITY_FIRM", "3")) // default FutuSG
	trdMkt, _ := strconv.Atoi(getenv("TRD_MARKET", "6"))     // default SG
	return Config{
		Host:         host,
		Port:         port,
		TradePass:    getenv("TRADE_PASSWORD", ""),
		AccID:        accID,
		SecurityFirm: secFirm,
		TrdMarket:    trdMkt,
	}
}

// Connect establishes a TCP connection to OpenD, initialises the session,
// discovers the account ID if not configured, and unlocks trading.
func Connect(cfg Config, paper bool) (*Client, error) {
	addr := fmt.Sprintf("%s:%d", cfg.Host, cfg.Port)
	conn, err := net.DialTimeout("tcp", addr, 5*time.Second)
	if err != nil {
		return nil, fmt.Errorf("OpenD not running on %s — start OpenD first: %w", addr, err)
	}
	trdMkt := cfg.TrdMarket
	if trdMkt == 0 {
		trdMkt = trdMarketSG
	}
	c := &Client{conn: conn, paper: paper, securityFirm: cfg.SecurityFirm, trdMarket: trdMkt}

	// 1. InitConnect
	if err := c.initConnect(); err != nil {
		conn.Close()
		return nil, fmt.Errorf("init: %w", err)
	}

	// 2. Discover account ID
	accID := cfg.AccID
	if accID == 0 {
		accID, err = c.discoverAccID()
		if err != nil {
			conn.Close()
			return nil, fmt.Errorf("get_acc_list: %w", err)
		}
	}
	c.accID = accID

	// 3. Unlock trading (required before any trade operation)
	// OpenD needs a moment after InitConnect before it accepts unlock requests.
	if cfg.TradePass != "" {
		time.Sleep(500 * time.Millisecond)
		var unlockErr error
		for attempt := 0; attempt < 3; attempt++ {
			unlockErr = c.unlockTrade(cfg.TradePass)
			if unlockErr == nil {
				break
			}
			if strings.Contains(unlockErr.Error(), "not ready") {
				time.Sleep(2 * time.Second)
				continue
			}
			break
		}
		if unlockErr != nil {
			conn.Close()
			return nil, fmt.Errorf("unlock_trade: %w", unlockErr)
		}
	}

	return c, nil
}

// Close shuts down the connection.
func (c *Client) Close() error { return c.conn.Close() }

// IsPaper returns true when in simulation mode.
func (c *Client) IsPaper() bool { return c.paper }

// AccID returns the resolved account ID.
func (c *Client) AccID() int64 { return c.accID }

// TrdMarketName returns a human-readable name for a TrdMarket enum value.
func TrdMarketName(m int) string {
	switch m {
	case 1: return "HK"
	case 2: return "US"
	case 3: return "CN"
	case 4: return "HKCC"
	case 5: return "Futures"
	case 6: return "SG"
	case 8: return "AU"
	case 15: return "JP"
	case 111: return "MY"
	case 112: return "CA"
	default: return fmt.Sprintf("Market_%d", m)
	}
}

// AccountEntry represents one account from GetAccList.
type AccountEntry struct {
	AccID     int64
	TrdEnv    int    // 0=simulate, 1=real
	AccType   int
	Markets   []int
}

// ListAccounts returns all accounts available in the current OpenD session.
func (c *Client) ListAccounts() ([]AccountEntry, error) {
	req := map[string]any{"c2s": map[string]any{"userID": 0, "trdCategory": 1, "needGeneralSecAccount": true}}
	raw, err := c.call(protoGetAccList, req)
	if err != nil {
		return nil, err
	}
	var resp struct {
		S2C struct {
			AccList []struct {
				AccID             json.Number `json:"accID"`
				TrdEnv            int         `json:"trdEnv"`
				AccType           int         `json:"accType"`
				TrdMarketAuthList []int       `json:"trdMarketAuthList"`
			} `json:"accList"`
		} `json:"s2c"`
	}
	if err := json.Unmarshal(raw, &resp); err != nil {
		return nil, fmt.Errorf("parse acc_list: %w", err)
	}
	var out []AccountEntry
	for _, a := range resp.S2C.AccList {
		id, _ := a.AccID.Int64()
		out = append(out, AccountEntry{
			AccID:   id,
			TrdEnv:  a.TrdEnv,
			AccType: a.AccType,
			Markets: a.TrdMarketAuthList,
		})
	}
	return out, nil
}

// SwitchAccount changes the active account and market for subsequent requests.
func (c *Client) SwitchAccount(accID int64, trdEnv, trdMarket int) {
	c.accID = accID
	c.trdEnv = trdEnv
	c.trdMarket = trdMarket
}

// ─── Public API ──────────────────────────────────────────────────────────────

type Position struct {
	Symbol       string
	Shares       int64
	AvgCost      float64
	MarketPrice  float64
	MarketValue  float64
	UnrealPnL    float64
	UnrealPct    float64
}

type AccountInfo struct {
	NetAssets   float64
	Cash        float64
	MarketValue float64
	BuyingPower float64
	Currency    string
}

type Order struct {
	OrderID  string
	Symbol   string
	Side     string
	Type     string
	Qty      int64
	Filled   int64
	Price    float64
	AuxPrice float64
	TIF      string
	Status   string
}

type OrderResult struct {
	OrderID string
	Status  string
	Symbol  string
	Side    string
	Qty     int64
	Price   float64
}

func (c *Client) Positions() ([]Position, error) {
	req := map[string]any{
		"c2s": map[string]any{
			"header": c.trdHeader(),
		},
	}
	raw, err := c.call(protoGetPositionList, req)
	if err != nil {
		return nil, err
	}
	var resp struct {
		S2C struct {
			PositionList []struct {
				Code         string  `json:"code"`
				Qty          int64   `json:"qty"`
				CostPrice    float64 `json:"costPrice"`
				NominalPrice float64 `json:"nominalPrice"`
				MarketVal    float64 `json:"marketVal"`
				PlVal        float64 `json:"plVal"`
				PlRatio      float64 `json:"plRatio"`
			} `json:"positionList"`
		} `json:"s2c"`
	}
	if err := json.Unmarshal(raw, &resp); err != nil {
		return nil, fmt.Errorf("parse positions: %w", err)
	}
	var out []Position
	for _, p := range resp.S2C.PositionList {
		out = append(out, Position{
			Symbol:      p.Code,
			Shares:      p.Qty,
			AvgCost:     p.CostPrice,
			MarketPrice: p.NominalPrice,
			MarketValue: p.MarketVal,
			UnrealPnL:   p.PlVal,
			UnrealPct:   p.PlRatio * 100,
		})
	}
	return out, nil
}

func (c *Client) AccountInfo() (AccountInfo, error) {
	req := map[string]any{
		"c2s": map[string]any{
			"header":   c.trdHeader(),
			"currency": 1, // Trd_Common.Currency_HKD=1, will return in account's base currency
		},
	}
	raw, err := c.call(protoGetAccInfo, req)
	if err != nil {
		return AccountInfo{}, err
	}
	var resp struct {
		S2C struct {
			Funds struct {
				TotalAssets float64     `json:"totalAssets"`
				Cash        float64     `json:"cash"`
				MarketVal   float64     `json:"marketVal"`
				Power       float64     `json:"power"`
				Currency    json.Number `json:"currency"`
			} `json:"funds"`
		} `json:"s2c"`
	}
	if err := json.Unmarshal(raw, &resp); err != nil {
		return AccountInfo{}, fmt.Errorf("parse account: %w", err)
	}
	fi := resp.S2C.Funds
	return AccountInfo{
		NetAssets:   fi.TotalAssets,
		Cash:        fi.Cash,
		MarketValue: fi.MarketVal,
		BuyingPower: fi.Power,
		Currency:    fi.Currency.String(),
	}, nil
}

func (c *Client) Orders() ([]Order, error) {
	req := map[string]any{
		"c2s": map[string]any{
			"header":           c.trdHeader(),
			"filterStatusList": []int{1, 2, 3, 5, 11}, // submitting,submitted,part-filled,waiting,part-cancel
		},
	}
	raw, err := c.call(protoGetOrderList, req)
	if err != nil {
		return nil, err
	}
	var resp struct {
		S2C struct {
			OrderList []struct {
				OrderID    string  `json:"orderID"`
				Code       string  `json:"code"`
				TrdSide    int     `json:"trdSide"`
				OrderType  int     `json:"orderType"`
				Qty        int64   `json:"qty"`
				DealtQty   int64   `json:"dealtQty"`
				Price      float64 `json:"price"`
				AuxPrice   float64 `json:"auxPrice"`
				TimeInForce int    `json:"timeInForce"`
				OrderStatus int   `json:"orderStatus"`
			} `json:"orderList"`
		} `json:"s2c"`
	}
	if err := json.Unmarshal(raw, &resp); err != nil {
		return nil, fmt.Errorf("parse orders: %w", err)
	}
	var out []Order
	for _, o := range resp.S2C.OrderList {
		out = append(out, Order{
			OrderID:  o.OrderID,
			Symbol:   o.Code,
			Side:     sideStr(o.TrdSide),
			Type:     orderTypeStr(o.OrderType),
			Qty:      o.Qty,
			Filled:   o.DealtQty,
			Price:    o.Price,
			AuxPrice: o.AuxPrice,
			TIF:      tifStr(o.TimeInForce),
			Status:   orderStatusStr(o.OrderStatus),
		})
	}
	return out, nil
}

func (c *Client) PlaceOrder(symbol, side, orderType string, qty int64, price, auxPrice float64, tif string) (OrderResult, error) {
	if c.paper {
		return OrderResult{OrderID: "PAPER", Status: "PAPER", Symbol: symbol, Side: side, Qty: qty, Price: price}, nil
	}
	req := map[string]any{
		"c2s": map[string]any{
			"header":    c.trdHeader(),
			"trdSide":   sideInt(side),
			"orderType": orderTypeInt(orderType),
			"code":      symbol,
			"qty":       qty,
			"price":     price,
			"auxPrice":  auxPrice,
			"timeInForce": tifInt(tif),
		},
	}
	raw, err := c.call(protoPlaceOrder, req)
	if err != nil {
		return OrderResult{}, fmt.Errorf("place_order %s: %w", symbol, err)
	}
	var resp struct {
		S2C struct {
			OrderID string `json:"orderID"`
		} `json:"s2c"`
	}
	if err := json.Unmarshal(raw, &resp); err != nil {
		return OrderResult{}, fmt.Errorf("parse place_order: %w", err)
	}
	return OrderResult{OrderID: resp.S2C.OrderID, Status: "SUBMITTED", Symbol: symbol, Side: side, Qty: qty, Price: price}, nil
}

func (c *Client) ModifyOrder(orderID string, qty int64, price, auxPrice float64, cancel bool) error {
	modifyOp := 1 // NORMAL modify
	if cancel {
		modifyOp = 2 // CANCEL
	}
	req := map[string]any{
		"c2s": map[string]any{
			"header":         c.trdHeader(),
			"orderID":        orderID,
			"modifyOrderOp":  modifyOp,
			"qty":            qty,
			"price":          price,
			"auxPrice":       auxPrice,
		},
	}
	_, err := c.call(protoModifyOrder, req)
	return err
}

// ─── Internal ────────────────────────────────────────────────────────────────

func (c *Client) trdHeader() map[string]any {
	mkt := c.trdMarket
	if mkt == 0 {
		mkt = trdMarketSG
	}
	return map[string]any{
		"trdEnv":    c.trdEnv, // matches the discovered account's environment
		"accID":     c.accID,
		"trdMarket": mkt,
	}
}

func (c *Client) initConnect() error {
	req := map[string]any{
		"c2s": map[string]any{
			"clientVer":           300,
			"clientID":            "moomoo-cli-go",
			"recvNotify":          false,
			"programmingLanguage": "Go",
		},
	}
	raw, err := c.call(protoInitConnect, req)
	if err != nil {
		return err
	}
	// Parse InitConnect response to get connID and loginUserID.
	var resp struct {
		S2C struct {
			ConnID  json.Number `json:"connID"`
			UserID  json.Number `json:"loginUserID"`
		} `json:"s2c"`
	}
	if json.Unmarshal(raw, &resp) == nil {
		cid, _ := resp.S2C.ConnID.Int64()
		c.connID = uint64(cid)
		uid, _ := resp.S2C.UserID.Int64()
		c.userID = uint64(uid)
	}
	return nil
}

func (c *Client) discoverAccID() (int64, error) {
	req := map[string]any{"c2s": map[string]any{"userID": 0, "trdCategory": 1, "needGeneralSecAccount": true}}
	raw, err := c.call(protoGetAccList, req)
	if err != nil {
		return 0, err
	}
	var resp struct {
		S2C struct {
			AccList []struct {
				AccID             json.Number `json:"accID"`
				TrdEnv            int         `json:"trdEnv"`
				AccType           int         `json:"accType"`
				TrdMarketAuthList []int       `json:"trdMarketAuthList"`
			} `json:"accList"`
		} `json:"s2c"`
	}
	if err := json.Unmarshal(raw, &resp); err != nil {
		return 0, fmt.Errorf("parse acc_list: %w", err)
	}
	// Pick account matching paper/live mode
	// trdEnv: 0=simulate, 1=real
	wantEnv := trdEnvReal
	if c.paper {
		wantEnv = 0 // simulate
	}

	// First pass: match trdEnv + requested trdMarket
	for _, a := range resp.S2C.AccList {
		id, _ := a.AccID.Int64()
		if a.TrdEnv == wantEnv && id > 0 {
			for _, mkt := range a.TrdMarketAuthList {
				if mkt == c.trdMarket {
					c.trdEnv = a.TrdEnv
					return id, nil
				}
			}
		}
	}
	// Second pass: match trdEnv, use the account's own market
	for _, a := range resp.S2C.AccList {
		id, _ := a.AccID.Int64()
		if a.TrdEnv == wantEnv && id > 0 {
			c.trdEnv = a.TrdEnv
			if len(a.TrdMarketAuthList) > 0 {
				c.trdMarket = a.TrdMarketAuthList[0]
			}
			return id, nil
		}
	}
	// Third pass: any account — use its market
	for _, a := range resp.S2C.AccList {
		id, _ := a.AccID.Int64()
		if id > 0 {
			c.trdEnv = a.TrdEnv
			if len(a.TrdMarketAuthList) > 0 {
				c.trdMarket = a.TrdMarketAuthList[0]
			}
			return id, nil
		}
	}
	return 0, fmt.Errorf("no accounts found")
}

func (c *Client) unlockTrade(pin string) error {
	h := md5.Sum([]byte(pin))
	md5hex := strings.ToLower(fmt.Sprintf("%x", h))
	req := map[string]any{
		"c2s": map[string]any{
			"unlock":       true,
			"pwdMD5":       md5hex,
			"securityFirm": c.securityFirm,
		},
	}
	_, err := c.call(protoUnlockTrade, req)
	return err
}

func (c *Client) call(protoID uint32, payload any) (json.RawMessage, error) {
	body, err := json.Marshal(payload)
	if err != nil {
		return nil, err
	}

	sn := atomic.AddUint32(&c.serial, 1)
	hdr := make([]byte, headerSize)
	hdr[0] = magic[0]                                        // 'F'
	hdr[1] = magic[1]                                        // 'T'
	binary.LittleEndian.PutUint32(hdr[2:6], protoID)         // proto_id
	hdr[6] = bodyTypeJSON                                    // proto_fmt_type (1=JSON)
	hdr[7] = 0                                               // proto_ver
	binary.LittleEndian.PutUint32(hdr[8:12], sn)             // serial_no
	binary.LittleEndian.PutUint32(hdr[12:16], uint32(len(body))) // body_len
	bodySHA := sha1.Sum(body)                                    // SHA1 of body
	copy(hdr[16:36], bodySHA[:])                                 // bytes 16-35
	// bytes 36-43: reserved

	c.conn.SetDeadline(time.Now().Add(10 * time.Second))
	if _, err := c.conn.Write(append(hdr, body...)); err != nil {
		return nil, fmt.Errorf("send proto %d: %w", protoID, err)
	}

	// Read response header
	rHdr := make([]byte, headerSize)
	if _, err := readFull(c.conn, rHdr); err != nil {
		return nil, fmt.Errorf("recv header proto %d: %w", protoID, err)
	}
	bodyLen := binary.LittleEndian.Uint32(rHdr[12:16])
	respFmtType := rHdr[6] // proto_fmt_type: 0=protobuf, 1=JSON

	rBody := make([]byte, bodyLen)
	if bodyLen > 0 {
		if _, err := readFull(c.conn, rBody); err != nil {
			return nil, fmt.Errorf("recv body proto %d: %w", protoID, err)
		}
	}

	// Check if OpenD responded with protobuf instead of JSON.
	if respFmtType != bodyTypeJSON {
		if len(rBody) > 0 && (rBody[0] == '{' || rBody[0] == '[') {
			// Body looks like JSON despite the header — proceed.
		} else {
			return nil, fmt.Errorf("OpenD responded with protobuf (fmt_type=%d) for proto %d — "+
				"ensure OpenD is configured for JSON mode", respFmtType, protoID)
		}
	}

	// Check for error in response
	var errCheck struct {
		RetType int    `json:"retType"`
		RetMsg  string `json:"retMsg"`
	}
	if err := json.Unmarshal(rBody, &errCheck); err == nil && errCheck.RetType != 0 {
		return nil, fmt.Errorf("OpenD error %d: %s", errCheck.RetType, errCheck.RetMsg)
	}

	return json.RawMessage(rBody), nil
}

// ─── Helpers ─────────────────────────────────────────────────────────────────

func readFull(conn net.Conn, buf []byte) (int, error) {
	total := 0
	for total < len(buf) {
		n, err := conn.Read(buf[total:])
		total += n
		if err != nil {
			return total, err
		}
	}
	return total, nil
}

func sideStr(v int) string {
	if v == 1 {
		return "BUY"
	}
	return "SELL"
}

func sideInt(s string) int {
	if strings.ToUpper(s) == "BUY" {
		return 1
	}
	return 2
}

func orderTypeStr(v int) string {
	switch v {
	case 1:
		return "LMT"
	case 5:
		return "MKT"
	case 6:
		return "STP"
	default:
		return fmt.Sprintf("%d", v)
	}
}

func orderTypeInt(s string) int {
	switch strings.ToUpper(s) {
	case "LMT", "LIMIT":
		return 1
	case "MKT", "MARKET":
		return 5
	case "STP", "STOP":
		return 6
	default:
		return 1
	}
}

func tifStr(v int) string {
	if v == 1 {
		return "GTC"
	}
	return "DAY"
}

func tifInt(s string) int {
	if strings.ToUpper(s) == "GTC" {
		return 1
	}
	return 0
}

func orderStatusStr(v int) string {
	switch v {
	case 1:
		return "Submitting"
	case 2:
		return "Submitted"
	case 3:
		return "PartFilled"
	case 4:
		return "Filled"
	case 5:
		return "Cancelled"
	case 11:
		return "Failed"
	default:
		return fmt.Sprintf("%d", v)
	}
}

func loadEnvFile() {
	paths := []string{
		".env",
		"../../../brokers/Moomoo/.env",
	}
	if exe, err := os.Executable(); err == nil {
		paths = append(paths, exe+"/../.env")
	}
	for _, p := range paths {
		data, err := os.ReadFile(p)
		if err != nil {
			continue
		}
		for _, line := range strings.Split(string(data), "\n") {
			line = strings.TrimSpace(line)
			if line == "" || strings.HasPrefix(line, "#") {
				continue
			}
			k, v, ok := strings.Cut(line, "=")
			if !ok {
				continue
			}
			if os.Getenv(strings.TrimSpace(k)) == "" {
				os.Setenv(strings.TrimSpace(k), strings.TrimSpace(v))
			}
		}
		return
	}
}

func getenv(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}
