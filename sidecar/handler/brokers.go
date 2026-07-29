package handler

import (
	"encoding/json"
	"net/http"

	"trade-kit-sidecar/api"
)

// ListBrokers returns all registered brokers with status.
func (h *Handlers) ListBrokers(w http.ResponseWriter, r *http.Request) {
	api.WriteJSON(w, http.StatusOK, h.registry.List())
}

// ConnectBroker connects a broker using provided credentials.
func (h *Handlers) ConnectBroker(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	adapter, err := h.registry.Get(id)
	if err != nil {
		api.WriteError(w, http.StatusNotFound, err.Error())
		return
	}

	var creds map[string]string
	if err := json.NewDecoder(r.Body).Decode(&creds); err != nil {
		api.WriteError(w, http.StatusBadRequest, "invalid JSON body")
		return
	}

	if err := adapter.Connect(creds); err != nil {
		api.WriteJSON(w, http.StatusOK, map[string]interface{}{
			"ok":    false,
			"error": err.Error(),
		})
		return
	}

	api.WriteJSON(w, http.StatusOK, map[string]interface{}{
		"ok": true,
	})
}

// TestBroker verifies a broker connection is alive.
func (h *Handlers) TestBroker(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	adapter, err := h.registry.Get(id)
	if err != nil {
		api.WriteError(w, http.StatusNotFound, err.Error())
		return
	}

	if !adapter.Connected() {
		api.WriteJSON(w, http.StatusOK, map[string]interface{}{
			"connected": false,
			"error":     "not connected",
		})
		return
	}

	if err := adapter.Test(); err != nil {
		api.WriteJSON(w, http.StatusOK, map[string]interface{}{
			"connected": false,
			"error":     err.Error(),
		})
		return
	}

	api.WriteJSON(w, http.StatusOK, map[string]interface{}{
		"connected": true,
	})
}

// ListBrokerAccounts returns available accounts after connecting.
func (h *Handlers) ListBrokerAccounts(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	adapter, err := h.registry.Get(id)
	if err != nil {
		api.WriteError(w, http.StatusNotFound, err.Error())
		return
	}
	if !adapter.Connected() {
		api.WriteError(w, http.StatusBadRequest, "broker not connected")
		return
	}

	accounts, err := adapter.ListAccounts()
	if err != nil {
		api.WriteError(w, http.StatusInternalServerError, err.Error())
		return
	}
	api.WriteJSON(w, http.StatusOK, accounts)
}

// SelectBrokerAccount sets the active account for a broker.
func (h *Handlers) SelectBrokerAccount(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	adapter, err := h.registry.Get(id)
	if err != nil {
		api.WriteError(w, http.StatusNotFound, err.Error())
		return
	}
	if !adapter.Connected() {
		api.WriteError(w, http.StatusBadRequest, "broker not connected")
		return
	}

	var req struct {
		AccID string `json:"acc_id"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		api.WriteError(w, http.StatusBadRequest, "invalid JSON body")
		return
	}
	if req.AccID == "" {
		api.WriteError(w, http.StatusBadRequest, "acc_id is required")
		return
	}

	if err := adapter.SelectAccount(req.AccID); err != nil {
		api.WriteError(w, http.StatusBadRequest, err.Error())
		return
	}
	api.WriteOK(w)
}

// DisconnectBroker disconnects a broker.
func (h *Handlers) DisconnectBroker(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	adapter, err := h.registry.Get(id)
	if err != nil {
		api.WriteError(w, http.StatusNotFound, err.Error())
		return
	}

	adapter.Disconnect()
	api.WriteOK(w)
}

// BuyOrder places a buy order through a broker.
func (h *Handlers) BuyOrder(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	adapter, err := h.registry.Get(id)
	if err != nil {
		api.WriteError(w, http.StatusNotFound, err.Error())
		return
	}
	if !adapter.Connected() {
		api.WriteError(w, http.StatusBadRequest, "broker not connected")
		return
	}

	var req struct {
		Symbol     string  `json:"symbol"`
		Qty        int     `json:"qty"`
		LimitPrice float64 `json:"limit_price"`
		StopPrice  float64 `json:"stop_price"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		api.WriteError(w, http.StatusBadRequest, "invalid JSON body")
		return
	}
	if req.Symbol == "" || req.Qty <= 0 {
		api.WriteError(w, http.StatusBadRequest, "symbol and qty are required")
		return
	}

	entryID, stopID, err := adapter.Buy(req.Symbol, req.Qty, req.LimitPrice, req.StopPrice)
	if err != nil {
		api.WriteJSON(w, http.StatusOK, map[string]interface{}{"ok": false, "error": err.Error()})
		return
	}
	api.WriteJSON(w, http.StatusOK, map[string]interface{}{
		"ok":       true,
		"entry_id": entryID,
		"stop_id":  stopID,
	})
}

// SellOrder closes a position by symbol.
func (h *Handlers) SellOrder(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	adapter, err := h.registry.Get(id)
	if err != nil {
		api.WriteError(w, http.StatusNotFound, err.Error())
		return
	}
	if !adapter.Connected() {
		api.WriteError(w, http.StatusBadRequest, "broker not connected")
		return
	}

	var req struct {
		Symbol string `json:"symbol"`
		Qty    int    `json:"qty"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		api.WriteError(w, http.StatusBadRequest, "invalid JSON body")
		return
	}
	if req.Symbol == "" {
		api.WriteError(w, http.StatusBadRequest, "symbol is required")
		return
	}

	orderID, err := adapter.Sell(req.Symbol, req.Qty)
	if err != nil {
		api.WriteJSON(w, http.StatusOK, map[string]interface{}{"ok": false, "error": err.Error()})
		return
	}
	api.WriteJSON(w, http.StatusOK, map[string]interface{}{
		"ok":       true,
		"order_id": orderID,
	})
}
