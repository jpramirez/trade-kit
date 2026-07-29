package server

import (
	"net/http"

	"trade-kit-sidecar/handler"
)

// registerRoutes wires all API endpoints on the ServeMux.
func (s *Server) registerRoutes(mux *http.ServeMux) {
	h := handler.New(s.registry, s.broadcaster, s.runner, s)

	// Status
	mux.HandleFunc("GET /v1/status", h.GetStatus)
	mux.HandleFunc("POST /v1/kill", h.Kill)

	// Brokers
	mux.HandleFunc("GET /v1/brokers", h.ListBrokers)
	mux.HandleFunc("POST /v1/brokers/{id}/connect", h.ConnectBroker)
	mux.HandleFunc("POST /v1/brokers/{id}/test", h.TestBroker)
	mux.HandleFunc("GET /v1/brokers/{id}/accounts", h.ListBrokerAccounts)
	mux.HandleFunc("POST /v1/brokers/{id}/select-account", h.SelectBrokerAccount)
	mux.HandleFunc("POST /v1/brokers/{id}/disconnect", h.DisconnectBroker)
	mux.HandleFunc("GET /v1/brokers/{id}/positions", h.GetPositions)
	mux.HandleFunc("GET /v1/brokers/{id}/account", h.GetAccount)
	mux.HandleFunc("GET /v1/brokers/{id}/orders", h.GetOrders)
	mux.HandleFunc("POST /v1/brokers/{id}/buy", h.BuyOrder)
	mux.HandleFunc("POST /v1/brokers/{id}/sell", h.SellOrder)

	// Recipes
	mux.HandleFunc("GET /v1/recipes", h.ListRecipes)
	mux.HandleFunc("POST /v1/recipes/{id}/start", h.StartRecipe)
	mux.HandleFunc("POST /v1/recipes/{id}/stop", h.StopRecipe)
	mux.HandleFunc("GET /v1/recipes/{id}/signals", h.GetRecipeSignals)

	// Copilot context
	mux.HandleFunc("POST /v1/backtest", h.RunBacktest)
	mux.HandleFunc("GET /v1/configs/{id}", h.GetConfig)
	mux.HandleFunc("PUT /v1/configs/{id}", h.UpdateConfig)
	mux.HandleFunc("GET /v1/journal", h.GetJournal)
	mux.HandleFunc("GET /v1/journal/pnl", h.GetJournalPnL)
	mux.HandleFunc("POST /v1/journal", h.AddJournalEntry)
	mux.HandleFunc("GET /v1/strategies", h.ListStrategies)
	mux.HandleFunc("GET /v1/strategies/{id}", h.GetStrategy)

	// ScanQL
	mux.HandleFunc("POST /v1/recipes/custom", h.StartCustomRecipe)
	mux.HandleFunc("POST /v1/scanql/validate", h.ValidateScanQL)
	mux.HandleFunc("GET /v1/scanql/examples", h.ScanQLExamples)

	// Settings
	mux.HandleFunc("POST /v1/settings/paper-mode", h.SetPaperMode)

	// SSE events
	mux.Handle("GET /v1/events", s.broadcaster)
}
