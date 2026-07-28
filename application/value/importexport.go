package value

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"strings"

	"github.com/zkrebbekx/flexitype/application/appctx"
	apptypedef "github.com/zkrebbekx/flexitype/application/typedef"
	"github.com/zkrebbekx/flexitype/application/uow"
	domainattribute "github.com/zkrebbekx/flexitype/domain/attribute"
	domaindependency "github.com/zkrebbekx/flexitype/domain/dependency"
	domainerrors "github.com/zkrebbekx/flexitype/domain/errors"
	domainvalue "github.com/zkrebbekx/flexitype/domain/value"
	"github.com/zkrebbekx/flexitype/domain/valueobjects"
	"github.com/zkrebbekx/flexitype/pkg/db"
)

// Import/export move whole entity sets as tabular data — the spreadsheet is
// how catalogs arrive. A row keys on an entity id column; other columns map
// to attributes. Every write flows through the normal value unit of work, so
// activity, events and the search index update exactly as single-value
// writes do.

// maxImportRows and maxExportRows bound one request's work.
const (
	maxImportRows = 5000
	maxExportRows = 10000
)

// importChunkSize is how many rows a best-effort import commits per unit of
// work. One transaction per chunk (rather than per row) amortizes the
// definition locks and the existing-value prefetch across the chunk while
// still bounding how much a single failure re-runs on the fallback path.
const importChunkSize = 100

// importCache memoizes the per-import lookups the value write path otherwise
// repeats for every cell. It is created per write transaction (a chunk, or the
// whole transactional import) and stashed on the context; setWithin consults it
// when present and keeps it consistent with its own writes. Absent, the normal
// single-write path runs unchanged.
type importCache struct {
	// defs holds definitions locked (GetForUpdate) within THIS transaction, so
	// a shared column is locked once per chunk rather than once per row.
	defs map[string]*domainattribute.Definition
	// deps memoizes each definition's incoming dependencies, keyed by target
	// attribute id. Definitions and dependencies are immutable during an import.
	deps map[string][]*domaindependency.Dependency
	// existing holds every live value of each entity touched in THIS
	// transaction, seeded by one ListByEntities and then kept in step with the
	// values setWithin writes. A non-nil map switches the write path off its
	// per-cell FindByDefinitionAndEntity query.
	existing map[string][]*domainvalue.AttributeValue
}

func newImportCache() *importCache {
	return &importCache{
		defs: map[string]*domainattribute.Definition{},
		deps: map[string][]*domaindependency.Dependency{},
	}
}

// prefetch seeds the existing-value cache with one query for every entity the
// chunk touches, so the write path reads them from memory instead of a query
// per cell.
func (c *importCache) prefetch(ctx context.Context, reads appctx.ValueReader, tenant valueobjects.TenantID, entityIDs []valueobjects.EntityID) error {
	c.existing = map[string][]*domainvalue.AttributeValue{}
	if len(entityIDs) == 0 {
		return nil
	}
	vals, err := reads.ListByEntities(ctx, tenant, entityIDs)
	if err != nil {
		return fmt.Errorf("prefetch existing values: %w", err)
	}
	for _, av := range vals {
		key := av.EntityID().String()
		c.existing[key] = append(c.existing[key], av)
	}
	return nil
}

type importCacheKey struct{}

// withImportCache stashes the per-transaction import cache on the context.
func withImportCache(ctx context.Context, c *importCache) context.Context {
	return context.WithValue(ctx, importCacheKey{}, c)
}

// importCacheFromContext returns the active import cache, or nil for the normal
// single-write path.
func importCacheFromContext(ctx context.Context) *importCache {
	c, _ := ctx.Value(importCacheKey{}).(*importCache)
	return c
}

// preparedRow is one import row whose cells have converted cleanly to value
// inputs, ready to write.
type preparedRow struct {
	row    int
	inputs []SetInput
}

// entity reports the row's entity id, which is the canonical lock-ordering
// key. Every input on one CSV row names the same entity.
func (p preparedRow) entity() string {
	if len(p.inputs) == 0 {
		return ""
	}
	return p.inputs[0].EntityID
}

// errRowValid is the sentinel a dry-run row returns to force its unit of
// work to roll back after validating cleanly — nothing is written, but the
// row exercised the full validation path (types, constraints, dependencies,
// uniqueness) against committed data.
var errRowValid = errors.New("dry-run row validated")

// ImportMode selects how a commit handles invalid rows.
type ImportMode string

const (
	// ImportBestEffort writes every valid row and reports the rest.
	ImportBestEffort ImportMode = "best_effort"
	// ImportTransactional writes all rows or none: any invalid row aborts
	// the whole import and nothing is written.
	ImportTransactional ImportMode = "transactional"
)

// ImportInput describes a tabular import against one type.
type ImportInput struct {
	TypeDefinitionID string
	// KeyColumn names the CSV column holding each row's entity id.
	KeyColumn string
	// Mapping maps a CSV column name to an attribute internal name.
	Mapping map[string]string
	// Columns is the CSV header, in order; Rows are the data rows.
	Columns []string
	Rows    [][]string
	Mode    ImportMode
	// DryRun validates every row and writes nothing.
	DryRun bool
}

// ImportError points at one rejected cell (or row).
type ImportError struct {
	Row       int    `json:"row"`
	Column    string `json:"column,omitempty"`
	Attribute string `json:"attribute,omitempty"`
	Reason    string `json:"reason"`
}

// ImportReport summarizes an import run.
type ImportReport struct {
	RowsTotal   int           `json:"rows_total"`
	RowsValid   int           `json:"rows_valid"`
	RowsWritten int           `json:"rows_written"`
	DryRun      bool          `json:"dry_run"`
	Mode        ImportMode    `json:"mode"`
	Errors      []ImportError `json:"errors"`
}

// mappedColumn is a resolved CSV column bound to an attribute.
type mappedColumn struct {
	index    int
	column   string
	attrID   string
	attrName string
	dataType valueobjects.DataType
	required bool
}

// Import loads tabular rows into a type's entities. It resolves the column
// mapping against the type's effective schema, converts every row's cells,
// then either reports (dry run), writes the valid rows (best effort) or writes
// all rows atomically (transactional, refusing the whole set if any row is
// invalid).
//
// Cell conversion is a single pure-Go pass (no database). The write path then
// diverges by mode:
//
//   - dry run keeps the original per-row rollback validation, so each row is
//     checked against committed data independently (a preview never lets one
//     row's would-be write mask another's), and nothing is written;
//   - best effort commits rows in chunk-sized transactions, falling back to
//     per-row transactions for a chunk that fails so every writable row is
//     still written and only the bad rows are reported;
//   - transactional stays one logical unit — the whole import is a single
//     transaction, so any bad row rolls the whole set back — preserving its
//     all-or-nothing semantics.
//
// RowsValid is the number of rows that produced no error; RowsWritten the
// number persisted.
func (i *Interactor) Import(ctx context.Context, in ImportInput) (*ImportReport, error) {
	if len(in.Rows) > maxImportRows {
		return nil, domainerrors.NewValidation("import exceeds the maximum row count", "max", maxImportRows)
	}
	mode := in.Mode
	if mode == "" {
		mode = ImportBestEffort
	}
	if mode != ImportBestEffort && mode != ImportTransactional {
		return nil, domainerrors.NewValidation("unknown import mode", "mode", string(mode))
	}

	cols, keyIdx, err := i.resolveMapping(ctx, in)
	if err != nil {
		return nil, err
	}

	report := &ImportReport{RowsTotal: len(in.Rows), DryRun: in.DryRun, Mode: mode, Errors: []ImportError{}}

	// Pure-Go pass: convert every row's cells to value inputs, collecting
	// cell-level errors without a single database round-trip. erroredRows
	// tracks which rows failed anywhere, so RowsValid = total - failed.
	erroredRows := map[int]bool{}
	valid := make([]preparedRow, 0, len(in.Rows))
	for r, row := range in.Rows {
		rowNum := r + 1 // 1-based; header is row 0 for humans
		inputs, cellErrs := i.rowInputs(in.TypeDefinitionID, cols, keyIdx, rowNum, row)
		if len(cellErrs) > 0 {
			report.Errors = append(report.Errors, cellErrs...)
			erroredRows[rowNum] = true
			continue
		}
		valid = append(valid, preparedRow{row: rowNum, inputs: inputs})
	}

	if in.DryRun {
		// Validate each convertible row against committed data in its own
		// rollback unit of work, exactly as before, so the preview is
		// independent per row and writes nothing.
		for _, p := range valid {
			if err := i.applyRow(ctx, p.inputs, false); err != nil {
				report.Errors = append(report.Errors, importErrorFrom(p.row, err))
				erroredRows[p.row] = true
			}
		}
		report.RowsValid = len(in.Rows) - len(erroredRows)
		return report, nil
	}

	tenant := uow.TenantFromContext(ctx)
	switch mode {
	case ImportTransactional:
		i.importTransactional(ctx, tenant, valid, erroredRows, report)
	case ImportBestEffort:
		i.importBestEffort(ctx, tenant, valid, erroredRows, report)
	}
	report.RowsValid = len(in.Rows) - len(erroredRows)
	return report, nil
}

// importTransactional writes every row in ONE transaction (one logical unit):
// a cell error anywhere, or a write failure on any row, leaves nothing written,
// preserving the mode's all-or-nothing semantics. Validation is folded into the
// write — there is no separate re-validation pass.
func (i *Interactor) importTransactional(ctx context.Context, tenant valueobjects.TenantID, valid []preparedRow, erroredRows map[int]bool, report *ImportReport) {
	if len(report.Errors) > 0 {
		return // a cell error already refuses the whole set; write nothing
	}
	failedRow := 0
	err := i.uow.Execute(ctx, func(tx db.Transactor, c *uow.Collector) error {
		cache := newImportCache()
		cctx := withImportCache(ctx, cache)
		if err := cache.prefetch(cctx, i.values.WithTx(tx).(appctx.ValueReader), tenant, preparedEntityIDs(valid)); err != nil {
			return err
		}
		// Canonical entity order (see lockorder.go): CSV rows arrive in
		// whatever order the file has, so two imports over the same entities
		// otherwise take the entity-summary rows in opposite order. The row
		// number an error reports is still the file's.
		for _, idx := range canonicalOrder(valid, func(p preparedRow) string { return p.entity() }) {
			p := valid[idx]
			for _, item := range p.inputs {
				if _, err := i.setWithin(cctx, tx, c, item); err != nil {
					failedRow = p.row
					return err
				}
			}
		}
		return nil
	})
	if err != nil {
		report.Errors = append(report.Errors, importErrorFrom(failedRow, err))
		if failedRow != 0 {
			erroredRows[failedRow] = true
		}
		return // atomic: the transaction rolled back, nothing written
	}
	report.RowsWritten = len(valid)
}

// importBestEffort commits rows in chunk-sized transactions. A chunk that
// commits cleanly writes all its rows at once (amortizing definition locks and
// the existing-value prefetch); a chunk that fails rolls back as a unit and is
// re-run row by row, so every writable row is still written and only the bad
// rows are reported — matching the per-row semantics best effort had before.
func (i *Interactor) importBestEffort(ctx context.Context, tenant valueobjects.TenantID, valid []preparedRow, erroredRows map[int]bool, report *ImportReport) {
	for start := 0; start < len(valid); start += importChunkSize {
		end := min(start+importChunkSize, len(valid))
		chunk := valid[start:end]
		if err := i.writeChunk(ctx, tenant, chunk); err == nil {
			report.RowsWritten += len(chunk)
			continue
		}
		// The chunk rolled back atomically; fall back to per-row transactions so
		// good rows still land and only the offending rows are reported.
		for _, p := range chunk {
			if err := i.applyRow(ctx, p.inputs, true); err != nil {
				report.Errors = append(report.Errors, importErrorFrom(p.row, err))
				erroredRows[p.row] = true
				continue
			}
			report.RowsWritten++
		}
	}
}

// writeChunk applies every row of a chunk in one unit of work with a shared
// import cache. On any failure the transaction rolls back and the error is
// returned so the caller can fall back to per-row writes.
func (i *Interactor) writeChunk(ctx context.Context, tenant valueobjects.TenantID, chunk []preparedRow) error {
	return i.uow.Execute(ctx, func(tx db.Transactor, c *uow.Collector) error {
		cache := newImportCache()
		cctx := withImportCache(ctx, cache)
		if err := cache.prefetch(cctx, i.values.WithTx(tx).(appctx.ValueReader), tenant, preparedEntityIDs(chunk)); err != nil {
			return err
		}
		// Canonical entity order, as in importTransactional.
		for _, idx := range canonicalOrder(chunk, func(p preparedRow) string { return p.entity() }) {
			for _, item := range chunk[idx].inputs {
				if _, err := i.setWithin(cctx, tx, c, item); err != nil {
					return err
				}
			}
		}
		return nil
	})
}

// preparedEntityIDs returns the distinct, parseable entity ids a set of rows
// touches — the keys to prefetch existing values for. An unparseable id is
// skipped here; setWithin surfaces the validation error when it writes the row.
func preparedEntityIDs(rows []preparedRow) []valueobjects.EntityID {
	seen := map[string]bool{}
	ids := make([]valueobjects.EntityID, 0, len(rows))
	for _, p := range rows {
		for _, item := range p.inputs {
			if seen[item.EntityID] {
				continue
			}
			seen[item.EntityID] = true
			if id, err := valueobjects.ParseEntityID(item.EntityID); err == nil {
				ids = append(ids, id)
			}
		}
	}
	return ids
}

// resolveMapping binds each mapped CSV column to an attribute in the type's
// effective schema and locates the key column.
func (i *Interactor) resolveMapping(ctx context.Context, in ImportInput) ([]mappedColumn, int, error) {
	typeID, err := valueobjects.ParseTypeDefinitionID(in.TypeDefinitionID)
	if err != nil {
		return nil, 0, domainerrors.NewValidation(err.Error())
	}
	t, err := i.typeDefs.Get(ctx, typeID)
	if err != nil {
		return nil, 0, err
	}
	if err := uow.EnsureTenant(ctx, t.TenantID(), "type_definition", in.TypeDefinitionID); err != nil {
		return nil, 0, err
	}
	if in.KeyColumn == "" {
		return nil, 0, domainerrors.NewValidation("key_column is required")
	}

	// Attribute internal name -> (id, data type) across the inheritance chain.
	chain, err := apptypedef.Chain(ctx, i.typeDefs, t)
	if err != nil {
		return nil, 0, err
	}
	byName := map[string]mappedColumn{}
	for _, link := range chain {
		attrs, err := domainattribute.ListAllForType(ctx, i.attrs, link.ID())
		if err != nil {
			return nil, 0, err
		}
		for _, a := range attrs {
			s := a.Snapshot()
			if _, seen := byName[s.InternalName]; seen {
				continue
			}
			byName[s.InternalName] = mappedColumn{attrID: s.ID.String(), attrName: s.InternalName, dataType: s.DataType, required: s.Required}
		}
	}

	colIndex := map[string]int{}
	for idx, c := range in.Columns {
		colIndex[c] = idx
	}
	keyIdx, ok := colIndex[in.KeyColumn]
	if !ok {
		return nil, 0, domainerrors.NewValidation("key column not present in the header", "key_column", in.KeyColumn)
	}

	cols := make([]mappedColumn, 0, len(in.Mapping))
	for column, attrName := range in.Mapping {
		idx, ok := colIndex[column]
		if !ok {
			return nil, 0, domainerrors.NewValidation("mapped column not present in the header", "column", column)
		}
		m, ok := byName[attrName]
		if !ok {
			return nil, 0, domainerrors.NewValidation("mapped attribute not in the type schema", "attribute", attrName)
		}
		m.index = idx
		m.column = column
		cols = append(cols, m)
	}
	if len(cols) == 0 {
		// An import with nothing mapped used to walk every row, write no
		// value, and report them all as written — a silent no-op dressed as a
		// successful load.
		return nil, 0, domainerrors.NewValidation(
			"no columns are mapped to attributes, so the import would write nothing")
	}
	return cols, keyIdx, nil
}

// rowInputs turns one CSV row into value SetInputs. Empty cells are skipped
// (no value written). Cell-conversion failures return per-cell errors and no
// inputs, so the row is reported without a database round-trip.
func (i *Interactor) rowInputs(typeID string, cols []mappedColumn, keyIdx, rowNum int, row []string) ([]SetInput, []ImportError) {
	entityID := ""
	if keyIdx < len(row) {
		entityID = row[keyIdx]
	}
	if entityID == "" {
		return nil, []ImportError{{Row: rowNum, Reason: "missing entity id (key column is empty)"}}
	}

	var inputs []SetInput
	var errs []ImportError
	for _, c := range cols {
		if c.index >= len(row) {
			continue
		}
		cell := row[c.index]
		if cell == "" {
			// A blank cell writes no value; if the target attribute is
			// required, that is a per-row error (the normal write path would
			// reject it too), not a silent skip.
			if c.required {
				errs = append(errs, ImportError{Row: rowNum, Column: c.column, Attribute: c.attrName, Reason: "value is required"})
			}
			continue
		}
		// A cell holding several values — a multi-valued attribute, or scoped
		// variants — arrives as the JSON array the export writes. Each member
		// becomes its own write, with its own scope.
		if entries, ok := scopedCell(cell, c.dataType); ok {
			for _, e := range entries {
				raw, err := cellToRaw(c.dataType, e.text())
				if err != nil {
					errs = append(errs, ImportError{Row: rowNum, Column: c.column, Attribute: c.attrName, Reason: err.Error()})
					continue
				}
				inputs = append(inputs, SetInput{
					AttributeDefinitionID: c.attrID,
					EntityID:              entityID,
					TypeDefinitionID:      typeID,
					Locale:                e.Locale,
					Channel:               e.Channel,
					Value:                 raw,
				})
			}
			continue
		}
		raw, err := cellToRaw(c.dataType, cell)
		if err != nil {
			errs = append(errs, ImportError{Row: rowNum, Column: c.column, Attribute: c.attrName, Reason: err.Error()})
			continue
		}
		inputs = append(inputs, SetInput{
			AttributeDefinitionID: c.attrID,
			EntityID:              entityID,
			TypeDefinitionID:      typeID,
			Value:                 raw,
		})
	}
	if len(errs) > 0 {
		return nil, errs
	}
	return inputs, nil
}

// applyRow writes a row's cells in one unit of work. When commit is false it
// validates then rolls back (dry run). A nil return with commit==false means
// the row is valid.
func (i *Interactor) applyRow(ctx context.Context, inputs []SetInput, commit bool) error {
	if len(inputs) == 0 {
		return nil
	}
	err := i.uow.Execute(ctx, func(tx db.Transactor, c *uow.Collector) error {
		for _, in := range inputs {
			if _, err := i.setWithin(ctx, tx, c, in); err != nil {
				return err
			}
		}
		if !commit {
			return errRowValid
		}
		return nil
	})
	if err != nil && !errors.Is(err, errRowValid) {
		return err
	}
	return nil
}

// importErrorFrom labels an error with its row for the report.
func importErrorFrom(row int, err error) ImportError {
	return ImportError{Row: row, Reason: err.Error()}
}

// cellToRaw renders a CSV cell as the raw JSON scalar ParseValue expects for
// the attribute's data type, inverting Value.String().
// cellEntry is one member of a multi-value cell: a value plus its scope.
type cellEntry struct {
	Value   json.RawMessage `json:"value"`
	Locale  string          `json:"locale,omitempty"`
	Channel string          `json:"channel,omitempty"`
}

// text renders the member as the cell text cellToRaw decodes. A JSON string
// is unquoted, so a plain value reads the way it would in its own cell.
func (e cellEntry) text() string {
	var plain string
	if err := json.Unmarshal(e.Value, &plain); err == nil {
		return plain
	}
	return string(e.Value)
}

// multiValueCellPrefix marks a multi-value cell OUT OF BAND.
//
// Every in-band sentinel drawn from the JSON grammar can be forged by a JSON
// payload. The format was a bare array of {"value",…} objects; retagging it
// as {"values":[…]} moved WHICH documents collide rather than whether they
// can, and the tagged shape is exactly what an earlier export of a json
// column looks like — so re-importing this tool's own output turned one
// document into two writes to a single-valued attribute, kept the last, and
// reported one row written with zero errors.
//
// A '#' cannot begin a JSON document, so a cell carrying this prefix is never
// a value and a value never carries it. The marker therefore works for a
// json column too, which no in-band form can.
const multiValueCellPrefix = "#flexitype-values:"

// scopedCell decodes a cell holding several values.
//
// It requires the TAGGED form, {"values":[…]}. The format was a bare array of
// {"value","locale","channel"} objects, which is a perfectly ordinary JSON
// payload — so a json-typed column round-tripping
// `[{"value":{"x":1}},{"value":{"y":2}}]` was read as two scoped members and
// stored as the last one, with no error reported. A cell that is not the
// tagged form is one value, whatever its shape.
//
// The untagged form is still ACCEPTED on import, so a file exported by an
// earlier release still loads.
//
// NEITHER form is looked for on a json column. See below: that is what makes
// the exclusion effective, rather than leaving it behind a branch the tagged
// form never reaches.
func scopedCell(cell string, dt valueobjects.DataType) ([]cellEntry, bool) {
	// A json cell is ALWAYS exactly one value, so the multi-value format has
	// no meaning there and is not looked for. Any in-band sentinel drawn from
	// the JSON grammar can be forged by a JSON payload: the first format was
	// a bare array, retagging it to {"values":[…]} only moved WHICH documents
	// collide — and `{"values":[{"value":…},…]}` is what an earlier export of
	// a json column looks like, so re-importing this tool's own output was
	// enough to reproduce it. One document became two writes to a
	// single-valued attribute, the second overwrote the first, and the report
	// said one row written with zero errors.
	trimmed := strings.TrimSpace(cell)
	if rest, found := strings.CutPrefix(trimmed, multiValueCellPrefix); found {
		return decodeCellEntries(rest)
	}
	// Below here are the LEGACY in-band forms, kept so a file an earlier
	// release exported still loads. Neither is looked for on a json column:
	// both are shapes a json document can have, and reading a document as a
	// member list is the silent corruption this prefix exists to end.
	if dt == valueobjects.DataTypeJSON {
		return nil, false
	}
	if strings.HasPrefix(trimmed, "{") {
		// The LEGACY tagged key, still read so a file an earlier release
		// exported still imports.
		var tagged struct {
			Values []cellEntry `json:"values"`
		}
		if err := json.Unmarshal([]byte(trimmed), &tagged); err != nil || len(tagged.Values) == 0 {
			return nil, false
		}
		for _, e := range tagged.Values {
			if len(e.Value) == 0 {
				return nil, false
			}
		}
		return tagged.Values, true
	}
	if !strings.HasPrefix(trimmed, "[") {
		return nil, false
	}
	return decodeCellEntries(trimmed)
}

// decodeCellEntries reads a JSON array of members, refusing one with a member
// that carries no value.
func decodeCellEntries(raw string) ([]cellEntry, bool) {
	var entries []cellEntry
	if err := json.Unmarshal([]byte(strings.TrimSpace(raw)), &entries); err != nil {
		return nil, false
	}
	for _, e := range entries {
		if len(e.Value) == 0 {
			return nil, false
		}
	}
	return entries, len(entries) > 0
}

func cellToRaw(dt valueobjects.DataType, cell string) (json.RawMessage, error) {
	switch dt {
	case valueobjects.DataTypeBool:
		b, err := strconv.ParseBool(cell)
		if err != nil {
			return nil, domainerrors.NewValidation("expected a boolean (true/false)", "got", cell)
		}
		return json.Marshal(b)
	case valueobjects.DataTypeInteger:
		n, err := strconv.ParseInt(cell, 10, 64)
		if err != nil {
			return nil, domainerrors.NewValidation("expected an integer", "got", cell)
		}
		return json.Marshal(n)
	case valueobjects.DataTypeFloat:
		f, err := strconv.ParseFloat(cell, 64)
		if err != nil {
			return nil, domainerrors.NewValidation("expected a number", "got", cell)
		}
		return json.Marshal(f)
	case valueobjects.DataTypeJSON, valueobjects.DataTypeMedia, valueobjects.DataTypeQuantity:
		// A quantity used to fall through to the default arm and arrive as
		// the quoted string "10 kg", which the quantity decoder rejects. The
		// export now writes the JSON object the values API accepts, and this
		// arm reads it back.
		if !json.Valid([]byte(cell)) {
			return nil, domainerrors.NewValidation("expected a JSON document", "got", cell)
		}
		return json.RawMessage(cell), nil
	default:
		// string, enum, decimal, url, email, date, time, datetime: a quoted
		// JSON string; ParseValue enforces the type-specific format.
		return json.Marshal(cell)
	}
}

// ExportInput describes a tabular export of a type's entities.
type ExportInput struct {
	TypeDefinitionID string
	// Attributes are the internal names to emit as columns, in order. Empty
	// exports the type's full effective schema.
	Attributes []string
	// EntityIDs, when set, restricts the export to those entities (e.g. an
	// FQL result set); otherwise all live entities of the type are exported.
	EntityIDs []string
	// KeyColumn names the entity-id column (defaults to "entity_id"). It is
	// always the first column, so an export re-imports unchanged.
	KeyColumn string
}

// ExportOutput is the tabular result: a header and rows of string cells.
type ExportOutput struct {
	Columns []string
	Rows    [][]string
}

// Export renders a type's entities as tabular data. The first column is the
// entity id; the rest are the chosen attributes.
//
// The output re-imports through Import unchanged, which is why a cell is not
// simply Value.String: that is a DISPLAY rendering, and it wrote a quantity
// as "10 kg" and a media value as a bare object key, neither of which the
// importer can decode. A quantity, media or JSON cell carries the same JSON
// the values API accepts.
//
// An attribute holding several values for one entity — multi-valued, or
// localized/channel-scoped variants — is one JSON array cell, each member
// carrying its own scope. It used to be one cell assigned per
// (entity, attribute), so the last row written won and the rest were dropped
// with no error.
func (i *Interactor) Export(ctx context.Context, in ExportInput) (*ExportOutput, error) {
	typeID, err := valueobjects.ParseTypeDefinitionID(in.TypeDefinitionID)
	if err != nil {
		return nil, domainerrors.NewValidation(err.Error())
	}
	t, err := i.typeDefs.Get(ctx, typeID)
	if err != nil {
		return nil, err
	}
	if err := uow.EnsureTenant(ctx, t.TenantID(), "type_definition", in.TypeDefinitionID); err != nil {
		return nil, err
	}
	tenant := t.TenantID()

	// Resolve the attribute columns (internal name -> id), in the requested
	// order or the full effective schema.
	chain, err := apptypedef.Chain(ctx, i.typeDefs, t)
	if err != nil {
		return nil, err
	}
	type attrCol struct {
		name string
		id   valueobjects.AttributeDefinitionID
	}
	byName := map[string]valueobjects.AttributeDefinitionID{}
	var order []string
	for _, link := range chain {
		attrs, err := domainattribute.ListAllForType(ctx, i.attrs, link.ID())
		if err != nil {
			return nil, err
		}
		for _, a := range attrs {
			s := a.Snapshot()
			if _, seen := byName[s.InternalName]; seen {
				continue
			}
			byName[s.InternalName] = s.ID
			order = append(order, s.InternalName)
		}
	}
	// Enforce field-level read permissions: drop attributes the principal may
	// not read so they are neither exported by default nor addressable by name
	// (an explicit unreadable column resolves as "not in the type schema").
	i.dropUnreadable(ctx, byName)
	readableOrder := order[:0]
	for _, name := range order {
		if _, ok := byName[name]; ok {
			readableOrder = append(readableOrder, name)
		}
	}
	order = readableOrder
	var cols []attrCol
	if len(in.Attributes) > 0 {
		for _, name := range in.Attributes {
			id, ok := byName[name]
			if !ok {
				return nil, domainerrors.NewValidation("attribute not in the type schema", "attribute", name)
			}
			cols = append(cols, attrCol{name: name, id: id})
		}
	} else {
		for _, name := range order {
			cols = append(cols, attrCol{name: name, id: byName[name]})
		}
	}

	entityIDs, err := i.exportEntityIDs(ctx, tenant, typeID, in.EntityIDs)
	if err != nil {
		return nil, err
	}

	key := in.KeyColumn
	if key == "" {
		key = "entity_id"
	}
	out := &ExportOutput{Columns: make([]string, 0, len(cols)+1)}
	out.Columns = append(out.Columns, key)
	for _, c := range cols {
		out.Columns = append(out.Columns, c.name)
	}

	// Batch the value load: one query per chunk instead of one per entity (up
	// to 10000). Group results by entity id, then emit rows in request order.
	ids := make([]valueobjects.EntityID, 0, len(entityIDs))
	for _, eid := range entityIDs {
		id, err := valueobjects.ParseEntityID(eid)
		if err != nil {
			return nil, domainerrors.NewValidation(err.Error())
		}
		ids = append(ids, id)
	}
	// Collect EVERY value per (entity, attribute), not one.
	//
	// The cell used to be assigned rather than accumulated, so a multi-valued
	// attribute kept whichever row came last and every locale/channel variant
	// overwrote the others. The export silently dropped data while its doc
	// promised the output re-imports unchanged.
	byEntity := make(map[string]map[valueobjects.AttributeDefinitionID][]*domainvalue.AttributeValue, len(entityIDs))
	if err := i.forEachValueBatched(ctx, tenant, ids, func(av *domainvalue.AttributeValue) {
		eid := av.EntityID().String()
		cells := byEntity[eid]
		if cells == nil {
			cells = map[valueobjects.AttributeDefinitionID][]*domainvalue.AttributeValue{}
			byEntity[eid] = cells
		}
		cells[av.AttributeDefinitionID()] = append(cells[av.AttributeDefinitionID()], av)
	}); err != nil {
		return nil, err
	}
	for _, eid := range entityIDs {
		byAttr := byEntity[eid]
		row := make([]string, 0, len(cols)+1)
		row = append(row, eid)
		for _, c := range cols {
			row = append(row, exportCell(byAttr[c.id]))
		}
		out.Rows = append(out.Rows, row)
	}
	return out, nil
}

// exportCell renders one (entity, attribute) cell so that importing it back
// reproduces what was exported.
//
// Value.String is a DISPLAY rendering: it writes a quantity as "10 kg" and a
// media value as a bare object key, neither of which the importer can decode —
// "10 kg" reaches the default arm as a quoted string the quantity decoder
// rejects, and a bare key fails the media arm's JSON check. A cell now carries
// the same JSON shape the values API accepts.
//
// Several values for one attribute — a multi-valued attribute, or scoped
// variants — become a JSON array, so nothing is dropped.
func exportCell(vals []*domainvalue.AttributeValue) string {
	switch len(vals) {
	case 0:
		return ""
	case 1:
		if vals[0].Scope().IsZero() {
			return exportScalar(vals[0].Value())
		}
	}
	parts := make([]json.RawMessage, 0, len(vals))
	for _, v := range vals {
		parts = append(parts, exportScoped(v))
	}
	// Marked OUT OF BAND with a prefix a JSON document cannot begin with.
	//
	// Any in-band shape is forgeable by a payload: a bare array of
	// {"value",…} objects is ordinary JSON, and so is {"values":[…]} — which
	// is what this function used to write, so re-importing an export of a
	// json column read one document as two members and kept the last,
	// silently, with zero errors reported.
	raw, err := json.Marshal(parts)
	if err != nil {
		return ""
	}
	return multiValueCellPrefix + string(raw)
}

// exportScalar renders one unscoped value as the JSON the importer accepts.
func exportScalar(v valueobjects.Value) string {
	switch v.DataType() {
	case valueobjects.DataTypeQuantity, valueobjects.DataTypeMedia, valueobjects.DataTypeJSON:
		raw, err := v.MarshalJSON()
		if err != nil {
			return v.String()
		}
		return string(raw)
	default:
		// Everything else round-trips through its plain text form, which is
		// what a person expects to see in a spreadsheet.
		return v.String()
	}
}

// exportScoped renders one value with its scope, for a cell holding several.
func exportScoped(av *domainvalue.AttributeValue) json.RawMessage {
	entry := map[string]any{}
	raw, err := av.Value().MarshalJSON()
	if err != nil {
		entry["value"] = av.Value().String()
	} else {
		entry["value"] = json.RawMessage(raw)
	}
	if l := av.Scope().Locale; l != "" {
		entry["locale"] = l
	}
	if c := av.Scope().Channel; c != "" {
		entry["channel"] = c
	}
	out, err := json.Marshal(entry)
	if err != nil {
		return json.RawMessage(`null`)
	}
	return out
}

// exportEntityIDs returns the entity ids to export: the explicit set when
// given, else a capped page of the type's live entities.
func (i *Interactor) exportEntityIDs(
	ctx context.Context,
	tenant valueobjects.TenantID,
	typeID valueobjects.TypeDefinitionID,
	explicit []string,
) ([]string, error) {
	if len(explicit) > 0 {
		if len(explicit) > maxExportRows {
			return nil, domainerrors.NewValidation("export exceeds the maximum row count", "max", maxExportRows)
		}
		return explicit, nil
	}
	var ids []string
	// An export is a full sweep, so it pages on the immutable key: ordering
	// newest-first meant an entity written mid-export jumped ahead of the
	// cursor and was left out of the file, silently.
	page := db.Page{Limit: 500, Stable: true}
	for {
		summaries, _, err := i.reads.ListEntities(ctx, tenant, []valueobjects.TypeDefinitionID{typeID}, page)
		if err != nil {
			return nil, err
		}
		for _, s := range summaries {
			ids = append(ids, s.EntityID.String())
		}
		if len(ids) >= maxExportRows || len(summaries) <= page.Limit {
			break
		}
		page.Cursor = db.EncodeKeyset(summaries[len(summaries)-1].EntityID.String())
	}
	if len(ids) > maxExportRows {
		ids = ids[:maxExportRows]
	}
	return ids, nil
}
