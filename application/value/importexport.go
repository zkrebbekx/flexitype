package value

import (
	"context"
	"encoding/json"
	"errors"
	"strconv"
	"time"

	apptypedef "github.com/zkrebbekx/flexitype/application/typedef"
	"github.com/zkrebbekx/flexitype/application/uow"
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
}

// Import loads tabular rows into a type's entities. It resolves the column
// mapping against the type's effective schema, validates every row, then
// either reports (dry run), writes the valid rows (best effort) or writes
// all rows atomically (transactional, refusing the whole set if any row is
// invalid).
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

	// Phase 1: validate every row against committed data (rollback UoW).
	// Cell conversion errors are caught here without touching the database.
	type prepared struct {
		row    int
		inputs []SetInput
	}
	valid := make([]prepared, 0, len(in.Rows))
	for r, row := range in.Rows {
		rowNum := r + 1 // 1-based; header is row 0 for humans
		inputs, cellErrs := i.rowInputs(in.TypeDefinitionID, cols, keyIdx, rowNum, row)
		if len(cellErrs) > 0 {
			report.Errors = append(report.Errors, cellErrs...)
			continue
		}
		if writeErr := i.applyRow(ctx, inputs, false); writeErr != nil {
			report.Errors = append(report.Errors, importErrorFrom(rowNum, writeErr))
			continue
		}
		report.RowsValid++
		valid = append(valid, prepared{row: rowNum, inputs: inputs})
	}

	if in.DryRun {
		return report, nil
	}

	switch mode {
	case ImportTransactional:
		if len(report.Errors) > 0 {
			return report, nil // atomic: refuse the whole set
		}
		all := make([]SetInput, 0, report.RowsValid)
		for _, p := range valid {
			all = append(all, p.inputs...)
		}
		if err := i.applyRow(ctx, all, true); err != nil {
			// Should not happen after phase 1; surface it defensively.
			report.Errors = append(report.Errors, importErrorFrom(0, err))
			return report, nil
		}
		report.RowsWritten = report.RowsValid
	case ImportBestEffort:
		for _, p := range valid {
			if err := i.applyRow(ctx, p.inputs, true); err != nil {
				report.Errors = append(report.Errors, importErrorFrom(p.row, err))
				continue
			}
			report.RowsWritten++
		}
	}
	return report, nil
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
			byName[s.InternalName] = mappedColumn{attrID: s.ID.String(), attrName: s.InternalName, dataType: s.DataType}
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

	for _, eid := range entityIDs {
		entityID, err := valueobjects.ParseEntityID(eid)
		if err != nil {
			return nil, domainerrors.NewValidation(err.Error())
		}
		vals, err := i.values.ListByEntity(ctx, domainvalue.EntityKey{
			TenantID: tenant, TypeDefinitionID: typeID, EntityID: entityID,
		})
		if err != nil {
			return nil, err
		}
		byAttr := map[valueobjects.AttributeDefinitionID]string{}
		for _, av := range vals {
			byAttr[av.AttributeDefinitionID()] = av.Value().String()
		}
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
		summaries, _, err := i.values.ListEntities(ctx, tenant, []valueobjects.TypeDefinitionID{typeID}, page)
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
		page.Cursor = db.EncodeKeyset(last.LastUpdatedAt.UTC().Format(time.RFC3339Nano), last.EntityID.String())
	}
	if len(ids) > maxExportRows {
		ids = ids[:maxExportRows]
	}
	return ids, nil
}
