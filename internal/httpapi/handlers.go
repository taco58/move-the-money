package httpapi

import (
	"encoding/json"
	"errors"
	"io"
	"log"
	"net/http"
	"strconv"

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
	var request struct {
		StartingBalanceCents *int64 `json:"starting_balance_cents"`
	}
	decoder := json.NewDecoder(r.Body)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&request); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid JSON request"})
		return
	}
	if err := decoder.Decode(new(any)); err != io.EOF {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "request must contain one JSON object"})
		return
	}
	if request.StartingBalanceCents == nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "starting_balance_cents is required"})
		return
	}

	account, err := h.service.OpenAccount(r.Context(), *request.StartingBalanceCents)
	if errors.Is(err, money.ErrInvalidInput) {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "starting_balance_cents must be nonnegative"})
		return
	}
	if err != nil {
		log.Printf("create account: %v", err)
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "could not create account"})
		return
	}
	writeJSON(w, http.StatusCreated, account)
}

func (h *handler) getAccount(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "account id must be a positive integer"})
		return
	}
	account, err := h.service.GetAccount(r.Context(), id)
	switch {
	case errors.Is(err, money.ErrInvalidInput):
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "account id must be a positive integer"})
	case errors.Is(err, money.ErrAccountNotFound):
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "account not found"})
	case err != nil:
		log.Printf("get account: %v", err)
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "could not get account"})
	default:
		writeJSON(w, http.StatusOK, account)
	}
}

func (h *handler) history(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "account id must be a positive integer"})
		return
	}
	entries, err := h.service.History(r.Context(), id)
	switch {
	case errors.Is(err, money.ErrInvalidInput):
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "account id must be a positive integer"})
	case errors.Is(err, money.ErrAccountNotFound):
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "account not found"})
	case err != nil:
		log.Printf("get account history: %v", err)
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "could not get account history"})
	default:
		writeJSON(w, http.StatusOK, entries)
	}
}

func (h *handler) transfer(w http.ResponseWriter, r *http.Request) {
	var request struct {
		FromAccountID *int64 `json:"from_account_id"`
		ToAccountID   *int64 `json:"to_account_id"`
		AmountCents   *int64 `json:"amount_cents"`
	}
	decoder := json.NewDecoder(r.Body)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&request); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid JSON request"})
		return
	}
	if err := decoder.Decode(new(any)); err != io.EOF {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "request must contain one JSON object"})
		return
	}
	if request.FromAccountID == nil || request.ToAccountID == nil || request.AmountCents == nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "from_account_id, to_account_id, and amount_cents are required"})
		return
	}

	receipt, replayed, err := h.service.Transfer(r.Context(), money.TransferInput{
		IdempotencyKey: r.Header.Get("Idempotency-Key"),
		FromAccountID:  *request.FromAccountID,
		ToAccountID:    *request.ToAccountID,
		AmountCents:    *request.AmountCents,
	})
	switch {
	case errors.Is(err, money.ErrInvalidInput):
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
	case errors.Is(err, money.ErrAccountNotFound):
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "account not found"})
	case errors.Is(err, money.ErrInsufficientFunds):
		writeJSON(w, http.StatusConflict, map[string]string{"error": "insufficient funds"})
	case errors.Is(err, money.ErrIdempotencyConflict):
		writeJSON(w, http.StatusConflict, map[string]string{"error": "idempotency key already used for another transfer"})
	case errors.Is(err, money.ErrBalanceOverflow):
		writeJSON(w, http.StatusConflict, map[string]string{"error": "recipient balance would overflow"})
	case errors.Is(err, money.ErrDatabaseBusy):
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "database busy; retry with the same Idempotency-Key"})
	case err != nil:
		log.Printf("create transfer: %v", err)
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "could not create transfer"})
	default:
		status := http.StatusCreated
		if replayed {
			status = http.StatusOK
		}
		writeJSON(w, status, receipt)
	}
}

func writeJSON(w http.ResponseWriter, status int, body any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	if err := json.NewEncoder(w).Encode(body); err != nil {
		log.Printf("write JSON response: %v", err)
	}
}
