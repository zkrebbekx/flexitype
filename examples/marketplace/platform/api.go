package main

import (
	"context"
	"crypto/subtle"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"

	"github.com/zkrebbekx/flexitype/client"
)

// API is the merchant-facing surface the React console will call.
//
// It is deliberately thin. Every endpoint below proxies to that merchant's
// flexitype client and adds NOTHING the console could not do itself — except
// hold the service-account token. That is the whole reason this service exists
// between the console and flexitype: a browser cannot be given a credential
// that reads and writes a merchant's entire catalog.
type API struct {
	store     *Store
	onboarder *Onboarder
	// apiToken authenticates the console. One shared token keeps the example
	// runnable; a real deployment authenticates a merchant USER and derives
	// the merchant id from the session, so one merchant cannot reach another.
	apiToken string
	clients  *clientCache
	// through forwards a READ straight to flexitype with the merchant's own
	// token, so the console's TypeScript SDK client can speak the real API.
	through *passthrough
	log     Logger
}

// NewAPI wires the merchant-facing API.
func NewAPI(store *Store, onboarder *Onboarder, apiToken, flexitypeURL string, log Logger) *API {
	return &API{
		store:     store,
		onboarder: onboarder,
		apiToken:  apiToken,
		clients:   newClientCache(store, flexitypeURL),
		through:   newPassthrough(store, flexitypeURL, log),
		log:       log,
	}
}

// Handler builds the route table.
func (a *API) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusOK) })

	mux.Handle("GET /api/merchants", a.authed(a.listMerchants))
	mux.Handle("POST /api/merchants", a.authed(a.onboardMerchant))

	mux.Handle("GET /api/merchants/{id}/types", a.authed(a.listTypes))
	mux.Handle("POST /api/merchants/{id}/types", a.authed(a.createSubtype))
	mux.Handle("GET /api/merchants/{id}/types/{typeID}/attributes", a.authed(a.listAttributes))
	mux.Handle("POST /api/merchants/{id}/types/{typeID}/attributes", a.authed(a.createAttribute))

	mux.Handle("GET /api/merchants/{id}/products", a.authed(a.listProducts))
	mux.Handle("GET /api/merchants/{id}/products/{entityID}", a.authed(a.getProduct))
	mux.Handle("PUT /api/merchants/{id}/products/{entityID}", a.authed(a.putProduct))
	mux.Handle("DELETE /api/merchants/{id}/products/{entityID}", a.authed(a.deleteProduct))
	mux.Handle("POST /api/merchants/{id}/products/{entityID}/image", a.authed(a.uploadImage))

	// The read-only flexitype passthrough. The console's SDK client is built
	// with this as its base URL, so it issues real flexitype paths and this
	// service attaches the merchant's token. See passthrough.go for why it
	// refuses a write.
	mux.Handle("/api/merchants/{id}/flexitype/api/v1/{path...}", a.authed(a.through.handle))

	return mux
}

// authed gates a handler on the console credential.
func (a *API) authed(next http.HandlerFunc) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		presented := strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer ")
		if a.apiToken == "" || subtle.ConstantTimeCompare([]byte(presented), []byte(a.apiToken)) != 1 {
			writeError(w, http.StatusUnauthorized, "unauthorized")
			return
		}
		next(w, r)
	})
}

// merchantClient resolves the merchant in the path to its flexitype client.
func (a *API) merchantClient(r *http.Request) (Merchant, *client.Client, error) {
	id := r.PathValue("id")
	merchant, ok, err := a.store.Get(r.Context(), id)
	if err != nil {
		return Merchant{}, nil, err
	}
	if !ok {
		return Merchant{}, nil, errNotFound
	}
	c, err := a.clients.get(merchant)
	return merchant, c, err
}

var errNotFound = errors.New("no such merchant")

// fail maps an error onto a response. A flexitype APIError keeps its own
// status, so a validation failure reaches the console as a 422 rather than
// being flattened to a 500.
func (a *API) fail(w http.ResponseWriter, action string, err error) {
	if errors.Is(err, errNotFound) {
		writeError(w, http.StatusNotFound, "no such merchant")
		return
	}
	var apiErr *client.APIError
	if errors.As(err, &apiErr) && apiErr.Status >= 400 && apiErr.Status < 600 {
		writeError(w, apiErr.Status, apiErr.Message)
		return
	}
	a.log.Error(action, "error", err)
	writeError(w, http.StatusInternalServerError, action+" failed")
}

func (a *API) listMerchants(w http.ResponseWriter, r *http.Request) {
	merchants, err := a.store.List(r.Context())
	if err != nil {
		a.fail(w, "list merchants", err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": merchants})
}

func (a *API) onboardMerchant(w http.ResponseWriter, r *http.Request) {
	var in OnboardInput
	if err := decode(w, r, &in); err != nil {
		writeError(w, http.StatusBadRequest, "bad request body")
		return
	}
	merchant, err := a.onboarder.Onboard(r.Context(), in)
	if err != nil {
		a.fail(w, "onboard merchant", err)
		return
	}
	// Merchant marshals without its token, so onboarding never returns the
	// credential it just minted.
	writeJSON(w, http.StatusOK, merchant)
}

func (a *API) listTypes(w http.ResponseWriter, r *http.Request) {
	_, c, err := a.merchantClient(r)
	if err != nil {
		a.fail(w, "list types", err)
		return
	}
	page, err := c.Types().List(r.Context(), client.ListTypesOptions{ListOptions: client.ListOptions{Limit: 200}})
	if err != nil {
		a.fail(w, "list types", err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": page.Items})
}

// createSubtypeInput asks for one subtype and, optionally, the attributes that
// make it different from the root product type.
type createSubtypeInput struct {
	InternalName string `json:"internal_name"`
	DisplayName  string `json:"display_name"`
	// Extends is the internal name of the parent type, "product" by default.
	Extends    string                `json:"extends"`
	Attributes []createAttributeBody `json:"attributes"`
}

type createAttributeBody struct {
	InternalName string          `json:"internal_name"`
	DisplayName  string          `json:"display_name"`
	DataType     string          `json:"data_type"`
	Required     bool            `json:"required"`
	MultiValued  bool            `json:"multi_valued"`
	Unique       bool            `json:"unique"`
	Localizable  bool            `json:"localizable"`
	Constraints  json.RawMessage `json:"constraints,omitempty"`
	HelpText     string          `json:"help_text,omitempty"`
}

// createSubtype is the endpoint a merchant uses to EXTEND the shared starter
// schema with fields only it has — size and colour, voltage and warranty.
//
// The subtype inherits every field of the root product type, so the storefront
// still finds name, price and status on it while the merchant keeps whatever
// else it needs.
func (a *API) createSubtype(w http.ResponseWriter, r *http.Request) {
	_, c, err := a.merchantClient(r)
	if err != nil {
		a.fail(w, "create subtype", err)
		return
	}
	var in createSubtypeInput
	if err := decode(w, r, &in); err != nil {
		writeError(w, http.StatusBadRequest, "bad request body")
		return
	}
	if in.InternalName == "" || in.DisplayName == "" {
		writeError(w, http.StatusBadRequest, "internal_name and display_name are required")
		return
	}
	parentName := in.Extends
	if parentName == "" {
		parentName = "product"
	}
	parent, err := typeByName(r.Context(), c, parentName)
	if err != nil {
		a.fail(w, "create subtype", err)
		return
	}

	// Creating the same subtype twice must not fail the console: a merchant
	// that is unsure whether the first click worked will click again.
	created, err := typeByName(r.Context(), c, in.InternalName)
	if err != nil && !errors.Is(err, client.ErrNotFound) {
		a.fail(w, "create subtype", err)
		return
	}
	if created == nil {
		created, err = c.Types().Create(r.Context(), client.CreateTypeInput{
			InternalName: in.InternalName,
			DisplayName:  in.DisplayName,
			ExtendsID:    parent.ID,
		})
		if err != nil {
			a.fail(w, "create subtype", err)
			return
		}
	}

	for _, attr := range in.Attributes {
		if err := createAttribute(r.Context(), c, created.ID, attr); err != nil {
			a.fail(w, "create subtype attribute", err)
			return
		}
	}
	writeJSON(w, http.StatusOK, created)
}

func (a *API) listAttributes(w http.ResponseWriter, r *http.Request) {
	_, c, err := a.merchantClient(r)
	if err != nil {
		a.fail(w, "list attributes", err)
		return
	}
	// EffectiveAttributes, not Attributes: a console that showed only a
	// subtype's OWN attributes would hide name, price and status, which is
	// most of the form.
	attrs, err := c.Types().EffectiveAttributes(r.Context(), r.PathValue("typeID"))
	if err != nil {
		a.fail(w, "list attributes", err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": attrs})
}

func (a *API) createAttribute(w http.ResponseWriter, r *http.Request) {
	_, c, err := a.merchantClient(r)
	if err != nil {
		a.fail(w, "create attribute", err)
		return
	}
	var in createAttributeBody
	if err := decode(w, r, &in); err != nil {
		writeError(w, http.StatusBadRequest, "bad request body")
		return
	}
	if err := createAttribute(r.Context(), c, r.PathValue("typeID"), in); err != nil {
		a.fail(w, "create attribute", err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// listProducts runs an FQL query over the merchant's own catalog. The default
// query returns every product of every subtype, because `name` is required on
// the root type.
func (a *API) listProducts(w http.ResponseWriter, r *http.Request) {
	_, c, err := a.merchantClient(r)
	if err != nil {
		a.fail(w, "list products", err)
		return
	}
	typeName := r.URL.Query().Get("type")
	if typeName == "" {
		typeName = "product"
	}
	query := r.URL.Query().Get("q")
	if query == "" {
		query = "has(name)"
	}
	page, err := c.QueryPage(r.Context(), typeName, query, client.QueryOptions{
		ListOptions: client.ListOptions{Limit: 100, Cursor: r.URL.Query().Get("cursor")},
	})
	if err != nil {
		a.fail(w, "list products", err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": page.Items, "page_info": page.PageInfo})
}

// getProduct returns one product's values keyed by attribute internal name,
// which is the shape a form binds to.
func (a *API) getProduct(w http.ResponseWriter, r *http.Request) {
	_, c, err := a.merchantClient(r)
	if err != nil {
		a.fail(w, "read product", err)
		return
	}
	typeDef, err := typeByName(r.Context(), c, typeParam(r))
	if err != nil {
		a.fail(w, "read product", err)
		return
	}
	values, err := c.Entities().Values(r.Context(), typeDef.ID, r.PathValue("entityID"))
	if err != nil {
		a.fail(w, "read product", err)
		return
	}
	names, err := attributeNames(r.Context(), c, typeDef.ID)
	if err != nil {
		a.fail(w, "read product", err)
		return
	}
	out := map[string]any{}
	for _, v := range values {
		key := names[v.AttributeDefinitionID]
		if key == "" {
			continue
		}
		if v.Locale != "" {
			key += "@" + v.Locale
		}
		out[key] = v.Value
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"entity_id": r.PathValue("entityID"), "type": typeDef.InternalName, "values": out,
	})
}

// putProductInput writes a whole product in one batch.
type putProductInput struct {
	// Type is the subtype's internal name.
	Type string `json:"type"`
	// Values are keyed by attribute internal name. A localized value uses
	// "name@fr".
	Values map[string]json.RawMessage `json:"values"`
}

// putProduct writes every field of a product in ONE batch.
//
// The batch is atomic: either every value lands and its events fire, or none
// does. That matters for the storefront, which would otherwise project a
// half-written product.
func (a *API) putProduct(w http.ResponseWriter, r *http.Request) {
	_, c, err := a.merchantClient(r)
	if err != nil {
		a.fail(w, "write product", err)
		return
	}
	var in putProductInput
	if err := decode(w, r, &in); err != nil {
		writeError(w, http.StatusBadRequest, "bad request body")
		return
	}
	if in.Type == "" {
		in.Type = "product"
	}
	typeDef, err := typeByName(r.Context(), c, in.Type)
	if err != nil {
		a.fail(w, "write product", err)
		return
	}
	ids, err := attributeIDs(r.Context(), c, typeDef.ID)
	if err != nil {
		a.fail(w, "write product", err)
		return
	}

	entityID := r.PathValue("entityID")
	batch := make([]client.SetValueInput, 0, len(in.Values))
	for key, raw := range in.Values {
		name, locale, _ := strings.Cut(key, "@")
		attrID, ok := ids[name]
		if !ok {
			writeError(w, http.StatusUnprocessableEntity, fmt.Sprintf("unknown attribute %q on type %q", name, in.Type))
			return
		}
		batch = append(batch, client.SetValueInput{
			AttributeDefinitionID: attrID,
			EntityID:              entityID,
			TypeDefinitionID:      typeDef.ID,
			Locale:                locale,
			Value:                 raw,
		})
	}
	if len(batch) == 0 {
		writeError(w, http.StatusBadRequest, "values must not be empty")
		return
	}
	written, err := c.Values().SetBatch(r.Context(), batch)
	if err != nil {
		a.fail(w, "write product", err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"entity_id": entityID, "written": len(written)})
}

func (a *API) deleteProduct(w http.ResponseWriter, r *http.Request) {
	_, c, err := a.merchantClient(r)
	if err != nil {
		a.fail(w, "delete product", err)
		return
	}
	typeDef, err := typeByName(r.Context(), c, typeParam(r))
	if err != nil {
		a.fail(w, "delete product", err)
		return
	}
	if err := c.Entities().Remove(r.Context(), typeDef.ID, r.PathValue("entityID")); err != nil {
		a.fail(w, "delete product", err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// uploadImage streams a multipart upload straight through to flexitype, which
// stores the bytes and writes the media value.
func (a *API) uploadImage(w http.ResponseWriter, r *http.Request) {
	_, c, err := a.merchantClient(r)
	if err != nil {
		a.fail(w, "upload image", err)
		return
	}
	typeDef, err := typeByName(r.Context(), c, typeParam(r))
	if err != nil {
		a.fail(w, "upload image", err)
		return
	}
	ids, err := attributeIDs(r.Context(), c, typeDef.ID)
	if err != nil {
		a.fail(w, "upload image", err)
		return
	}
	attrName := r.URL.Query().Get("attribute")
	if attrName == "" {
		attrName = "image"
	}
	attrID, ok := ids[attrName]
	if !ok {
		writeError(w, http.StatusUnprocessableEntity, "no such media attribute: "+attrName)
		return
	}

	r.Body = http.MaxBytesReader(w, r.Body, maxUploadBytes)
	file, header, err := r.FormFile("file")
	if err != nil {
		writeError(w, http.StatusBadRequest, "missing file part")
		return
	}
	defer func() { _ = file.Close() }()

	value, err := c.Entities().UploadMedia(r.Context(), typeDef.ID, r.PathValue("entityID"), attrID,
		header.Filename, header.Header.Get("Content-Type"), file)
	if err != nil {
		a.fail(w, "upload image", err)
		return
	}
	writeJSON(w, http.StatusOK, value)
}

// maxUploadBytes caps one image upload, so a merchant cannot exhaust this
// process's memory through the proxy.
const maxUploadBytes = 10 << 20

// typeParam reads the ?type= query argument, defaulting to the root type.
func typeParam(r *http.Request) string {
	if t := r.URL.Query().Get("type"); t != "" {
		return t
	}
	return "product"
}

// typeByName resolves a type definition from its internal name. It returns a
// client.ErrNotFound when there is none, so a caller can tell "absent" from
// "failed".
func typeByName(ctx context.Context, c *client.Client, internalName string) (*client.TypeDefinition, error) {
	page, err := c.Types().List(ctx, client.ListTypesOptions{InternalNames: []string{internalName}})
	if err != nil {
		return nil, err
	}
	for i := range page.Items {
		if page.Items[i].InternalName == internalName {
			return &page.Items[i], nil
		}
	}
	return nil, fmt.Errorf("type %q: %w", internalName, client.ErrNotFound)
}

// attributeNames maps attribute id to internal name for one type, inherited
// attributes included.
func attributeNames(ctx context.Context, c *client.Client, typeID string) (map[string]string, error) {
	attrs, err := c.Types().EffectiveAttributes(ctx, typeID)
	if err != nil {
		return nil, err
	}
	out := make(map[string]string, len(attrs))
	for _, a := range attrs {
		out[a.Attribute.ID] = a.Attribute.InternalName
	}
	return out, nil
}

// attributeIDs is the inverse of attributeNames.
func attributeIDs(ctx context.Context, c *client.Client, typeID string) (map[string]string, error) {
	attrs, err := c.Types().EffectiveAttributes(ctx, typeID)
	if err != nil {
		return nil, err
	}
	out := make(map[string]string, len(attrs))
	for _, a := range attrs {
		out[a.Attribute.InternalName] = a.Attribute.ID
	}
	return out, nil
}

// createAttribute adds one attribute to a type, treating an existing one of
// the same name as success.
func createAttribute(ctx context.Context, c *client.Client, typeID string, in createAttributeBody) error {
	if in.InternalName == "" || in.DataType == "" {
		return fmt.Errorf("attribute internal_name and data_type are required")
	}
	display := in.DisplayName
	if display == "" {
		display = in.InternalName
	}
	_, err := c.Attributes().Create(ctx, client.CreateAttributeInput{
		TypeDefinitionID: typeID,
		InternalName:     in.InternalName,
		DisplayName:      display,
		DataType:         in.DataType,
		Required:         in.Required,
		MultiValued:      in.MultiValued,
		Unique:           in.Unique,
		Localizable:      in.Localizable,
		Constraints:      in.Constraints,
		HelpText:         in.HelpText,
	})
	if errors.Is(err, client.ErrConflict) {
		return nil // already declared; creating a subtype twice is safe
	}
	return err
}

func decode(w http.ResponseWriter, r *http.Request, dst any) error {
	return json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<20)).Decode(dst)
}

func writeJSON(w http.ResponseWriter, status int, body any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(body)
}

func writeError(w http.ResponseWriter, status int, message string) {
	writeJSON(w, status, map[string]any{"error": map[string]string{"message": message}})
}
