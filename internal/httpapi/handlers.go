package httpapi

import (
	"encoding/json"
	"net/http"

	"koho-move-the-money/internal/money"
)

type handler struct {
	service *money.Service
}

func NewHandler(service *money.Service) http.Handler {
	h := &handler{service: service}
	mux := http.NewServeMux()
	mux.HandleFunc("POST /accounts", h.openAccount)
	mux.HandleFunc("GET /accounts/{id}", h.getAccount)
	mux.HandleFunc("GET /accounts/{id}/transactions", h.history)
	mux.HandleFunc("POST /transfers", h.transfer)
	return mux
}

func (h *handler) openAccount(w http.ResponseWriter, r *http.Request) {
	notImplemented(w)
}

func (h *handler) getAccount(w http.ResponseWriter, r *http.Request) {
	notImplemented(w)
}

func (h *handler) history(w http.ResponseWriter, r *http.Request) {
	notImplemented(w)
}

func (h *handler) transfer(w http.ResponseWriter, r *http.Request) {
	notImplemented(w)
}

func notImplemented(w http.ResponseWriter) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusNotImplemented)
	_ = json.NewEncoder(w).Encode(map[string]string{"error": money.ErrNotImplemented.Error()})
}
