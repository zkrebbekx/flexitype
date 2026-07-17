// Package dedup finds probable duplicate entities: an operator declares
// matching rules (an attribute plus a comparison strategy) per type, and a
// scan reports candidate pairs with similarity scores. Detection is
// report-only — merging is out of scope. Scoring runs in Go over values
// loaded through the repository, so the memory and postgres backends agree
// exactly (no dependence on a database-specific similarity function).
package dedup

import (
	"context"
	"sort"
	"strings"
	"time"
	"unicode"

	domainattribute "github.com/zkrebbekx/flexitype/domain/attribute"
	domaintypedef "github.com/zkrebbekx/flexitype/domain/typedef"
	domainvalue "github.com/zkrebbekx/flexitype/domain/value"

	"github.com/zkrebbekx/flexitype/application/uow"
	domainerrors "github.com/zkrebbekx/flexitype/domain/errors"
	"github.com/zkrebbekx/flexitype/domain/valueobjects"
	"github.com/zkrebbekx/flexitype/pkg/db"
	"github.com/zkrebbekx/flexitype/pkg/ulid"
)

// Strategy is how two attribute values are compared for a match.
type Strategy string

const (
	// StrategyExact matches byte-identical values (score 1).
	StrategyExact Strategy = "exact"
	// StrategyCaseInsensitive matches values equal ignoring case and
	// surrounding whitespace (score 1).
	StrategyCaseInsensitive Strategy = "case_insensitive"
	// StrategyTrigram matches values whose trigram similarity meets the
	// rule threshold (score = similarity).
	StrategyTrigram Strategy = "trigram"
)

// maxScanEntities bounds a scan's comparison work at the entity count.
const maxScanEntities = 5000

// maxScanDuration is a soft budget for the trigram matching phase. When the
// inverted-index scan of a pathological, densely-overlapping set still runs
// long, the scan bails out gracefully with the matches found so far and marks
// the result truncated — it never silently caps the output.
const maxScanDuration = 5 * time.Second

// Rule declares how to detect duplicates for one attribute of a type.
type Rule struct {
	ID                    ulid.ID               `json:"id"`
	TenantID              valueobjects.TenantID `json:"tenant_id"`
	TypeDefinitionID      string                `json:"type_definition_id"`
	AttributeDefinitionID string                `json:"attribute_definition_id"`
	Strategy              Strategy              `json:"strategy"`
	// Threshold is the minimum trigram similarity (0..1); ignored by the
	// exact and case-insensitive strategies.
	Threshold float64   `json:"threshold"`
	CreatedAt time.Time `json:"created_at"`
}

// Dismissal records that an operator has judged a candidate pair not a
// duplicate; dismissed pairs never resurface on re-scan.
type Dismissal struct {
	RuleID   ulid.ID               `json:"rule_id"`
	TenantID valueobjects.TenantID `json:"tenant_id"`
	EntityA  string                `json:"entity_a"`
	EntityB  string                `json:"entity_b"`
}

// Candidate is one probable-duplicate pair.
type Candidate struct {
	EntityA string  `json:"entity_a"`
	EntityB string  `json:"entity_b"`
	ValueA  string  `json:"value_a"`
	ValueB  string  `json:"value_b"`
	Score   float64 `json:"score"`
}

// Store persists matching rules and dismissals, scoped by tenant.
type Store interface {
	CreateRule(ctx context.Context, r Rule) error
	GetRule(ctx context.Context, tenant valueobjects.TenantID, id ulid.ID) (Rule, error)
	ListRules(ctx context.Context, tenant valueobjects.TenantID, typeDefID string) ([]Rule, error)
	DeleteRule(ctx context.Context, tenant valueobjects.TenantID, id ulid.ID) error
	Dismiss(ctx context.Context, d Dismissal) error
	ListDismissals(ctx context.Context, tenant valueobjects.TenantID, ruleID ulid.ID) ([]Dismissal, error)
}

// Interactor implements the duplicate-detection usecases. It combines the
// pool-level rule store with request-scoped repositories used by scans.
type Interactor struct {
	store    Store
	typeDefs domaintypedef.Repository
	attrs    domainattribute.Repository
	values   domainvalue.Repository
	now      func() time.Time
}

// NewInteractor wires the duplicate-detection usecases.
func NewInteractor(
	store Store,
	typeDefs domaintypedef.Repository,
	attrs domainattribute.Repository,
	values domainvalue.Repository,
	now func() time.Time,
) *Interactor {
	if now == nil {
		now = time.Now
	}
	return &Interactor{store: store, typeDefs: typeDefs, attrs: attrs, values: values, now: now}
}

// CreateRuleInput carries a new rule's fields.
type CreateRuleInput struct {
	TypeDefinitionID      string
	AttributeDefinitionID string
	Strategy              Strategy
	Threshold             float64
}

// CreateRule validates and stores a matching rule. The attribute must
// belong to the type's schema, and a trigram rule needs a threshold in
// (0,1].
func (i *Interactor) CreateRule(ctx context.Context, in CreateRuleInput) (*Rule, error) {
	typeID, err := valueobjects.ParseTypeDefinitionID(in.TypeDefinitionID)
	if err != nil {
		return nil, domainerrors.NewValidation(err.Error())
	}
	attrID, err := valueobjects.ParseAttributeDefinitionID(in.AttributeDefinitionID)
	if err != nil {
		return nil, domainerrors.NewValidation(err.Error())
	}
	switch in.Strategy {
	case StrategyExact, StrategyCaseInsensitive:
	case StrategyTrigram:
		if in.Threshold <= 0 || in.Threshold > 1 {
			return nil, domainerrors.NewValidation("trigram threshold must be in (0,1]", "threshold", in.Threshold)
		}
	default:
		return nil, domainerrors.NewValidation("unknown match strategy", "strategy", string(in.Strategy))
	}

	t, err := i.typeDefs.Get(ctx, typeID)
	if err != nil {
		return nil, err
	}
	if err := uow.EnsureTenant(ctx, t.TenantID(), "type_definition", in.TypeDefinitionID); err != nil {
		return nil, err
	}
	attr, err := i.attrs.Get(ctx, attrID)
	if err != nil {
		return nil, err
	}
	if !attr.TypeDefinitionID().Equals(typeID) {
		return nil, domainerrors.NewValidation("attribute does not belong to the type", "attribute", in.AttributeDefinitionID)
	}

	rule := Rule{
		ID:                    ulid.New(),
		TenantID:              t.TenantID(),
		TypeDefinitionID:      in.TypeDefinitionID,
		AttributeDefinitionID: in.AttributeDefinitionID,
		Strategy:              in.Strategy,
		Threshold:             in.Threshold,
		CreatedAt:             i.now(),
	}
	if err := i.store.CreateRule(ctx, rule); err != nil {
		return nil, err
	}
	return &rule, nil
}

// ListRules returns the matching rules for a type.
func (i *Interactor) ListRules(ctx context.Context, rawTypeID string) ([]Rule, error) {
	tenant := uow.TenantFromContext(ctx)
	return i.store.ListRules(ctx, tenant, rawTypeID)
}

// DeleteRule removes a matching rule.
func (i *Interactor) DeleteRule(ctx context.Context, rawRuleID string) error {
	id, err := ulid.Parse(rawRuleID)
	if err != nil {
		return domainerrors.NewValidation(err.Error())
	}
	return i.store.DeleteRule(ctx, uow.TenantFromContext(ctx), id)
}

// Dismiss marks a candidate pair as not a duplicate.
func (i *Interactor) Dismiss(ctx context.Context, rawRuleID, entityA, entityB string) error {
	id, err := ulid.Parse(rawRuleID)
	if err != nil {
		return domainerrors.NewValidation(err.Error())
	}
	tenant := uow.TenantFromContext(ctx)
	if _, err := i.store.GetRule(ctx, tenant, id); err != nil {
		return err
	}
	a, b := canonicalPair(entityA, entityB)
	if a == "" || b == "" {
		return domainerrors.NewValidation("both entity ids are required")
	}
	return i.store.Dismiss(ctx, Dismissal{RuleID: id, TenantID: tenant, EntityA: a, EntityB: b})
}

// ScanOutput is a rule's candidate report.
type ScanOutput struct {
	RuleID     string      `json:"rule_id"`
	Strategy   Strategy    `json:"strategy"`
	Candidates []Candidate `json:"candidates"`
	Truncated  bool        `json:"truncated"`
}

// Scan runs a matching rule over the type's current entity values and
// returns the candidate duplicate pairs, highest score first, excluding
// dismissed pairs. It reads only committed data and never writes, so it is
// idempotent.
func (i *Interactor) Scan(ctx context.Context, rawRuleID string) (*ScanOutput, error) {
	id, err := ulid.Parse(rawRuleID)
	if err != nil {
		return nil, domainerrors.NewValidation(err.Error())
	}
	tenant := uow.TenantFromContext(ctx)
	rule, err := i.store.GetRule(ctx, tenant, id)
	if err != nil {
		return nil, err
	}
	attrID, err := valueobjects.ParseAttributeDefinitionID(rule.AttributeDefinitionID)
	if err != nil {
		return nil, domainerrors.NewValidation(err.Error())
	}

	entities, values, truncated, err := i.loadEntityValues(ctx, attrID)
	if err != nil {
		return nil, err
	}
	dismissed, err := i.dismissedSet(ctx, tenant, id)
	if err != nil {
		return nil, err
	}

	candidates, budgetHit := matchPairs(rule, entities, values, dismissed, i.now().Add(maxScanDuration), i.now)
	truncated = truncated || budgetHit
	sort.SliceStable(candidates, func(a, b int) bool {
		if candidates[a].Score != candidates[b].Score {
			return candidates[a].Score > candidates[b].Score
		}
		if candidates[a].EntityA != candidates[b].EntityA {
			return candidates[a].EntityA < candidates[b].EntityA
		}
		return candidates[a].EntityB < candidates[b].EntityB
	})
	return &ScanOutput{RuleID: id.String(), Strategy: rule.Strategy, Candidates: candidates, Truncated: truncated}, nil
}

// loadEntityValues collects the first live value each entity holds for the
// attribute, capped at maxScanEntities.
func (i *Interactor) loadEntityValues(ctx context.Context, attrID valueobjects.AttributeDefinitionID) (entities []string, values []string, truncated bool, err error) {
	seen := map[valueobjects.EntityID]bool{}
	page := db.Page{Limit: 500}
	for {
		batch, _, err := i.values.ListByDefinition(ctx, attrID, page)
		if err != nil {
			return nil, nil, false, err
		}
		for _, av := range batch {
			if seen[av.EntityID()] {
				continue
			}
			seen[av.EntityID()] = true
			if len(entities) >= maxScanEntities {
				return entities, values, true, nil
			}
			entities = append(entities, av.EntityID().String())
			values = append(values, av.Value().String())
		}
		// The repository over-fetches by one; a short page is the last one.
		if len(batch) <= page.Limit {
			break
		}
		page.Cursor = db.EncodeKeyset(batch[len(batch)-1].ID().String())
	}
	return entities, values, false, nil
}

// dismissedSet returns the dismissed pairs as canonical "a\x00b" keys.
func (i *Interactor) dismissedSet(ctx context.Context, tenant valueobjects.TenantID, ruleID ulid.ID) (map[string]bool, error) {
	list, err := i.store.ListDismissals(ctx, tenant, ruleID)
	if err != nil {
		return nil, err
	}
	set := make(map[string]bool, len(list))
	for _, d := range list {
		a, b := canonicalPair(d.EntityA, d.EntityB)
		set[a+"\x00"+b] = true
	}
	return set, nil
}

// matchPairs produces candidate pairs for a rule. Exact and case-insensitive
// strategies bucket by a normalized key (O(n)). The trigram strategy builds an
// inverted index (trigram -> the values containing it) and only compares pairs
// that share at least one trigram, so it never materializes the full O(n^2)
// pair space — pairs with no shared trigram score 0 and (with a threshold in
// (0,1]) could never be candidates anyway. It returns whether it stopped early
// on the soft time budget. deadline is ignored when zero.
func matchPairs(rule Rule, entities, values []string, dismissed map[string]bool, deadline time.Time, now func() time.Time) ([]Candidate, bool) {
	out := []Candidate{}
	add := func(ai, bi int, score float64) {
		a, b := canonicalPair(entities[ai], entities[bi])
		if dismissed[a+"\x00"+b] {
			return
		}
		va, vb := values[ai], values[bi]
		if a != entities[ai] {
			va, vb = vb, va
		}
		out = append(out, Candidate{EntityA: a, EntityB: b, ValueA: va, ValueB: vb, Score: score})
	}

	switch rule.Strategy {
	case StrategyExact, StrategyCaseInsensitive:
		buckets := map[string][]int{}
		for idx, v := range values {
			key := v
			if rule.Strategy == StrategyCaseInsensitive {
				key = strings.ToLower(strings.TrimSpace(v))
			}
			buckets[key] = append(buckets[key], idx)
		}
		for _, idxs := range buckets {
			for x := 0; x < len(idxs); x++ {
				for y := x + 1; y < len(idxs); y++ {
					add(idxs[x], idxs[y], 1)
				}
			}
		}
	case StrategyTrigram:
		// Extract each value's sorted trigram set and index which values hold
		// each trigram.
		grams := make([][]string, len(values))
		index := map[string][]int{}
		for idx, v := range values {
			g := trigrams(v)
			grams[idx] = g
			for _, t := range g {
				index[t] = append(index[t], idx)
			}
		}
		// For each value, the only possible partners are the ones sharing a
		// trigram; gather them from the index (keeping y > x so each unordered
		// pair is scored once) and Jaccard just those.
		partners := map[int]struct{}{}
		for x := 0; x < len(values); x++ {
			if len(grams[x]) == 0 {
				continue
			}
			if !deadline.IsZero() && x%512 == 0 && now().After(deadline) {
				return out, true
			}
			clear(partners)
			for _, t := range grams[x] {
				for _, y := range index[t] {
					if y > x {
						partners[y] = struct{}{}
					}
				}
			}
			for y := range partners {
				if score := jaccard(grams[x], grams[y]); score >= rule.Threshold {
					add(x, y, score)
				}
			}
		}
	}
	return out, false
}

// canonicalPair orders two entity ids so a pair has one representation
// regardless of comparison direction.
func canonicalPair(a, b string) (string, string) {
	if a <= b {
		return a, b
	}
	return b, a
}

// trigrams returns the sorted, de-duplicated padded 3-grams of s, lowercased.
// Each alphanumeric word is padded with two leading and one trailing space
// before extraction, so similar words share boundary trigrams. The result is
// sorted so jaccard can intersect two sets by a linear merge and so the
// inverted index sees each trigram once per value.
func trigrams(s string) []string {
	set := map[string]struct{}{}
	for _, word := range strings.FieldsFunc(strings.ToLower(s), func(r rune) bool {
		return !unicode.IsLetter(r) && !unicode.IsDigit(r)
	}) {
		padded := "  " + word + " "
		runes := []rune(padded)
		for k := 0; k+3 <= len(runes); k++ {
			set[string(runes[k:k+3])] = struct{}{}
		}
	}
	out := make([]string, 0, len(set))
	for g := range set {
		out = append(out, g)
	}
	sort.Strings(out)
	return out
}

// jaccard is the size of the intersection over the union of two sorted trigram
// sets, computed by a linear merge. A value with no trigrams (no alphanumeric
// content — punctuation or emoji only) is treated as matching nothing, so two
// content-free values do not score as a perfect duplicate.
func jaccard(a, b []string) float64 {
	if len(a) == 0 || len(b) == 0 {
		return 0
	}
	inter := 0
	for i, j := 0, 0; i < len(a) && j < len(b); {
		switch {
		case a[i] == b[j]:
			inter++
			i++
			j++
		case a[i] < b[j]:
			i++
		default:
			j++
		}
	}
	union := len(a) + len(b) - inter
	if union == 0 {
		return 0
	}
	return float64(inter) / float64(union)
}
