// moomoo-cli — execute Moomoo/Futu trades from the command line.
// Pure Go — communicates with Futu OpenD via TCP (no Python bridge required).
//
// Build:
//
//	cd tools/moomoo && go build -o moomoo-cli ./cmd/
package main

import (
	"bufio"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"strconv"
	"strings"
	"text/tabwriter"

	"moomoo-cli/client"
	"moomoo-cli/internal/tlog"
	"moomoo-cli/ops"
)

const usage = `moomoo-cli — Moomoo trade execution CLI
Requires OpenD running on localhost:11111.

Usage:
  moomoo-cli [--paper|--live] [--json] <command> [args]

Global flags:
  --paper   Paper/simulation mode (default — no real orders sent)
  --live    Live trading mode (requires Y confirmation for write ops)
  --json    Output as JSON (for scripting)

Read commands (safe — no confirmation needed):
  positions                         List open positions
  account                           Account cash, buying power, net assets
  quote  <SYMBOL>                   Real-time price snapshot (Yahoo Finance)
  orders                            List open/pending orders

Write commands (paper: log only | live: confirm then execute):
  buy    <SYMBOL> <SHARES>          Market buy
         --limit  <price>           → Limit buy instead (DAY)
         --stop   <price>           Also place stop-loss after buy
         --target <price>           Also place take-profit after buy

  sell   <SYMBOL> <SHARES>          Market sell
         --limit  <price>           → Limit sell instead (GTC)

  stop   <SYMBOL> <SHARES>          Set stop-loss on existing position
         --price  <price>           Stop trigger price (required)

  target <SYMBOL> <SHARES>          Set take-profit on existing position
         --price  <price>           Limit price (required)

  cancel <ORDER_ID>                 Cancel an open order
  modify <ORDER_ID>                 Modify price or quantity
         --limit  <price>           New limit price
         --stop   <price>           New aux/stop price
         --qty    <n>               New quantity

Symbol format (auto-detected):
  AAPL         → US.AAPL
  Z74 / G13    → SG.Z74  (SGX alpha+digit pattern)
  558 / 558.SI → SG.558  (digits or .SI suffix)
  SG.558       → SG.558  (explicit prefix → pass-through)
  HK.00700     → HK.00700

Examples:
  moomoo-cli positions
  moomoo-cli account
  moomoo-cli quote Z74
  moomoo-cli orders
  moomoo-cli --live buy Z74 100 --limit 4.50 --stop 4.20
  moomoo-cli --live sell Z74 100
  moomoo-cli --live stop Z74 100 --price 4.20
  moomoo-cli --live target Z74 100 --price 5.00
  moomoo-cli --live cancel FS1C71DE6D05BA1000
  moomoo-cli --live modify FS1C71DE6D05BA1000 --limit 4.60
`

func main() {
	paperFlag := flag.Bool("paper", false, "Paper/simulation mode (default)")
	liveFlag  := flag.Bool("live", false, "Live trading — real orders")
	jsonFlag  := flag.Bool("json", false, "Output as JSON")
	flag.Usage = func() { fmt.Fprint(os.Stderr, usage) }
	flag.Parse()

	args := flag.Args()
	if len(args) == 0 {
		fmt.Fprint(os.Stderr, usage)
		os.Exit(1)
	}

	paper := !*liveFlag || *paperFlag

	// quote is Yahoo Finance — no OpenD connection needed
	if args[0] == "quote" {
		if len(args) < 2 {
			fatalf("usage: moomoo-cli quote <SYMBOL>")
		}
		q, err := ops.GetQuote(args[1])
		check(err)
		if *jsonFlag {
			printJSON(q)
		} else {
			sign := "+"
			if q.ChangePct < 0 {
				sign = ""
			}
			fmt.Printf("%s  $%.4f  (%s%.2f%%)  [%s]\n", q.Symbol, q.Price, sign, q.ChangePct, q.Currency)
			fmt.Printf("  O: %.4f  H: %.4f  L: %.4f  Vol: %s\n",
				q.Open, q.High, q.Low, commas(q.Volume))
			fmt.Printf("  Prev close: %.4f\n", q.PrevClose)
		}
		return
	}

	if paper {
		tlog.Info("paper mode — no real orders will be sent (use --live for live trading)")
	}

	cfg := client.LoadConfig()
	c, err := client.Connect(cfg, paper)
	if err != nil {
		tlog.Error("%v", err)
		os.Exit(1)
	}
	defer func() {
		if err := c.Close(); err != nil {
			fmt.Fprintf(os.Stderr, "moomoo: close: %v\n", err)
		}
	}()
	tlog.Info("connected (mode: %s)", map[bool]string{true: "PAPER", false: "LIVE"}[paper])

	cmd := args[0]
	rest := args[1:]

	switch cmd {
	case "accounts":
		cmdAccounts(c, *jsonFlag)
	case "positions":
		cmdPositions(c, *jsonFlag)
	case "account":
		cmdAccount(c, *jsonFlag)
	case "orders":
		cmdOrders(c, *jsonFlag)
	case "buy":
		requireArgs(rest, 2, "buy <SYMBOL> <SHARES>")
		cmdBuy(c, rest, *jsonFlag, *liveFlag)
	case "sell":
		requireArgs(rest, 2, "sell <SYMBOL> <SHARES>")
		cmdSell(c, rest, *jsonFlag, *liveFlag)
	case "stop":
		requireArgs(rest, 2, "stop <SYMBOL> <SHARES> --price <price>")
		cmdStop(c, rest, *jsonFlag, *liveFlag)
	case "target":
		requireArgs(rest, 2, "target <SYMBOL> <SHARES> --price <price>")
		cmdTarget(c, rest, *jsonFlag, *liveFlag)
	case "cancel":
		requireArgs(rest, 1, "cancel <ORDER_ID>")
		cmdCancel(c, rest[0], *jsonFlag, *liveFlag)
	case "modify":
		requireArgs(rest, 1, "modify <ORDER_ID> [--limit P] [--stop P] [--qty N]")
		cmdModify(c, rest, *jsonFlag, *liveFlag)
	default:
		fatalf("unknown command %q\n\n%s", cmd, usage)
	}
}

// ── Read commands ─────────────────────────────────────────────────────────────

func cmdAccounts(c *client.Client, asJSON bool) {
	accounts, err := c.ListAccounts()
	check(err)
	if asJSON {
		printJSON(accounts)
		return
	}
	w := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
	fmt.Fprintln(w, "ACC ID\tENV\tTYPE\tMARKETS")
	fmt.Fprintln(w, "------\t---\t----\t-------")
	for _, a := range accounts {
		env := "SIMULATE"
		if a.TrdEnv == 1 {
			env = "REAL"
		}
		var markets []string
		for _, m := range a.Markets {
			markets = append(markets, client.TrdMarketName(m))
		}
		fmt.Fprintf(w, "%d\t%s\t%d\t%s\n", a.AccID, env, a.AccType, strings.Join(markets, ","))
	}
	w.Flush()

	// Also show which account is currently active
	fmt.Printf("\nActive: accID=%d\n", c.AccID())

	// Scan all accounts for non-zero balances
	fmt.Println("\nScanning all accounts for balances...")
	for _, a := range accounts {
		for _, mkt := range a.Markets {
			c.SwitchAccount(a.AccID, a.TrdEnv, mkt)
			acct, err := c.AccountInfo()
			if err != nil {
				continue
			}
			if acct.NetAssets > 0 || acct.Cash > 0 {
				fmt.Printf("  ✓ accID=%d env=%d market=%s → Net=$%.2f Cash=$%.2f\n",
					a.AccID, a.TrdEnv, client.TrdMarketName(mkt), acct.NetAssets, acct.Cash)
			}
			pos, err := c.Positions()
			if err == nil && len(pos) > 0 {
				fmt.Printf("  ✓ accID=%d env=%d market=%s → %d positions\n",
					a.AccID, a.TrdEnv, client.TrdMarketName(mkt), len(pos))
			}
		}
	}
}

func cmdPositions(c *client.Client, asJSON bool) {
	positions, err := c.Positions()
	check(err)
	if asJSON {
		printJSON(positions)
		return
	}
	if len(positions) == 0 {
		fmt.Println("No open positions.")
		return
	}
	w := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
	fmt.Fprintln(w, "SYMBOL\tSHARES\tAVG COST\tMKT PRICE\tMKT VALUE\tUNREAL P&L\tPCT")
	fmt.Fprintln(w, "------\t------\t--------\t---------\t---------\t----------\t---")
	for _, p := range positions {
		sign := "+"
		if p.UnrealPnL < 0 {
			sign = ""
		}
		fmt.Fprintf(w, "%s\t%d\t%.4f\t%.4f\t%.2f\t%s%.2f\t%s%.1f%%\n",
			p.Symbol, p.Shares, p.AvgCost, p.MarketPrice, p.MarketValue,
			sign, p.UnrealPnL, sign, p.UnrealPct)
	}
	w.Flush()
}

func cmdAccount(c *client.Client, asJSON bool) {
	a, err := c.AccountInfo()
	check(err)
	if asJSON {
		printJSON(a)
		return
	}
	fmt.Printf("Net Assets:    %s %.2f\n", a.Currency, a.NetAssets)
	fmt.Printf("Cash:          %s %.2f\n", a.Currency, a.Cash)
	fmt.Printf("Market Value:  %s %.2f\n", a.Currency, a.MarketValue)
	fmt.Printf("Buying Power:  %s %.2f\n", a.Currency, a.BuyingPower)
}

func cmdOrders(c *client.Client, asJSON bool) {
	orders, err := c.Orders()
	check(err)
	if asJSON {
		printJSON(orders)
		return
	}
	if len(orders) == 0 {
		fmt.Println("No open orders.")
		return
	}
	w := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
	fmt.Fprintln(w, "ORDER ID\tSYMBOL\tSIDE\tTYPE\tQTY\tFILLED\tPRICE\tAUX\tSTATUS")
	fmt.Fprintln(w, "--------\t------\t----\t----\t---\t------\t-----\t---\t------")
	for _, o := range orders {
		price, aux := "-", "-"
		if o.Price > 0 {
			price = fmt.Sprintf("%.4f", o.Price)
		}
		if o.AuxPrice > 0 {
			aux = fmt.Sprintf("%.4f", o.AuxPrice)
		}
		fmt.Fprintf(w, "%s\t%s\t%s\t%s\t%d\t%d\t%s\t%s\t%s\n",
			o.OrderID, o.Symbol, o.Side, o.Type, o.Qty, o.Filled, price, aux, o.Status)
	}
	w.Flush()
}

// ── Write commands ────────────────────────────────────────────────────────────

func cmdBuy(c *client.Client, args []string, asJSON, live bool) {
	fs := flag.NewFlagSet("buy", flag.ExitOnError)
	limitPrice  := fs.Float64("limit", 0, "Limit price")
	stopPrice   := fs.Float64("stop", 0, "Stop-loss after fill")
	targetPrice := fs.Float64("target", 0, "Take-profit after fill")
	fs.Parse(args[2:])

	sym    := ops.NormalizeSymbol(args[0])
	shares := parseInt64(args[1], "shares")

	orderType := "MKT"
	tif := "DAY"
	price := 0.0
	if *limitPrice > 0 {
		orderType = "LMT"
		price = *limitPrice
	}
	confirmLive(live, "BUY %d %s @ %s", shares, sym, map[bool]string{true: fmt.Sprintf("LIMIT %.4f", price), false: "MARKET"}[price > 0])
	res, err := c.PlaceOrder(sym, "BUY", orderType, shares, price, 0, tif)
	check(err)
	printOrderResult(res, asJSON)

	if *stopPrice > 0 {
		r, err := c.PlaceOrder(sym, "SELL", "STP", shares, *stopPrice, *stopPrice, "GTC")
		check(err)
		if !asJSON {
			fmt.Printf("  + stop-loss  → order %s\n", r.OrderID)
		}
	}
	if *targetPrice > 0 {
		r, err := c.PlaceOrder(sym, "SELL", "LMT", shares, *targetPrice, 0, "GTC")
		check(err)
		if !asJSON {
			fmt.Printf("  + take-profit → order %s\n", r.OrderID)
		}
	}
}

func cmdSell(c *client.Client, args []string, asJSON, live bool) {
	fs := flag.NewFlagSet("sell", flag.ExitOnError)
	limitPrice := fs.Float64("limit", 0, "Limit price (GTC)")
	fs.Parse(args[2:])

	sym    := ops.NormalizeSymbol(args[0])
	shares := parseInt64(args[1], "shares")

	orderType := "MKT"
	tif := "DAY"
	price := 0.0
	if *limitPrice > 0 {
		orderType = "LMT"
		tif = "GTC"
		price = *limitPrice
	}
	confirmLive(live, "SELL %d %s @ %s", shares, sym, map[bool]string{true: fmt.Sprintf("LIMIT %.4f GTC", price), false: "MARKET"}[price > 0])
	res, err := c.PlaceOrder(sym, "SELL", orderType, shares, price, 0, tif)
	check(err)
	printOrderResult(res, asJSON)
}

func cmdStop(c *client.Client, args []string, asJSON, live bool) {
	fs := flag.NewFlagSet("stop", flag.ExitOnError)
	price := fs.Float64("price", 0, "Stop trigger price (required)")
	fs.Parse(args[2:])
	if *price == 0 {
		fatalf("--price is required for stop command")
	}
	sym    := ops.NormalizeSymbol(args[0])
	shares := parseInt64(args[1], "shares")
	confirmLive(live, "SET STOP-LOSS %d %s @ %.4f (GTC, stop-market)", shares, sym, *price)
	res, err := c.PlaceOrder(sym, "SELL", "STP", shares, *price, *price, "GTC")
	check(err)
	printOrderResult(res, asJSON)
}

func cmdTarget(c *client.Client, args []string, asJSON, live bool) {
	fs := flag.NewFlagSet("target", flag.ExitOnError)
	price := fs.Float64("price", 0, "Take-profit limit price (required)")
	fs.Parse(args[2:])
	if *price == 0 {
		fatalf("--price is required for target command")
	}
	sym    := ops.NormalizeSymbol(args[0])
	shares := parseInt64(args[1], "shares")
	confirmLive(live, "SET TAKE-PROFIT %d %s @ %.4f (GTC, limit)", shares, sym, *price)
	res, err := c.PlaceOrder(sym, "SELL", "LMT", shares, *price, 0, "GTC")
	check(err)
	printOrderResult(res, asJSON)
}

func cmdCancel(c *client.Client, orderID string, asJSON, live bool) {
	confirmLive(live, "CANCEL order %s", orderID)
	err := c.ModifyOrder(orderID, 0, 0, 0, true)
	check(err)
	if asJSON {
		printJSON(map[string]string{"order_id": orderID, "status": "CANCELLED"})
		return
	}
	fmt.Printf("Cancelled order %s\n", orderID)
}

func cmdModify(c *client.Client, args []string, asJSON, live bool) {
	fs := flag.NewFlagSet("modify", flag.ExitOnError)
	limitPrice := fs.Float64("limit", 0, "New limit price")
	stopPrice  := fs.Float64("stop", 0, "New aux/stop price")
	qty        := fs.Int64("qty", 0, "New quantity")
	fs.Parse(args[1:])

	orderID := args[0]
	if *limitPrice == 0 && *stopPrice == 0 && *qty == 0 {
		fatalf("modify requires at least one of: --limit, --stop, --qty")
	}
	confirmLive(live, "MODIFY order %s  limit=%v stop=%v qty=%v", orderID, *limitPrice, *stopPrice, *qty)
	err := c.ModifyOrder(orderID, *qty, *limitPrice, *stopPrice, false)
	check(err)
	if asJSON {
		printJSON(map[string]interface{}{"order_id": orderID, "status": "MODIFIED"})
		return
	}
	fmt.Printf("Modified order %s\n", orderID)
}

// ── Helpers ───────────────────────────────────────────────────────────────────

func printOrderResult(r client.OrderResult, asJSON bool) {
	if asJSON {
		printJSON(r)
		return
	}
	mode := "LIVE"
	if r.Status == "PAPER" {
		mode = "PAPER"
	}
	if r.Price > 0 {
		fmt.Printf("[%s] %s %d %s @ %.4f → order %s\n",
			mode, r.Side, r.Qty, r.Symbol, r.Price, r.OrderID)
	} else {
		fmt.Printf("[%s] %s %d %s @ MARKET → order %s\n",
			mode, r.Side, r.Qty, r.Symbol, r.OrderID)
	}
}

func printJSON(v interface{}) {
	enc := json.NewEncoder(os.Stdout)
	enc.SetIndent("", "  ")
	enc.Encode(v)
}

func confirmLive(live bool, format string, args ...interface{}) {
	if !live {
		return
	}
	msg := fmt.Sprintf(format, args...)
	fmt.Printf("\nLIVE ORDER: %s\nExecute? [y/N] ", msg)
	reader := bufio.NewReader(os.Stdin)
	resp, _ := reader.ReadString('\n')
	if strings.TrimSpace(strings.ToLower(resp)) != "y" {
		fmt.Println("Aborted.")
		os.Exit(0)
	}
}

func requireArgs(args []string, n int, usage string) {
	if len(args) < n {
		fatalf("usage: moomoo-cli %s", usage)
	}
}

func parseInt64(s, name string) int64 {
	n, err := strconv.ParseInt(s, 10, 64)
	if err != nil || n <= 0 {
		fatalf("%s must be a positive integer, got %q", name, s)
	}
	return n
}

func commas(n int64) string {
	s := strconv.FormatInt(n, 10)
	out := ""
	for i, c := range s {
		if i > 0 && (len(s)-i)%3 == 0 {
			out += ","
		}
		out += string(c)
	}
	return out
}

func check(err error) {
	if err != nil {
		tlog.Error("%v", err)
		os.Exit(1)
	}
}

func fatalf(format string, args ...interface{}) {
	tlog.Error(format, args...)
	os.Exit(1)
}
