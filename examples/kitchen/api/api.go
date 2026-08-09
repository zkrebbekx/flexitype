package main

import (
	"context"
	"encoding/csv"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/zkrebbekx/flexitype/client"
)

// API is the kitchen's HTTP surface: ingredients, dishes and their lines, the
// per-channel price matrix, and the scheduled menu change.
type API struct {
	c   *client.Client
	log Logger

	// Resolved once at start-up. A type or attribute id never changes, and
	// re-reading the schema on every request would triple the call count.
	dishType, lineType, ingredientType string
	dishAttrs, lineAttrs, ingAttrs     map[string]string
	relHasLine, relOfIngredient        string
}

// NewAPI resolves the schema ids the handlers address values by.
func NewAPI(ctx context.Context, c *client.Client, log Logger) (*API, error) {
	a := &API{c: c, log: log}
	for _, entry := range []struct {
		name  string
		id    *string
		attrs *map[string]string
	}{
		{typeDish, &a.dishType, &a.dishAttrs},
		{typeRecipeLine, &a.lineType, &a.lineAttrs},
		{typeIngredient, &a.ingredientType, &a.ingAttrs},
	} {
		td, err := typeByName(ctx, c, entry.name)
		if err != nil {
			return nil, fmt.Errorf("resolve type %q: %w", entry.name, err)
		}
		*entry.id = td.ID
		ids, aerr := attributeIDs(ctx, c, td.ID)
		if aerr != nil {
			return nil, aerr
		}
		*entry.attrs = ids
	}
	var err error
	if a.relHasLine, err = relationshipByName(ctx, c, relHasLine); err != nil {
		return nil, err
	}
	if a.relOfIngredient, err = relationshipByName(ctx, c, relOfIngredient); err != nil {
		return nil, err
	}
	return a, nil
}

// Handler builds the route table.
func (a *API) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusOK) })

	mux.HandleFunc("GET /api/ingredients", a.listIngredients)
	mux.HandleFunc("PUT /api/ingredients/{id}", a.putIngredient)
	// A supplier's price list. This is the demonstration in one call: the
	// import writes pack prices, and every dependent cost recomputes itself.
	mux.HandleFunc("POST /api/ingredients/import", a.importPriceList)

	mux.HandleFunc("GET /api/dishes", a.listDishes)
	mux.HandleFunc("GET /api/dishes/{id}", a.getDish)
	mux.HandleFunc("PUT /api/dishes/{id}", a.putDish)
	mux.HandleFunc("DELETE /api/dishes/{id}", a.deleteDish)
	mux.HandleFunc("PUT /api/dishes/{id}/lines/{lineID}", a.putLine)
	mux.HandleFunc("DELETE /api/dishes/{id}/lines/{lineID}", a.deleteLine)

	// The menu change: a set of price moves, approved and published at a time.
	mux.HandleFunc("POST /api/menu-changes", a.scheduleMenuChange)
	mux.HandleFunc("GET /api/menu-changes", a.listMenuChanges)

	// A dish reaches the menu only when the schema says it is complete.
	mux.HandleFunc("POST /api/dishes/{id}/publish", a.publishDish)

	// What a dish cost on a date, from its revisions.
	mux.HandleFunc("GET /api/dishes/{id}/cost-history", a.costHistory)

	return mux
}

// --- ingredients -------------------------------------------------------------

// Ingredient is what a supplier sells, and what a cost per kilogram is derived
// from.
type Ingredient struct {
	ID       string `json:"id"`
	Name     string `json:"name"`
	Supplier string `json:"supplier"`
	// PackSize keeps the unit it was entered in: an invoice in pounds stays in
	// pounds, and still costs per kilogram.
	PackSize  *Quantity `json:"pack_size,omitempty"`
	PackPrice string    `json:"pack_price,omitempty"`
	// CostPerKg is DERIVED by the service, never written here.
	CostPerKg string `json:"cost_per_kg,omitempty"`
}

// Quantity is a magnitude with the unit it was entered in.
type Quantity struct {
	Magnitude string `json:"magnitude"`
	Unit      string `json:"unit"`
}

func (a *API) listIngredients(w http.ResponseWriter, r *http.Request) {
	page, err := a.c.QueryPage(r.Context(), typeIngredient, "has(name)", client.QueryOptions{
		ListOptions: client.ListOptions{Limit: 200},
	})
	if err != nil {
		a.fail(w, "list ingredients", err)
		return
	}
	out := make([]Ingredient, 0, len(page.Items))
	for _, entity := range page.Items {
		ing, ierr := a.readIngredient(r.Context(), entity.EntityID)
		if ierr != nil {
			a.fail(w, "read ingredient", ierr)
			return
		}
		out = append(out, *ing)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	writeJSON(w, http.StatusOK, map[string]any{"items": out})
}

func (a *API) readIngredient(ctx context.Context, entityID string) (*Ingredient, error) {
	values, err := a.c.Entities().Values(ctx, a.ingredientType, entityID)
	if err != nil {
		return nil, err
	}
	byID := map[string]string{}
	for name, id := range a.ingAttrs {
		byID[id] = name
	}
	out := Ingredient{ID: entityID}
	for _, v := range values {
		switch byID[v.AttributeDefinitionID] {
		case "name":
			out.Name = asString(v.Value)
		case "supplier":
			out.Supplier = asString(v.Value)
		case "pack_price":
			out.PackPrice = asString(v.Value)
		case "cost_per_kg":
			out.CostPerKg = asString(v.Value)
		case "pack_size":
			out.PackSize = asQuantity(v.Value)
		}
	}
	return &out, nil
}

// putIngredientInput writes one ingredient. cost_per_kg is absent on purpose:
// it is the service's to compute, and the values API refuses a write to it.
type putIngredientInput struct {
	Name      string    `json:"name"`
	Supplier  string    `json:"supplier"`
	PackSize  *Quantity `json:"pack_size"`
	PackPrice string    `json:"pack_price"`
}

func (a *API) putIngredient(w http.ResponseWriter, r *http.Request) {
	var in putIngredientInput
	if err := decode(w, r, &in); err != nil {
		writeError(w, http.StatusBadRequest, "bad request body")
		return
	}
	if err := a.writeIngredient(r.Context(), r.PathValue("id"), in); err != nil {
		a.fail(w, "write ingredient", err)
		return
	}
	ing, err := a.readIngredient(r.Context(), r.PathValue("id"))
	if err != nil {
		a.fail(w, "read ingredient", err)
		return
	}
	writeJSON(w, http.StatusOK, ing)
}

func (a *API) writeIngredient(ctx context.Context, entityID string, in putIngredientInput) error {
	batch := []client.SetValueInput{}
	add := func(name string, raw json.RawMessage) {
		if raw == nil {
			return
		}
		batch = append(batch, client.SetValueInput{
			AttributeDefinitionID: a.ingAttrs[name], EntityID: entityID,
			TypeDefinitionID: a.ingredientType, Value: raw,
		})
	}
	add("name", jsonString(in.Name))
	add("supplier", jsonString(in.Supplier))
	add("pack_price", jsonString(in.PackPrice))
	if in.PackSize != nil {
		add("pack_size", quantity(in.PackSize.Magnitude, in.PackSize.Unit))
	}
	if len(batch) == 0 {
		return nil
	}
	// One batch: either every field of this ingredient lands, or none does.
	_, err := a.c.Values().SetBatch(ctx, batch)
	return err
}

// importPriceList takes a supplier's CSV and writes the pack prices.
//
// This is the whole demonstration: the import writes ONE value per ingredient,
// and the service recomputes the cost per kilogram, every recipe line that
// uses it, and every dish that contains those lines.
//
//	id,name,supplier,pack_size,pack_unit,pack_price
func (a *API) importPriceList(w http.ResponseWriter, r *http.Request) {
	reader := csv.NewReader(http.MaxBytesReader(w, r.Body, 1<<20))
	reader.TrimLeadingSpace = true
	rows, err := reader.ReadAll()
	if err != nil {
		writeError(w, http.StatusBadRequest, "could not read the CSV: "+err.Error())
		return
	}
	if len(rows) < 2 {
		writeError(w, http.StatusBadRequest, "the CSV needs a header row and at least one line")
		return
	}
	index := map[string]int{}
	for i, name := range rows[0] {
		index[strings.TrimSpace(strings.ToLower(name))] = i
	}
	for _, required := range []string{"id", "pack_price"} {
		if _, ok := index[required]; !ok {
			writeError(w, http.StatusBadRequest, "the CSV needs an "+required+" column")
			return
		}
	}

	written := 0
	for _, row := range rows[1:] {
		field := func(name string) string {
			i, ok := index[name]
			if !ok || i >= len(row) {
				return ""
			}
			return strings.TrimSpace(row[i])
		}
		id := field("id")
		if id == "" {
			continue
		}
		in := putIngredientInput{
			Name:      field("name"),
			Supplier:  field("supplier"),
			PackPrice: field("pack_price"),
		}
		if size, unit := field("pack_size"), field("pack_unit"); size != "" && unit != "" {
			in.PackSize = &Quantity{Magnitude: size, Unit: unit}
		}
		if err := a.writeIngredient(r.Context(), id, in); err != nil {
			a.fail(w, "import price list", err)
			return
		}
		written++
	}
	a.log.Info("imported a supplier price list", "ingredients", written)
	writeJSON(w, http.StatusOK, map[string]any{"ingredients": written})
}

// --- dishes ------------------------------------------------------------------

// Dish is what a guest orders, priced per channel and named per locale.
type Dish struct {
	ID     string `json:"id"`
	Course string `json:"course"`
	Status string `json:"status"`
	// Name and Description are per locale, keyed by locale code. The base
	// value is under "".
	Name        map[string]string `json:"name"`
	Description map[string]string `json:"description,omitempty"`
	// Price is per channel, keyed by channel. A dish is one dish at three
	// prices.
	Price             map[string]string `json:"price"`
	Allergens         []string          `json:"allergens,omitempty"`
	ContainsAllergens bool              `json:"contains_allergens"`
	// FoodCost and LineCount are DERIVED. Nothing here adds them up.
	FoodCost  string `json:"food_cost,omitempty"`
	LineCount int    `json:"line_count"`
	// Margin is computed HERE, per channel, because a formula cannot read a
	// scoped attribute — see the README.
	Margin map[string]string `json:"margin,omitempty"`
	Lines  []Line            `json:"lines,omitempty"`
}

// Line is one ingredient in one dish.
type Line struct {
	ID           string    `json:"id"`
	IngredientID string    `json:"ingredient_id"`
	Ingredient   string    `json:"ingredient,omitempty"`
	Quantity     *Quantity `json:"quantity,omitempty"`
	// CostPerKg and LineCost are derived by the service.
	CostPerKg string `json:"cost_per_kg,omitempty"`
	LineCost  string `json:"line_cost,omitempty"`
}

func (a *API) listDishes(w http.ResponseWriter, r *http.Request) {
	query := r.URL.Query().Get("q")
	if query == "" {
		query = "has(name)"
	}
	page, err := a.c.QueryPage(r.Context(), typeDish, query, client.QueryOptions{
		ListOptions: client.ListOptions{Limit: 200},
	})
	if err != nil {
		a.fail(w, "list dishes", err)
		return
	}
	out := make([]Dish, 0, len(page.Items))
	for _, entity := range page.Items {
		dish, derr := a.readDish(r.Context(), entity.EntityID, false)
		if derr != nil {
			a.fail(w, "read dish", derr)
			return
		}
		out = append(out, *dish)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	writeJSON(w, http.StatusOK, map[string]any{"items": out})
}

func (a *API) getDish(w http.ResponseWriter, r *http.Request) {
	dish, err := a.readDish(r.Context(), r.PathValue("id"), true)
	if err != nil {
		a.fail(w, "read dish", err)
		return
	}
	writeJSON(w, http.StatusOK, dish)
}

func (a *API) readDish(ctx context.Context, entityID string, withLines bool) (*Dish, error) {
	values, err := a.c.Entities().Values(ctx, a.dishType, entityID)
	if err != nil {
		return nil, err
	}
	byID := map[string]string{}
	for name, id := range a.dishAttrs {
		byID[id] = name
	}
	out := Dish{
		ID: entityID, Name: map[string]string{}, Description: map[string]string{},
		Price: map[string]string{},
	}
	for _, v := range values {
		switch byID[v.AttributeDefinitionID] {
		case "name":
			out.Name[v.Locale] = asString(v.Value)
		case "description":
			out.Description[v.Locale] = asString(v.Value)
		case "price":
			// The channel is part of the address, so one attribute holds a
			// price per channel rather than three attributes holding one each.
			out.Price[v.Channel] = asString(v.Value)
		case "course":
			out.Course = asString(v.Value)
		case "status":
			out.Status = asString(v.Value)
		case "allergens":
			out.Allergens = append(out.Allergens, asString(v.Value))
		case "contains_allergens":
			out.ContainsAllergens = asBool(v.Value)
		case "food_cost":
			out.FoodCost = asString(v.Value)
		case "line_count":
			out.LineCount = asInt(v.Value)
		}
	}
	out.Margin = marginsFor(out.FoodCost, out.Price)

	if withLines {
		lines, lerr := a.readLines(ctx, entityID)
		if lerr != nil {
			return nil, lerr
		}
		out.Lines = lines
	}
	return &out, nil
}

// readLines loads a dish's recipe lines and the ingredient each points at.
func (a *API) readLines(ctx context.Context, dishID string) ([]Line, error) {
	links, err := a.c.Entities().Relationships(ctx, a.dishType, dishID)
	if err != nil {
		return nil, err
	}
	byID := map[string]string{}
	for name, id := range a.lineAttrs {
		byID[id] = name
	}

	out := []Line{}
	for _, link := range links {
		if link.Definition.InternalName != relHasLine || link.Role != "parent" {
			continue
		}
		lineID := link.Relationship.ChildEntityID
		values, verr := a.c.Entities().Values(ctx, a.lineType, lineID)
		if verr != nil {
			return nil, verr
		}
		line := Line{ID: lineID}
		for _, v := range values {
			switch byID[v.AttributeDefinitionID] {
			case "quantity":
				line.Quantity = asQuantity(v.Value)
			case "ingredient_cost":
				line.CostPerKg = asString(v.Value)
			case "line_cost":
				line.LineCost = asString(v.Value)
			}
		}
		if ing, ierr := a.ingredientOf(ctx, lineID); ierr == nil && ing != nil {
			line.IngredientID = ing.ID
			line.Ingredient = ing.Name
		}
		out = append(out, line)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out, nil
}

// ingredientOf resolves the ingredient a line points at.
func (a *API) ingredientOf(ctx context.Context, lineID string) (*Ingredient, error) {
	links, err := a.c.Entities().Relationships(ctx, a.lineType, lineID)
	if err != nil {
		return nil, err
	}
	for _, link := range links {
		if link.Definition.InternalName == relOfIngredient && link.Role == "child" {
			return a.readIngredient(ctx, link.Relationship.ParentEntityID)
		}
	}
	return nil, nil
}

// putDishInput writes a dish. food_cost and line_count are absent: they are
// the service's, and a write to them is refused.
type putDishInput struct {
	Course            string            `json:"course"`
	Status            string            `json:"status"`
	Name              map[string]string `json:"name"`
	Description       map[string]string `json:"description"`
	Price             map[string]string `json:"price"`
	Allergens         []string          `json:"allergens"`
	ContainsAllergens *bool             `json:"contains_allergens"`
}

func (a *API) putDish(w http.ResponseWriter, r *http.Request) {
	var in putDishInput
	if err := decode(w, r, &in); err != nil {
		writeError(w, http.StatusBadRequest, "bad request body")
		return
	}
	entityID := r.PathValue("id")

	// A channel or locale the model does not declare is refused here.
	//
	// flexitype would accept it: a scope is part of a value's address, not a
	// closed set, so `price` in channel "dinein" would be written and stored
	// happily — and then read by nothing, because every read path iterates the
	// channels the menu knows. A typo would silently price a dish for nobody.
	for channel := range in.Price {
		if !known(channels, channel) {
			writeError(w, http.StatusUnprocessableEntity,
				"unknown channel "+channel+"; this menu prices for "+strings.Join(channels, ", "))
			return
		}
	}
	for _, byLocale := range []map[string]string{in.Name, in.Description} {
		for locale := range byLocale {
			if locale != "" && !known(locales, locale) {
				writeError(w, http.StatusUnprocessableEntity,
					"unknown locale "+locale+"; this menu is written in "+strings.Join(locales, ", "))
				return
			}
		}
	}

	batch := []client.SetValueInput{}
	add := func(name, locale, channel string, raw json.RawMessage) {
		batch = append(batch, client.SetValueInput{
			AttributeDefinitionID: a.dishAttrs[name], EntityID: entityID,
			TypeDefinitionID: a.dishType, Locale: locale, Channel: channel, Value: raw,
		})
	}
	for locale, text := range in.Name {
		add("name", locale, "", jsonString(text))
	}
	for locale, text := range in.Description {
		add("description", locale, "", jsonString(text))
	}
	for channel, price := range in.Price {
		add("price", "", channel, jsonString(price))
	}
	if in.Course != "" {
		add("course", "", "", jsonString(in.Course))
	}
	if in.Status != "" {
		add("status", "", "", jsonString(in.Status))
	}
	if in.ContainsAllergens != nil {
		raw, _ := json.Marshal(*in.ContainsAllergens)
		add("contains_allergens", "", "", raw)
	}
	for _, allergen := range in.Allergens {
		add("allergens", "", "", jsonString(allergen))
	}
	if len(batch) == 0 {
		writeError(w, http.StatusBadRequest, "nothing to write")
		return
	}

	// One batch. A dish that declares allergens must list them, and the
	// dependency is evaluated against the whole write — so the flag and the
	// list arriving together is what makes the write legal.
	if _, err := a.c.Values().SetBatch(r.Context(), batch); err != nil {
		a.fail(w, "write dish", err)
		return
	}
	dish, err := a.readDish(r.Context(), entityID, true)
	if err != nil {
		a.fail(w, "read dish", err)
		return
	}
	writeJSON(w, http.StatusOK, dish)
}

// known reports whether a scope is one the menu declares.
func known(allowed []string, value string) bool {
	for _, candidate := range allowed {
		if candidate == value {
			return true
		}
	}
	return false
}

// deleteDish withdraws a dish: its values are archived and its links are
// unlinked, which is what the service's cascade removal does.
//
// The recipe lines are entities of their own, so they survive — a line with no
// dish is orphaned rather than deleted, which is the honest behaviour for a
// model where a line could in principle be shared.
func (a *API) deleteDish(w http.ResponseWriter, r *http.Request) {
	if err := a.c.Entities().Remove(r.Context(), a.dishType, r.PathValue("id")); err != nil {
		a.fail(w, "remove dish", err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// putLineInput is one recipe line: how much of which ingredient.
type putLineInput struct {
	IngredientID string    `json:"ingredient_id"`
	Quantity     *Quantity `json:"quantity"`
}

func (a *API) putLine(w http.ResponseWriter, r *http.Request) {
	var in putLineInput
	if err := decode(w, r, &in); err != nil {
		writeError(w, http.StatusBadRequest, "bad request body")
		return
	}
	if in.Quantity == nil || in.IngredientID == "" {
		writeError(w, http.StatusBadRequest, "ingredient_id and quantity are required")
		return
	}
	dishID, lineID := r.PathValue("id"), r.PathValue("lineID")

	if _, err := a.c.Values().Set(r.Context(), client.SetValueInput{
		AttributeDefinitionID: a.lineAttrs["quantity"], EntityID: lineID,
		TypeDefinitionID: a.lineType, Value: quantity(in.Quantity.Magnitude, in.Quantity.Unit),
	}); err != nil {
		a.fail(w, "write line", err)
		return
	}
	// The links are what the rollups traverse. Linking a line to its dish is
	// what makes the dish's cost include it — no value is written to the dish.
	if err := a.ensureLink(r.Context(), a.relOfIngredient, in.IngredientID, lineID); err != nil {
		a.fail(w, "link ingredient", err)
		return
	}
	if err := a.ensureLink(r.Context(), a.relHasLine, dishID, lineID); err != nil {
		a.fail(w, "link line", err)
		return
	}

	dish, err := a.readDish(r.Context(), dishID, true)
	if err != nil {
		a.fail(w, "read dish", err)
		return
	}
	writeJSON(w, http.StatusOK, dish)
}

func (a *API) deleteLine(w http.ResponseWriter, r *http.Request) {
	dishID, lineID := r.PathValue("id"), r.PathValue("lineID")
	links, err := a.c.Entities().Relationships(r.Context(), a.lineType, lineID)
	if err != nil {
		a.fail(w, "read line", err)
		return
	}
	for _, link := range links {
		if link.Definition.InternalName == relHasLine {
			if uerr := a.c.Relationships().Unlink(r.Context(), link.Relationship.ID); uerr != nil {
				a.fail(w, "unlink line", uerr)
				return
			}
		}
	}
	// The dish's cost follows the link, with nothing written to the dish.
	dish, err := a.readDish(r.Context(), dishID, true)
	if err != nil {
		a.fail(w, "read dish", err)
		return
	}
	writeJSON(w, http.StatusOK, dish)
}

// ensureLink links two entities if they are not linked already.
func (a *API) ensureLink(ctx context.Context, definitionID, parent, child string) error {
	links, err := a.c.Entities().Relationships(ctx, a.lineType, child)
	if err != nil {
		return err
	}
	for _, link := range links {
		if link.Relationship.DefinitionID == definitionID &&
			link.Relationship.ParentEntityID == parent {
			return nil
		}
	}
	_, err = a.c.Relationships().Link(ctx, client.LinkInput{
		DefinitionID: definitionID, ParentEntity: parent, ChildEntity: child,
	})
	if err != nil && !errors.Is(err, client.ErrConflict) {
		return err
	}
	return nil
}

// publishDish puts a dish on the menu, but only if it is COMPLETE.
//
// A dependency makes an attribute required — allergens, once a dish declares
// it has them — and the service enforces that when the attribute itself is
// written. Setting the flag alone is accepted: the rule describes what the
// dish needs, not the order a chef fills it in.
//
// So something has to decide when "needs" becomes "must", and that is
// publishing. The gate reads the service's own completeness report rather than
// re-deriving the rules here, so a dependency added later is enforced with no
// change to this code.
func (a *API) publishDish(w http.ResponseWriter, r *http.Request) {
	dishID := r.PathValue("id")
	report, err := a.c.Entities().Completeness(r.Context(), a.dishType, dishID)
	if err != nil {
		a.fail(w, "read completeness", err)
		return
	}
	if len(report.Missing) > 0 {
		missing := make([]string, 0, len(report.Missing))
		for _, m := range report.Missing {
			missing = append(missing, m.InternalName)
		}
		sort.Strings(missing)
		writeJSON(w, http.StatusUnprocessableEntity, map[string]any{
			"error": map[string]string{
				"message": "this dish is not ready for the menu: " + strings.Join(missing, ", "),
			},
			"missing": missing,
			"score":   report.Score,
		})
		return
	}
	if _, err := a.c.Values().Set(r.Context(), client.SetValueInput{
		AttributeDefinitionID: a.dishAttrs["status"], EntityID: dishID,
		TypeDefinitionID: a.dishType, Value: jsonString("on_menu"),
	}); err != nil {
		a.fail(w, "publish dish", err)
		return
	}
	dish, err := a.readDish(r.Context(), dishID, true)
	if err != nil {
		a.fail(w, "read dish", err)
		return
	}
	writeJSON(w, http.StatusOK, dish)
}

// --- the menu change ---------------------------------------------------------

// menuChangeInput schedules a set of price moves.
type menuChangeInput struct {
	Name string `json:"name"`
	// PublishAt is when the new prices take effect. Absent publishes now.
	PublishAt *time.Time `json:"publish_at"`
	// Prices are keyed by dish id, then channel.
	Prices map[string]map[string]string `json:"prices"`
}

// scheduleMenuChange stages price moves in a CHANGE SET and schedules it.
//
// The alternative is writing each price at the moment it should take effect,
// which needs somebody awake at 06:00 on Monday. A change set is approved
// ahead of time and published by the service, and every price in it moves in
// ONE transaction — a menu is never half-changed.
func (a *API) scheduleMenuChange(w http.ResponseWriter, r *http.Request) {
	var in menuChangeInput
	if err := decode(w, r, &in); err != nil {
		writeError(w, http.StatusBadRequest, "bad request body")
		return
	}
	if in.Name == "" || len(in.Prices) == 0 {
		writeError(w, http.StatusBadRequest, "name and at least one price are required")
		return
	}

	create := client.CreateChangeSetInput{Name: in.Name}
	if in.PublishAt != nil {
		at := in.PublishAt.UTC().Format(time.RFC3339)
		create.PublishAt = &at
	}
	cs, err := a.c.ChangeSets().Create(r.Context(), create)
	if err != nil {
		a.fail(w, "open the menu change", err)
		return
	}
	for dishID, byChannel := range in.Prices {
		for channel, price := range byChannel {
			if _, aerr := a.c.ChangeSets().AddMutation(r.Context(), cs.ID, client.Mutation{
				Kind:                  "set",
				AttributeDefinitionID: a.dishAttrs["price"],
				EntityID:              dishID,
				TypeDefinitionID:      a.dishType,
				Channel:               channel,
				Value:                 jsonString(price),
			}); aerr != nil {
				a.fail(w, "stage a price", aerr)
				return
			}
		}
	}
	if _, err := a.c.ChangeSets().Submit(r.Context(), cs.ID); err != nil {
		a.fail(w, "submit the menu change", err)
		return
	}
	approved, err := a.c.ChangeSets().Approve(r.Context(), cs.ID)
	if err != nil {
		a.fail(w, "approve the menu change", err)
		return
	}
	// With no publish_at the change goes live now; with one, the service's
	// scheduler publishes it.
	if in.PublishAt == nil {
		published, perr := a.c.ChangeSets().Publish(r.Context(), cs.ID)
		if perr != nil {
			a.fail(w, "publish the menu change", perr)
			return
		}
		writeJSON(w, http.StatusOK, published)
		return
	}
	writeJSON(w, http.StatusOK, approved)
}

func (a *API) listMenuChanges(w http.ResponseWriter, r *http.Request) {
	sets, err := a.c.ChangeSets().List(r.Context())
	if err != nil {
		a.fail(w, "list menu changes", err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": sets})
}

// --- cost history ------------------------------------------------------------

// costHistory reports what a dish cost at each of its revisions.
//
// A food cost that only exists as "now" cannot answer the question a chef
// actually asks — "what did this cost in January?" — and the answer is not
// reconstructable from today's prices.
func (a *API) costHistory(w http.ResponseWriter, r *http.Request) {
	dishID := r.PathValue("id")
	revisions, err := a.c.Entities().Revisions(r.Context(), a.dishType, dishID)
	if err != nil {
		a.fail(w, "list revisions", err)
		return
	}

	type point struct {
		Revision  string    `json:"revision"`
		Label     string    `json:"label,omitempty"`
		At        time.Time `json:"at"`
		FoodCost  string    `json:"food_cost,omitempty"`
		LineCount int       `json:"line_count"`
	}
	out := make([]point, 0, len(revisions))
	for _, rev := range revisions {
		values, verr := a.c.Entities().AsOf(r.Context(), a.dishType, dishID,
			rev.CreatedAt.Format(time.RFC3339Nano))
		if verr != nil {
			a.fail(w, "read the dish as of a revision", verr)
			return
		}
		p := point{Revision: rev.ID, Label: rev.Label, At: rev.CreatedAt}
		for _, v := range values {
			switch v.InternalName {
			// A revision snapshot renders every value as text, so these two
			// need no JSON decode.
			case "food_cost":
				p.FoodCost = v.Value
			case "line_count":
				p.LineCount = atoiOrZero(v.Value)
			}
		}
		out = append(out, p)
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": out})
}

// --- helpers -----------------------------------------------------------------

func (a *API) fail(w http.ResponseWriter, action string, err error) {
	var apiErr *client.APIError
	if errors.As(err, &apiErr) && apiErr.Status >= 400 && apiErr.Status < 600 {
		writeError(w, apiErr.Status, apiErr.Message)
		return
	}
	a.log.Error(action, "error", err)
	writeError(w, http.StatusInternalServerError, action+" failed")
}

func decode(w http.ResponseWriter, r *http.Request, dst any) error {
	dec := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<20))
	dec.DisallowUnknownFields()
	return dec.Decode(dst)
}

func writeJSON(w http.ResponseWriter, status int, body any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(body)
}

func writeError(w http.ResponseWriter, status int, message string) {
	writeJSON(w, status, map[string]any{"error": map[string]string{"message": message}})
}

func jsonString(s string) json.RawMessage {
	raw, err := json.Marshal(s)
	if err != nil {
		return json.RawMessage(`""`)
	}
	return raw
}

// The client hands back a value as raw JSON, which is the honest shape: a
// value's type is the SCHEMA's, not the transport's. Each reader below decodes
// what it expects and answers a zero value otherwise, so one unreadable value
// never fails a whole dish.

func asString(raw json.RawMessage) string {
	if len(raw) == 0 {
		return ""
	}
	var text string
	if err := json.Unmarshal(raw, &text); err == nil {
		return text
	}
	// A number or a boolean read as text rather than as nothing.
	return strings.Trim(string(raw), `"`)
}

func asBool(raw json.RawMessage) bool {
	var out bool
	_ = json.Unmarshal(raw, &out)
	return out
}

func asInt(raw json.RawMessage) int {
	var out int
	if err := json.Unmarshal(raw, &out); err != nil {
		return 0
	}
	return out
}

// asQuantity reads the magnitude-and-unit pair a quantity value carries.
// atoiOrZero reads an integer rendered as text.
func atoiOrZero(text string) int {
	out, err := strconv.Atoi(text)
	if err != nil {
		return 0
	}
	return out
}

// asQuantity reads the magnitude-and-unit pair a quantity value carries.
func asQuantity(raw json.RawMessage) *Quantity {
	var q Quantity
	if err := json.Unmarshal(raw, &q); err != nil || q.Magnitude == "" {
		return nil
	}
	return &q
}
