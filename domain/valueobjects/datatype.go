package valueobjects

import "fmt"

// DataType enumerates the soft types an attribute definition can declare.
type DataType string

// The supported soft data types.
const (
	DataTypeBool     DataType = "bool"
	DataTypeString   DataType = "string"
	DataTypeInteger  DataType = "integer"
	DataTypeFloat    DataType = "float"
	DataTypeDecimal  DataType = "decimal"
	DataTypeDate     DataType = "date"
	DataTypeTime     DataType = "time"
	DataTypeDateTime DataType = "datetime"
	DataTypeEnum     DataType = "enum"
	DataTypeURL      DataType = "url"
	DataTypeEmail    DataType = "email"
	DataTypeJSON     DataType = "json"
	// DataTypeMedia stores a reference to a file/image in object storage:
	// the value is metadata (object key, mime, size, checksum, filename),
	// never the bytes.
	DataTypeMedia DataType = "media"
)

var dataTypes = map[DataType]struct{}{
	DataTypeBool:     {},
	DataTypeString:   {},
	DataTypeInteger:  {},
	DataTypeFloat:    {},
	DataTypeDecimal:  {},
	DataTypeDate:     {},
	DataTypeTime:     {},
	DataTypeDateTime: {},
	DataTypeEnum:     {},
	DataTypeURL:      {},
	DataTypeEmail:    {},
	DataTypeJSON:     {},
	DataTypeMedia:    {},
}

// ParseDataType validates a data type name.
func ParseDataType(s string) (DataType, error) {
	dt := DataType(s)
	if _, ok := dataTypes[dt]; !ok {
		return "", fmt.Errorf("unknown data type %q", s)
	}
	return dt, nil
}

// String returns the data type name.
func (d DataType) String() string { return string(d) }

// IsTextual reports whether values of this type are backed by text and
// support length/pattern constraints.
func (d DataType) IsTextual() bool {
	switch d {
	case DataTypeString, DataTypeEnum, DataTypeURL, DataTypeEmail:
		return true
	default:
		return false
	}
}

// IsOrdered reports whether values of this type have a total order and
// support min/max value and range constraints.
func (d DataType) IsOrdered() bool {
	switch d {
	case DataTypeInteger, DataTypeFloat, DataTypeDecimal, DataTypeDate, DataTypeTime, DataTypeDateTime:
		return true
	default:
		return false
	}
}

// IsTemporal reports whether values of this type carry a timestamp and
// support dynamic (relative-time) defaults and conditions.
func (d DataType) IsTemporal() bool {
	switch d {
	case DataTypeDate, DataTypeTime, DataTypeDateTime:
		return true
	default:
		return false
	}
}
