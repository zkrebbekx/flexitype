package value

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"

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
		for _, p := range valid {
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
		for _, p := range chunk {
			for _, item := range p.inputs {
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
		attrs, _, err := i.attrs.ListByTypeDefinition(ctx, link.ID(), db.Page{Limit: 500})
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
	case valueobjects.DataTypeJSON, valueobjects.DataTypeMedia:
		if !json.Valid([]byte(cell)) {
			return nil, domainerrors.NewValidation("expected a JSON document")
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
// entity id; the rest are the chosen attributes rendered via Value.String,
// so the output re-imports through Import unchanged.
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
		attrs, _, err := i.attrs.ListByTypeDefinition(ctx, link.ID(), db.Page{Limit: 500})
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
	byEntity := make(map[string]map[valueobjects.AttributeDefinitionID]string, len(entityIDs))
	if err := i.forEachValueBatched(ctx, tenant, ids, func(av *domainvalue.AttributeValue) {
		eid := av.EntityID().String()
		cells := byEntity[eid]
		if cells == nil {
			cells = map[valueobjects.AttributeDefinitionID]string{}
			byEntity[eid] = cells
		}
		cells[av.AttributeDefinitionID()] = av.Value().String()
	}); err != nil {
		return nil, err
	}
	for _, eid := range entityIDs {
		byAttr := byEntity[eid]
		row := make([]string, 0, len(cols)+1)
		row = append(row, eid)
		for _, c := range cols {
			row = append(row, byAttr[c.id])
		}
		out.Rows = append(out.Rows, row)
	}
	return out, nil
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
	page := db.Page{Limit: 500}
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
		last := summaries[len(summaries)-1]
		page.Cursor = db.EncodeKeyset(db.KeysetTime(last.LastUpdatedAt), last.EntityID.String())
	}
	if len(ids) > maxExportRows {
		ids = ids[:maxExportRows]
	}
	return ids, nil
}
