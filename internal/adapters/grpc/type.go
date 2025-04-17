package grpc

import (
	"context"
	"fmt"
	"github.com/bufbuild/connect-go"
	"github.com/zac300/flexitype/pkg/sdk"
	"reflect"
	"strings"
	"time"

	// Import using the correct package paths that match the replace directives in go.mod
	"github.com/zac300/flexitype/api/flexitypev1"
	"github.com/zac300/flexitype/api/flexitypev1connect"
	"github.com/zac300/flexitype/internal/application/services"
	"github.com/zac300/flexitype/internal/domain/core"
	"github.com/zac300/flexitype/internal/domain/validation"
	"github.com/zac300/flexitype/internal/ports"
)

// flexiTypeServiceServer implements the FlexiTypeService defined in the proto file
type flexiTypeServiceServer struct {
	flexitypev1connect.UnimplementedFlexiTypeServiceHandler
	typeService     *services.TypeService
	instanceService *services.InstanceService
}

// Helper functions for converting between domain and proto types
func domainTypeToProto(typeDef *core.TypeDefinition) *flexitypev1.TypeDefinition {
	// Parent type ID
	var parentTypeID string
	if typeDef.ParentType != nil {
		parentTypeID = typeDef.ParentType.ID
	}

	protoType := &flexitypev1.TypeDefinition{
		Id:           typeDef.ID,
		Name:         typeDef.Name,
		Description:  typeDef.Description,
		Version:      int32(typeDef.Version),
		ParentTypeId: parentTypeID,
		Attributes:   make([]*flexitypev1.AttributeDefinition, 0, len(typeDef.Attributes)),
	}

	// Set timestamps
	if typeDef.ArchivedAt != nil {
		protoType.ArchivedAt = typeDef.ArchivedAt.Format(time.RFC3339)
	}
	protoType.CreatedAt = typeDef.CreatedAt.Format(time.RFC3339)
	protoType.UpdatedAt = typeDef.UpdatedAt.Format(time.RFC3339)

	// Convert attributes
	for _, attr := range typeDef.Attributes {
		protoAttr := domainAttributeToProto(attr)
		protoType.Attributes = append(protoType.Attributes, protoAttr)
	}

	return protoType
}

func domainAttributeToProto(attr *core.AttributeDefinition) *flexitypev1.AttributeDefinition {
	protoAttr := &flexitypev1.AttributeDefinition{
		Id:              attr.ID,
		Name:            attr.Name,
		Description:     attr.Description,
		DataType:        string(attr.DataType),
		Required:        attr.Required,
		MultiValued:     attr.MultiValued,
		Disabled:        attr.Disabled,
		ValidationRules: make([]*flexitypev1.ValidationRule, 0, len(attr.ValidationRules)),
		CreatedAt:       attr.CreatedAt.Format(time.RFC3339),
		UpdatedAt:       attr.UpdatedAt.Format(time.RFC3339),
		Cascades:        make([]*flexitypev1.CascadeDefinition, 0, len(attr.Cascades)),
	}

	// Convert all cascades
	for _, cascade := range attr.Cascades {
		protoCascade := &flexitypev1.CascadeDefinition{
			Enabled:  cascade.Enabled,
			Behavior: string(cascade.Behavior),
			Logic:    cascade.Logic,
			Weight:   int32(cascade.Weight),
		}
		protoAttr.Cascades = append(protoAttr.Cascades, protoCascade)
	}

	// Set default value if present
	if attr.DefaultValue != nil {
		protoAttr.DefaultValue = domainValueToProto(attr.DefaultValue)
	}

	// Convert validation rules
	for _, rule := range attr.ValidationRules {
		protoRule := &flexitypev1.ValidationRule{
			Type:       fmt.Sprintf("%T", rule),
			Parameters: make(map[string]*flexitypev1.AttributeValue),
		}

		// Extract parameters based on rule type
		switch r := rule.(type) {
		case *validation.MinLengthRule:
			protoRule.Parameters["minLength"] = &flexitypev1.AttributeValue{
				Value: &flexitypev1.AttributeValue_IntValue{
					IntValue: int64(r.MinLength),
				},
			}
		case *validation.MaxLengthRule:
			protoRule.Parameters["maxLength"] = &flexitypev1.AttributeValue{
				Value: &flexitypev1.AttributeValue_IntValue{
					IntValue: int64(r.MaxLength),
				},
			}
		case *validation.PatternRule:
			protoRule.Parameters["pattern"] = &flexitypev1.AttributeValue{
				Value: &flexitypev1.AttributeValue_StringValue{
					StringValue: r.Pattern,
				},
			}
		case *validation.EnumRule:
			// Convert allowed values to an array
			arrayValue := &flexitypev1.ArrayValue{
				Values: make([]*flexitypev1.AttributeValue, 0, len(r.AllowedValues)),
			}
			for _, allowedValue := range r.AllowedValues {
				arrayValue.Values = append(arrayValue.Values, domainValueToProto(allowedValue))
			}
			protoRule.Parameters["allowedValues"] = &flexitypev1.AttributeValue{
				Value: &flexitypev1.AttributeValue_ArrayValue{
					ArrayValue: arrayValue,
				},
			}
		case *validation.RangeRule:
			if r.Min != nil {
				protoRule.Parameters["min"] = &flexitypev1.AttributeValue{
					Value: &flexitypev1.AttributeValue_FloatValue{
						FloatValue: *r.Min,
					},
				}
			}
			if r.Max != nil {
				protoRule.Parameters["max"] = &flexitypev1.AttributeValue{
					Value: &flexitypev1.AttributeValue_FloatValue{
						FloatValue: *r.Max,
					},
				}
			}
		case *validation.CustomRule:
			protoRule.Parameters["description"] = &flexitypev1.AttributeValue{
				Value: &flexitypev1.AttributeValue_StringValue{
					StringValue: r.Description,
				},
			}
		}

		protoAttr.ValidationRules = append(protoAttr.ValidationRules, protoRule)
	}

	return protoAttr
}

func domainInstanceToProto(instance *core.Instance) *flexitypev1.Instance {
	protoInstance := &flexitypev1.Instance{
		Id:              instance.ID,
		Version:         int32(instance.Version),
		TypeId:          instance.TypeDefinition.ID,
		TypeVersion:     int32(instance.TypeVersion),
		AttributeValues: make(map[string]*flexitypev1.AttributeValue, len(instance.Attributes)),
	}

	// Set timestamps
	if instance.ArchivedAt != nil {
		protoInstance.ArchivedAt = instance.ArchivedAt.Format(time.RFC3339)
	}
	protoInstance.CreatedAt = instance.CreatedAt.Format(time.RFC3339)
	protoInstance.UpdatedAt = instance.UpdatedAt.Format(time.RFC3339)

	// Convert attribute values
	for name, value := range instance.Attributes {
		protoInstance.AttributeValues[name] = domainValueToProto(value)
	}

	return protoInstance
}

func domainValueToProto(value interface{}) *flexitypev1.AttributeValue {
	if value == nil {
		return nil
	}

	protoValue := &flexitypev1.AttributeValue{}

	switch v := value.(type) {
	case string:
		protoValue.Value = &flexitypev1.AttributeValue_StringValue{StringValue: v}
	case int, int8, int16, int32, int64:
		// Convert to int64
		intVal := reflect.ValueOf(v).Int()
		protoValue.Value = &flexitypev1.AttributeValue_IntValue{IntValue: intVal}
	case float32, float64:
		// Convert to float64
		floatVal := reflect.ValueOf(v).Float()
		protoValue.Value = &flexitypev1.AttributeValue_FloatValue{FloatValue: floatVal}
	case bool:
		protoValue.Value = &flexitypev1.AttributeValue_BoolValue{BoolValue: v}
	case []interface{}:
		// Handle array value
		arrayValue := &flexitypev1.ArrayValue{
			Values: make([]*flexitypev1.AttributeValue, 0, len(v)),
		}
		for _, item := range v {
			arrayValue.Values = append(arrayValue.Values, domainValueToProto(item))
		}
		protoValue.Value = &flexitypev1.AttributeValue_ArrayValue{ArrayValue: arrayValue}
	case map[string]interface{}:
		// Handle object value
		objectValue := &flexitypev1.ObjectValue{
			Fields: make(map[string]*flexitypev1.AttributeValue, len(v)),
		}
		for key, val := range v {
			objectValue.Fields[key] = domainValueToProto(val)
		}
		protoValue.Value = &flexitypev1.AttributeValue_ObjectValue{ObjectValue: objectValue}
	default:
		// Default to string representation
		protoValue.Value = &flexitypev1.AttributeValue_StringValue{StringValue: fmt.Sprintf("%v", v)}
	}

	return protoValue
}

func protoValueToDomain(value *flexitypev1.AttributeValue) interface{} {
	if value == nil {
		return nil
	}

	switch v := value.Value.(type) {
	case *flexitypev1.AttributeValue_StringValue:
		return v.StringValue
	case *flexitypev1.AttributeValue_IntValue:
		return v.IntValue
	case *flexitypev1.AttributeValue_FloatValue:
		return v.FloatValue
	case *flexitypev1.AttributeValue_BoolValue:
		return v.BoolValue
	case *flexitypev1.AttributeValue_DateValue:
		return v.DateValue
	case *flexitypev1.AttributeValue_ArrayValue:
		result := make([]interface{}, 0, len(v.ArrayValue.Values))
		for _, item := range v.ArrayValue.Values {
			result = append(result, protoValueToDomain(item))
		}
		return result
	case *flexitypev1.AttributeValue_ObjectValue:
		result := make(map[string]interface{}, len(v.ObjectValue.Fields))
		for key, val := range v.ObjectValue.Fields {
			result[key] = protoValueToDomain(val)
		}
		return result
	default:
		return nil
	}
}

// The following methods have been updated to use the new repository interfaces with QueryOptions

// CreateType creates a new type definition
func (s *flexiTypeServiceServer) CreateType(
	ctx context.Context,
	req *connect.Request[flexitypev1.CreateTypeRequest],
) (*connect.Response[flexitypev1.TypeResponse], error) {
	// Use the type service to create the type
	typeDef, err := s.typeService.CreateType(ctx, req.Msg.Id, req.Msg.Name, req.Msg.Description, req.Msg.ParentTypeId)
	if err != nil {
		return nil, connect.NewError(connect.CodeInternal, fmt.Errorf("failed to create type: %w", err))
	}

	// Return the created type
	return connect.NewResponse(&flexitypev1.TypeResponse{
		Type: domainTypeToProto(typeDef),
	}), nil
}

// GetType retrieves a type definition by ID
func (s *flexiTypeServiceServer) GetType(
	ctx context.Context,
	req *connect.Request[flexitypev1.GetTypeRequest],
) (*connect.Response[flexitypev1.TypeResponse], error) {
	typeDef, err := s.typeService.GetType(ctx, req.Msg.Id)
	if err != nil {
		return nil, connect.NewError(connect.CodeNotFound, fmt.Errorf("type not found: %w", err))
	}

	return connect.NewResponse(&flexitypev1.TypeResponse{
		Type: domainTypeToProto(typeDef),
	}), nil
}

// ListTypes lists all type definitions - UPDATED to use QueryOptions with IncludeArchived
func (s *flexiTypeServiceServer) ListTypes(
	ctx context.Context,
	req *connect.Request[flexitypev1.ListTypesRequest],
) (*connect.Response[flexitypev1.ListTypesResponse], error) {
	// Create query options for pagination
	options := &ports.QueryOptions{
		Limit:           int(req.Msg.PageSize),
		IncludeArchived: req.Msg.IncludeArchived,
	}

	// Default page size if not specified
	if options.Limit <= 0 {
		options.Limit = 50
	}

	// Get page token as offset
	if req.Msg.PageToken != "" {
		// PageToken might be a string, convert to int if possible
		var offset int
		if _, err := fmt.Sscanf(req.Msg.PageToken, "%d", &offset); err == nil {
			options.Offset = offset
		}
	}

	// Create a convenience method for listing types with options in TypeService
	// For now, we'll reuse the existing pattern and access the repo via the typeRepo field
	types, totalCount, err := s.typeService.QueryTypes(ctx, options)
	if err != nil {
		return nil, connect.NewError(connect.CodeInternal, fmt.Errorf("failed to list types: %w", err))
	}

	// Prepare next page token
	var nextPageToken string
	if options.Offset+len(types) < totalCount {
		nextPageToken = fmt.Sprintf("%d", options.Offset+len(types))
	}

	// Build response
	response := &flexitypev1.ListTypesResponse{
		Types:         make([]*flexitypev1.TypeDefinition, 0, len(types)),
		NextPageToken: nextPageToken,
	}

	for _, typeDef := range types {
		response.Types = append(response.Types, domainTypeToProto(typeDef))
	}

	return connect.NewResponse(response), nil
}

// UpdateType updates an existing type definition
func (s *flexiTypeServiceServer) UpdateType(
	ctx context.Context,
	req *connect.Request[flexitypev1.UpdateTypeRequest],
) (*connect.Response[flexitypev1.TypeResponse], error) {
	// Use the type service to update the type
	typeDef, err := s.typeService.UpdateType(ctx, req.Msg.Id, req.Msg.Name, req.Msg.Description, req.Msg.ParentTypeId)
	if err != nil {
		return nil, connect.NewError(connect.CodeInternal, fmt.Errorf("failed to update type: %w", err))
	}

	return connect.NewResponse(&flexitypev1.TypeResponse{
		Type: domainTypeToProto(typeDef),
	}), nil
}

// AddAttribute adds or updates an attribute on a type definition
func (s *flexiTypeServiceServer) AddAttribute(ctx context.Context, req *connect.Request[flexitypev1.AddAttributeRequest]) (*connect.Response[flexitypev1.TypeResponse], error) {
	// Convert proto attribute to domain attribute
	protoAttr := req.Msg.Attribute

	// Create domain attribute
	attribute := &core.AttributeDefinition{
		ID:              protoAttr.Id,
		Name:            protoAttr.Name,
		Description:     protoAttr.Description,
		DataType:        core.DataType(protoAttr.DataType),
		Required:        protoAttr.Required,
		MultiValued:     protoAttr.MultiValued,
		Disabled:        protoAttr.Disabled,
		ValidationRules: make([]validation.Rule, 0),
		Cascades:        make([]core.Cascade, 0),
	}

	// Process multiple cascades if provided
	if len(protoAttr.Cascades) > 0 {
		for _, protoCascade := range protoAttr.Cascades {
			cascade := core.Cascade{
				Enabled:  protoCascade.Enabled,
				Behavior: core.CascadeBehavior(protoCascade.Behavior),
				Logic:    protoCascade.Logic,
				Weight:   int(protoCascade.Weight),
			}
			attribute.Cascades = append(attribute.Cascades, cascade)
		}
	}

	// Set default value if provided
	if protoAttr.DefaultValue != nil {
		attribute.DefaultValue = protoValueToDomain(protoAttr.DefaultValue)
	}

	// Convert validation rules
	for _, protoRule := range protoAttr.ValidationRules {
		var rule validation.Rule

		switch protoRule.Type {
		case "*validation.RequiredRule":
			rule = &validation.RequiredRule{}

		case "*validation.MinLengthRule":
			minLengthParam, ok := protoRule.Parameters["minLength"]
			if !ok || minLengthParam.GetIntValue() <= 0 {
				return nil, connect.NewError(connect.CodeInvalidArgument, fmt.Errorf("invalid minLength parameter"))
			}
			rule = &validation.MinLengthRule{
				MinLength: int(minLengthParam.GetIntValue()),
			}

		case "*validation.MaxLengthRule":
			maxLengthParam, ok := protoRule.Parameters["maxLength"]
			if !ok || maxLengthParam.GetIntValue() <= 0 {
				return nil, connect.NewError(connect.CodeInvalidArgument, fmt.Errorf("invalid maxLength parameter"))
			}
			rule = &validation.MaxLengthRule{
				MaxLength: int(maxLengthParam.GetIntValue()),
			}

		case "*validation.PatternRule":
			patternParam, ok := protoRule.Parameters["pattern"]
			if !ok || patternParam.GetStringValue() == "" {
				return nil, connect.NewError(connect.CodeInvalidArgument, fmt.Errorf("invalid pattern parameter"))
			}
			var err error
			rule, err = validation.NewPatternRule(patternParam.GetStringValue())
			if err != nil {
				return nil, connect.NewError(connect.CodeInvalidArgument, fmt.Errorf("invalid pattern: %w", err))
			}

		case "*validation.EnumRule":
			allowedValuesParam, ok := protoRule.Parameters["allowedValues"]
			if !ok || allowedValuesParam.GetArrayValue() == nil {
				return nil, connect.NewError(connect.CodeInvalidArgument, fmt.Errorf("invalid allowedValues parameter"))
			}

			arrayValue := allowedValuesParam.GetArrayValue()
			allowedValues := make([]interface{}, 0, len(arrayValue.Values))
			for _, v := range arrayValue.Values {
				allowedValues = append(allowedValues, protoValueToDomain(v))
			}

			rule = &validation.EnumRule{
				AllowedValues: allowedValues,
			}

		case "*validation.RangeRule":
			minParam, hasMin := protoRule.Parameters["min"]
			maxParam, hasMax := protoRule.Parameters["max"]

			rangeRule := &validation.RangeRule{}

			if hasMin {
				minValue := minParam.GetFloatValue()
				rangeRule.Min = &minValue
			}

			if hasMax {
				maxValue := maxParam.GetFloatValue()
				rangeRule.Max = &maxValue
			}

			if !hasMin && !hasMax {
				return nil, connect.NewError(connect.CodeInvalidArgument, fmt.Errorf("range rule must have at least min or max"))
			}

			rule = rangeRule

		default:
			// Use a generic rule for unsupported types
			rule = &validation.GenericRule{}
		}

		attribute.ValidationRules = append(attribute.ValidationRules, rule)
	}

	// Use the type service to add the attribute
	typeDef, err := s.typeService.AddAttribute(ctx, req.Msg.TypeId, attribute)
	if err != nil {
		return nil, connect.NewError(connect.CodeInternal, fmt.Errorf("failed to add attribute: %w", err))
	}

	return connect.NewResponse(&flexitypev1.TypeResponse{
		Type: domainTypeToProto(typeDef),
	}), nil
}

func (s *flexiTypeServiceServer) UpdateAttribute(ctx context.Context, req *connect.Request[flexitypev1.UpdateAttributeRequest]) (*connect.Response[flexitypev1.TypeResponse], error) {
	// First get the type definition to find the existing attribute
	typeDef, err := s.typeService.GetType(ctx, req.Msg.TypeId)
	if err != nil {
		return nil, connect.NewError(connect.CodeNotFound, fmt.Errorf("type not found: %w", err))
	}

	// Convert proto attribute to domain attribute
	protoAttr := req.Msg.Attribute

	// Find existing attribute from the type definition
	var existingAttr *core.AttributeDefinition
	for _, attr := range typeDef.Attributes {
		if attr.ID == protoAttr.Id {
			existingAttr = attr
			break
		}
	}

	if existingAttr == nil {
		return nil, connect.NewError(connect.CodeNotFound, fmt.Errorf("attribute with ID '%s' not found", protoAttr.Id))
	}

	// Create a new attribute definition with the updated values
	updatedAttr := &core.AttributeDefinition{
		ID:              protoAttr.Id,
		Name:            protoAttr.Name,
		Description:     protoAttr.Description,
		DataType:        core.DataType(protoAttr.DataType),
		Required:        protoAttr.Required,
		MultiValued:     protoAttr.MultiValued,
		Disabled:        protoAttr.Disabled,
		ValidationRules: make([]validation.Rule, 0),
		Cascades:        make([]core.Cascade, 0),
		CreatedAt:       existingAttr.CreatedAt, // Preserve creation time
	}

	// Process multiple cascades if provided
	if len(protoAttr.Cascades) > 0 {
		for _, protoCascade := range protoAttr.Cascades {
			cascade := core.Cascade{
				Enabled:  protoCascade.Enabled,
				Behavior: core.CascadeBehavior(protoCascade.Behavior),
				Logic:    protoCascade.Logic,
				Weight:   int(protoCascade.Weight),
			}
			updatedAttr.Cascades = append(updatedAttr.Cascades, cascade)
		}
	}

	// Set default value if provided
	if protoAttr.DefaultValue != nil {
		updatedAttr.DefaultValue = protoValueToDomain(protoAttr.DefaultValue)
	}

	// Convert validation rules
	for _, protoRule := range protoAttr.ValidationRules {
		var rule validation.Rule

		switch protoRule.Type {
		case "*validation.RequiredRule":
			rule = &validation.RequiredRule{}

		case "*validation.MinLengthRule":
			minLengthParam, ok := protoRule.Parameters["minLength"]
			if !ok || minLengthParam.GetIntValue() <= 0 {
				return nil, connect.NewError(connect.CodeInvalidArgument, fmt.Errorf("invalid minLength parameter"))
			}
			rule = &validation.MinLengthRule{
				MinLength: int(minLengthParam.GetIntValue()),
			}

		case "*validation.MaxLengthRule":
			maxLengthParam, ok := protoRule.Parameters["maxLength"]
			if !ok || maxLengthParam.GetIntValue() <= 0 {
				return nil, connect.NewError(connect.CodeInvalidArgument, fmt.Errorf("invalid maxLength parameter"))
			}
			rule = &validation.MaxLengthRule{
				MaxLength: int(maxLengthParam.GetIntValue()),
			}

		case "*validation.PatternRule":
			patternParam, ok := protoRule.Parameters["pattern"]
			if !ok || patternParam.GetStringValue() == "" {
				return nil, connect.NewError(connect.CodeInvalidArgument, fmt.Errorf("invalid pattern parameter"))
			}
			var err error
			rule, err = validation.NewPatternRule(patternParam.GetStringValue())
			if err != nil {
				return nil, connect.NewError(connect.CodeInvalidArgument, fmt.Errorf("invalid pattern: %w", err))
			}

		case "*validation.EnumRule":
			allowedValuesParam, ok := protoRule.Parameters["allowedValues"]
			if !ok || allowedValuesParam.GetArrayValue() == nil {
				return nil, connect.NewError(connect.CodeInvalidArgument, fmt.Errorf("invalid allowedValues parameter"))
			}

			arrayValue := allowedValuesParam.GetArrayValue()
			allowedValues := make([]interface{}, 0, len(arrayValue.Values))
			for _, v := range arrayValue.Values {
				allowedValues = append(allowedValues, protoValueToDomain(v))
			}

			rule = &validation.EnumRule{
				AllowedValues: allowedValues,
			}

		case "*validation.RangeRule":
			minParam, hasMin := protoRule.Parameters["min"]
			maxParam, hasMax := protoRule.Parameters["max"]

			rangeRule := &validation.RangeRule{}

			if hasMin {
				minValue := minParam.GetFloatValue()
				rangeRule.Min = &minValue
			}

			if hasMax {
				maxValue := maxParam.GetFloatValue()
				rangeRule.Max = &maxValue
			}

			if !hasMin && !hasMax {
				return nil, connect.NewError(connect.CodeInvalidArgument, fmt.Errorf("range rule must have at least min or max"))
			}

			rule = rangeRule

		default:
			// Use a generic rule for unsupported types
			rule = &validation.GenericRule{}
		}

		updatedAttr.ValidationRules = append(updatedAttr.ValidationRules, rule)
	}

	// Use the type service to add the attribute (which will replace the existing one)
	updatedTypeDef, err := s.typeService.AddAttribute(ctx, req.Msg.TypeId, updatedAttr)
	if err != nil {
		return nil, connect.NewError(connect.CodeInternal, fmt.Errorf("failed to update attribute: %w", err))
	}

	return connect.NewResponse(&flexitypev1.TypeResponse{
		Type: domainTypeToProto(updatedTypeDef),
	}), nil
}

func (s *flexiTypeServiceServer) DeleteAttribute(ctx context.Context, req *connect.Request[flexitypev1.DeleteAttributeRequest]) (*connect.Response[flexitypev1.TypeResponse], error) {
	// Use the type service to delete the attribute
	typeDef, err := s.typeService.DeleteAttribute(ctx, req.Msg.TypeId, req.Msg.AttributeName)
	if err != nil {
		if strings.Contains(err.Error(), "not found") {
			return nil, connect.NewError(connect.CodeNotFound, err)
		}
		return nil, connect.NewError(connect.CodeInternal, fmt.Errorf("failed to delete attribute: %w", err))
	}

	return connect.NewResponse(&flexitypev1.TypeResponse{
		Type: domainTypeToProto(typeDef),
	}), nil
}

func (s *flexiTypeServiceServer) SetAttributeDisabledState(ctx context.Context, req *connect.Request[flexitypev1.SetAttributeDisabledStateRequest]) (*connect.Response[flexitypev1.TypeResponse], error) {
	// Use the type service to set the attribute disabled state
	typeDef, err := s.typeService.SetAttributeDisabledState(ctx, req.Msg.TypeId, req.Msg.AttributeName, req.Msg.Disabled)
	if err != nil {
		if strings.Contains(err.Error(), "not found") {
			return nil, connect.NewError(connect.CodeNotFound, err)
		}
		return nil, connect.NewError(connect.CodeInternal, fmt.Errorf("failed to set attribute disabled state: %w", err))
	}

	return connect.NewResponse(&flexitypev1.TypeResponse{
		Type: domainTypeToProto(typeDef),
	}), nil
}

// ArchiveType archives a type definition
func (s *flexiTypeServiceServer) ArchiveType(
	ctx context.Context,
	req *connect.Request[flexitypev1.ArchiveTypeRequest],
) (*connect.Response[flexitypev1.TypeResponse], error) {
	// Archive the type using the type service
	err := s.typeService.ArchiveType(ctx, req.Msg.Id)
	if err != nil {
		if strings.Contains(err.Error(), "not found") {
			return nil, connect.NewError(connect.CodeNotFound, err)
		}
		return nil, connect.NewError(connect.CodeInternal, err)
	}

	// Get the updated type definition with the archived_at timestamp
	typeDef, err := s.typeService.GetType(ctx, req.Msg.Id)
	if err != nil {
		return nil, connect.NewError(connect.CodeInternal, fmt.Errorf("failed to retrieve updated type: %w", err))
	}

	return connect.NewResponse(&flexitypev1.TypeResponse{
		Type: domainTypeToProto(typeDef),
	}), nil
}

// UnarchiveType removes the archived status from a type definition
func (s *flexiTypeServiceServer) UnarchiveType(
	ctx context.Context,
	req *connect.Request[flexitypev1.UnarchiveTypeRequest],
) (*connect.Response[flexitypev1.TypeResponse], error) {
	// Unarchive the type using the type service
	err := s.typeService.UnarchiveType(ctx, req.Msg.Id)
	if err != nil {
		if strings.Contains(err.Error(), "not found") {
			return nil, connect.NewError(connect.CodeNotFound, err)
		}
		if strings.Contains(err.Error(), "not archived") {
			return nil, connect.NewError(connect.CodeFailedPrecondition, err)
		}
		return nil, connect.NewError(connect.CodeInternal, err)
	}

	// Get the updated type definition with the archived_at timestamp cleared
	typeDef, err := s.typeService.GetType(ctx, req.Msg.Id)
	if err != nil {
		return nil, connect.NewError(connect.CodeInternal, fmt.Errorf("failed to retrieve updated type: %w", err))
	}

	return connect.NewResponse(&flexitypev1.TypeResponse{
		Type: domainTypeToProto(typeDef),
	}), nil
}

func (s *flexiTypeServiceServer) ExportTypeSchema(ctx context.Context, req *connect.Request[flexitypev1.ExportTypeSchemaRequest]) (*connect.Response[flexitypev1.SchemaResponse], error) {
	// Get the type definition using the type service
	typeDef, err := s.typeService.GetType(ctx, req.Msg.TypeId)
	if err != nil {
		return nil, connect.NewError(connect.CodeNotFound, fmt.Errorf("type not found: %w", err))
	}

	// Create SDK helper for YAML conversion
	yamlHelper := sdk.NewYAMLHelper()

	// Export to YAML
	yamlContent, err := yamlHelper.ExportTypeToYAML(typeDef)
	if err != nil {
		return nil, connect.NewError(connect.CodeInternal, fmt.Errorf("failed to export type to YAML: %w", err))
	}

	return connect.NewResponse(&flexitypev1.SchemaResponse{
		YamlContent: yamlContent,
	}), nil
}

// ArchiveInstance archives an instance
func (s *flexiTypeServiceServer) ArchiveInstance(
	ctx context.Context,
	req *connect.Request[flexitypev1.ArchiveInstanceRequest],
) (*connect.Response[flexitypev1.InstanceResponse], error) {
	// Archive the instance using the instance service
	err := s.instanceService.ArchiveInstance(ctx, req.Msg.Id)
	if err != nil {
		if strings.Contains(err.Error(), "not found") {
			return nil, connect.NewError(connect.CodeNotFound, err)
		}
		return nil, connect.NewError(connect.CodeInternal, fmt.Errorf("failed to archive instance: %w", err))
	}

	// Get the updated instance with the archived_at timestamp
	instance, err := s.instanceService.GetInstance(ctx, req.Msg.Id)
	if err != nil {
		return nil, connect.NewError(connect.CodeInternal, fmt.Errorf("failed to retrieve updated instance: %w", err))
	}

	return connect.NewResponse(&flexitypev1.InstanceResponse{
		Instance: domainInstanceToProto(instance),
	}), nil
}

// UnarchiveInstance removes the archived status from an instance
func (s *flexiTypeServiceServer) UnarchiveInstance(
	ctx context.Context,
	req *connect.Request[flexitypev1.UnarchiveInstanceRequest],
) (*connect.Response[flexitypev1.InstanceResponse], error) {
	// Unarchive the instance using the instance service
	err := s.instanceService.UnarchiveInstance(ctx, req.Msg.Id)
	if err != nil {
		if strings.Contains(err.Error(), "not found") {
			return nil, connect.NewError(connect.CodeNotFound, err)
		}
		if strings.Contains(err.Error(), "not archived") {
			return nil, connect.NewError(connect.CodeFailedPrecondition, err)
		}
		return nil, connect.NewError(connect.CodeInternal, fmt.Errorf("failed to unarchive instance: %w", err))
	}

	// Get the updated instance with the archived_at timestamp cleared
	instance, err := s.instanceService.GetInstance(ctx, req.Msg.Id)
	if err != nil {
		return nil, connect.NewError(connect.CodeInternal, fmt.Errorf("failed to retrieve updated instance: %w", err))
	}

	return connect.NewResponse(&flexitypev1.InstanceResponse{
		Instance: domainInstanceToProto(instance),
	}), nil
}

func (s *flexiTypeServiceServer) ImportTypeSchema(ctx context.Context, req *connect.Request[flexitypev1.ImportTypeSchemaRequest]) (*connect.Response[flexitypev1.TypeResponse], error) {
	// Create SDK helper for YAML conversion
	yamlHelper := sdk.NewYAMLHelper()

	// Import from YAML
	typeDef, err := yamlHelper.ImportTypeFromYAML(req.Msg.YamlContent)
	if err != nil {
		return nil, connect.NewError(connect.CodeInvalidArgument, fmt.Errorf("failed to import type from YAML: %w", err))
	}

	// Check if type with same ID already exists using the type service
	existing, err := s.typeService.GetType(ctx, typeDef.ID)
	if err == nil && existing != nil {
		return nil, connect.NewError(connect.CodeAlreadyExists, fmt.Errorf("type with ID '%s' already exists", typeDef.ID))
	}

	// Create the type using the type service
	typeDef, err = s.typeService.CreateType(ctx, typeDef.ID, typeDef.Name, typeDef.Description, "")
	if err != nil {
		return nil, connect.NewError(connect.CodeInternal, fmt.Errorf("failed to create type: %w", err))
	}

	// For each attribute in the imported type, add it to the newly created type
	for _, attr := range typeDef.Attributes {
		_, err = s.typeService.AddAttribute(ctx, typeDef.ID, attr)
		if err != nil {
			return nil, connect.NewError(connect.CodeInternal, fmt.Errorf("failed to add attribute '%s': %w", attr.Name, err))
		}
	}

	// Get the final updated type
	typeDef, err = s.typeService.GetType(ctx, typeDef.ID)
	if err != nil {
		return nil, connect.NewError(connect.CodeInternal, fmt.Errorf("failed to retrieve final type: %w", err))
	}

	return connect.NewResponse(&flexitypev1.TypeResponse{
		Type: domainTypeToProto(typeDef),
	}), nil
}
