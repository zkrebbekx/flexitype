package main

import (
	"crypto/subtle"
	"encoding/json"
	"net/http"
	"strconv"
)

// API serves the shopper-facing catalog and the platform-facing internal
// endpoints.
type API struct {
	store     *Store
	projector *Projector
	// internalToken authenticates the platform. The internal endpoints
	// register a merchant's credential and start a backfill, so they are not
	// shopper-reachable.
	internalToken string
	log           Logger
}

// NewAPI wires the storefront's HTTP surface.
func NewAPI(store *Store, projector *Projector, internalToken string, log Logger) *API {
	return &API{store: store, projector: projector, internalToken: internalToken, log: log}
}

// Handler builds the route table.
func (a *API) Handler(ingest *Ingest) http.Handler {
	mux := http.NewServeMux()

	mux.Handle("POST /hook/{tenant}", ingest)

	// Shopper API — public, read-only, active products only.
	mux.HandleFunc("GET /api/products", a.searchProducts)
	mux.HandleFunc("GET /api/products/{tenant}/{entityID}", a.getProduct)
	mux.HandleFunc("GET /api/products/{tenant}/{entityID}/image", a.getProductImage)
	mux.HandleFunc("GET /api/merchants", a.listMerchants)
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	})

	// Internal API — the platform only.
	mux.Handle("PUT /internal/merchants/{tenant}", a.internal(a.putMerchant))
	mux.Handle("POST /internal/merchants/{tenant}/backfill", a.internal(a.backfill))

	return mux
}

// internal gates a handler on the shared platform credential.
func (a *API) internal(next http.HandlerFunc) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		presented := r.Header.Get("X-Internal-Token")
		// Constant-time: a byte-by-byte comparison leaks the token's prefix
		// to a caller that can measure the response time.
		if a.internalToken == "" ||
			subtle.ConstantTimeCompare([]byte(presented), []byte(a.internalToken)) != 1 {
			writeError(w, http.StatusUnauthorized, "unauthorized")
			return
		}
		next(w, r)
	})
}

// searchProducts answers the storefront's search and filter query. Only active
// products are ever returned; Store.Search clamps that.
func (a *API) searchProducts(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	limit, _ := strconv.Atoi(q.Get("limit"))
	offset, _ := strconv.Atoi(q.Get("offset"))

	// `status` is accepted so a front end can pass its filter state through,
	// but the only value a shopper can be shown is "active". Anything else
	// returns nothing rather than an error, because a draft must not even be
	// PROBEABLE from here.
	if s := q.Get("status"); s != "" && s != "active" {
		writeJSON(w, http.StatusOK, map[string]any{"items": []Product{}})
		return
	}

	items, err := a.store.Search(r.Context(), Filter{
		Query:    q.Get("q"),
		Tenant:   q.Get("merchant"),
		MinPrice: q.Get("min_price"),
		MaxPrice: q.Get("max_price"),
		Limit:    limit,
		Offset:   offset,
	})
	if err != nil {
		a.log.Error("search catalog", "error", err)
		writeError(w, http.StatusInternalServerError, "search failed")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": items})
}

func (a *API) getProduct(w http.ResponseWriter, r *http.Request) {
	product, ok, err := a.store.Get(r.Context(), r.PathValue("tenant"), r.PathValue("entityID"))
	if err != nil {
		a.log.Error("read product", "error", err)
		writeError(w, http.StatusInternalServerError, "read failed")
		return
	}
	if !ok {
		writeError(w, http.StatusNotFound, "no such product")
		return
	}
	writeJSON(w, http.StatusOK, product)
}

// getProductImage streams a product photo.
//
// The bytes live in flexitype's blob store behind that merchant's credential,
// so the storefront proxies them. A shopper never holds a merchant token, and
// the image of a non-active product is unreachable because Store.Get already
// refused the row.
func (a *API) getProductImage(w http.ResponseWriter, r *http.Request) {
	tenant := r.PathValue("tenant")
	product, ok, err := a.store.Get(r.Context(), tenant, r.PathValue("entityID"))
	if err != nil {
		a.log.Error("read product", "error", err)
		writeError(w, http.StatusInternalServerError, "read failed")
		return
	}
	if !ok || len(product.Image) == 0 {
		writeError(w, http.StatusNotFound, "no such image")
		return
	}
	var media struct {
		ObjectKey string `json:"object_key"`
	}
	if err := json.Unmarshal(product.Image, &media); err != nil || media.ObjectKey == "" {
		writeError(w, http.StatusNotFound, "no such image")
		return
	}
	c, err := a.projector.clients.get(r.Context(), tenant)
	if err != nil {
		writeError(w, http.StatusNotFound, "no such image")
		return
	}
	body, mime, err := c.DownloadMedia(r.Context(), media.ObjectKey)
	if err != nil {
		a.log.Error("download media", "tenant", tenant, "error", err)
		writeError(w, http.StatusNotFound, "no such image")
		return
	}
	if mime != "" {
		w.Header().Set("Content-Type", mime)
	}
	// The bytes are merchant-supplied, so they must never render as active
	// content on this origin.
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.Header().Set("Content-Security-Policy", "default-src 'none'; sandbox")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(body)
}

func (a *API) listMerchants(w http.ResponseWriter, r *http.Request) {
	merchants, err := a.store.Merchants(r.Context())
	if err != nil {
		a.log.Error("list merchants", "error", err)
		writeError(w, http.StatusInternalServerError, "read failed")
		return
	}
	// Merchant marshals with `json:"-"` on the token and the secret, so
	// neither can reach a shopper through this response.
	items := merchants
	if items == nil {
		items = []Merchant{}
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": items})
}

// merchantRegistration is what the platform pushes on onboarding.
type merchantRegistration struct {
	DisplayName   string `json:"display_name"`
	Token         string `json:"token"`
	WebhookSecret string `json:"webhook_secret"`
}

// putMerchant records a merchant's credential. It is a PUT because onboarding
// is idempotent: the platform re-sends the same registration on every
// onboarding call.
func (a *API) putMerchant(w http.ResponseWriter, r *http.Request) {
	tenant := r.PathValue("tenant")
	var in merchantRegistration
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<16)).Decode(&in); err != nil {
		writeError(w, http.StatusBadRequest, "bad request body")
		return
	}
	if in.DisplayName == "" || in.Token == "" || in.WebhookSecret == "" {
		writeError(w, http.StatusBadRequest, "display_name, token and webhook_secret are required")
		return
	}
	err := a.store.UpsertMerchant(r.Context(), Merchant{
		Tenant: tenant, DisplayName: in.DisplayName, Token: in.Token, WebhookSecret: in.WebhookSecret,
	})
	if err != nil {
		a.log.Error("register merchant", "tenant", tenant, "error", err)
		writeError(w, http.StatusInternalServerError, "registration failed")
		return
	}
	// A re-registration can carry a rotated token, so drop the cached client.
	a.projector.clients.forget(tenant)
	a.log.Info("merchant registered", "tenant", tenant)
	w.WriteHeader(http.StatusNoContent)
}

// backfill projects everything a merchant already has. It is synchronous so
// the seed script can assert on the result.
func (a *API) backfill(w http.ResponseWriter, r *http.Request) {
	tenant := r.PathValue("tenant")
	count, err := a.projector.Backfill(r.Context(), tenant)
	if err != nil {
		a.log.Error("backfill", "tenant", tenant, "error", err)
		writeError(w, http.StatusInternalServerError, "backfill failed")
		return
	}
	a.log.Info("backfill complete", "tenant", tenant, "projected", count)
	writeJSON(w, http.StatusOK, map[string]any{"projected": count})
}

func writeJSON(w http.ResponseWriter, status int, body any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(body)
}

func writeError(w http.ResponseWriter, status int, message string) {
	writeJSON(w, status, map[string]any{"error": map[string]string{"message": message}})
}
