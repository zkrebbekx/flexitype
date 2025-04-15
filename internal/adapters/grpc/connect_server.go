package grpc

import (
	"context"
	"fmt"
	"net/http"
	"reflect"
	"strings"
	"time"

	"github.com/bufbuild/connect-go"
	"golang.org/x/net/http2"
	"golang.org/x/net/http2/h2c"

	// Import using the correct package paths that match the replace directives in go.mod
	"github.com/zac300/flexitype/api/flexitypev1"
	"github.com/zac300/flexitype/api/flexitypev1connect"
	"github.com/zac300/flexitype/internal/domain/core"
	"github.com/zac300/flexitype/internal/domain/validation"
	"github.com/zac300/flexitype/internal/ports"
	"github.com/zac300/flexitype/pkg/sdk"
)

// ConnectServer implements the FlexiType gRPC service using Connect
type ConnectServer struct {
	typeRepo     ports.TypeRepository
	instanceRepo ports.InstanceRepository
	httpServer   *http.Server
}

// NewConnectServer creates a new FlexiType Connect gRPC server
func NewConnectServer(typeRepo ports.TypeRepository, instanceRepo ports.InstanceRepository) *ConnectServer {
	return &ConnectServer{
		typeRepo:     typeRepo,
		instanceRepo: instanceRepo,
	}
}

// Start starts the Connect gRPC server on the specified port
func (s *ConnectServer) Start(port int) error {
	// Create a new FlexiType service
	service := &flexiTypeServiceServer{
		typeRepo:     s.typeRepo,
		instanceRepo: s.instanceRepo,
	}

	// Create API path handlers for the service
	mux := http.NewServeMux()
	path, handler := flexitypev1connect.NewFlexiTypeServiceHandler(service)
	mux.Handle(path, handler)

	// Configure the HTTP server
	addr := fmt.Sprintf(":%d", port)
	s.httpServer = &http.Server{
		Addr:    addr,
		Handler: h2c.NewHandler(mux, &http2.Server{}),
	}

	fmt.Printf("FlexiType Connect gRPC server listening on %s\n", addr)
	return s.httpServer.ListenAndServe()
}

// Stop stops the gRPC server
func (s *ConnectServer) Stop() error {
	if s.httpServer != nil {
		return s.httpServer.Shutdown(context.Background())
	}
	return nil
}

// flexiTypeServiceServer implements the FlexiTypeService defined in the proto file
type flexiTypeServiceServer struct {
	flexitypev1connect.UnimplementedFlexiTypeServiceHandler
	typeRepo     ports.TypeRepository
	instanceRepo ports.InstanceRepository
}

// Helper functions for converting between domain and proto types (unchanged)
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
	typeDef := core.NewTypeDefinition(req.Msg.Id, req.Msg.Name, req.Msg.Description)

	// Set parent type if specified
	if req.Msg.ParentTypeId != "" {
		parentType, err := s.typeRepo.GetByID(ctx, req.Msg.ParentTypeId)
		if err != nil {
			return nil, connect.NewError(connect.CodeNotFound, fmt.Errorf("parent type not found: %w", err))
		}
		typeDef.SetParentType(parentType)
	}

	// Save the type
	err := s.typeRepo.Save(ctx, typeDef)
	if err != nil {
		return nil, connect.NewError(connect.CodeInternal, fmt.Errorf("failed to save type: %w", err))
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
	typeDef, err := s.typeRepo.GetByID(ctx, req.Msg.Id)
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

	// Query types with options
	types, totalCount, err := s.typeRepo.List(ctx, options)
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
	// Get the existing type
	typeDef, err := s.typeRepo.GetByID(ctx, req.Msg.Id)
	if err != nil {
		return nil, connect.NewError(connect.CodeNotFound, fmt.Errorf("type not found: %w", err))
	}

	// Check if anything is changing that would require a version increment
	versionChange := false

	// Update name and description
	if typeDef.Name != req.Msg.Name || typeDef.Description != req.Msg.Description {
		versionChange = true
		typeDef.Name = req.Msg.Name
		typeDef.Description = req.Msg.Description
	}

	// Update parent type if specified
	if req.Msg.ParentTypeId != "" && (typeDef.ParentType == nil || typeDef.ParentType.ID != req.Msg.ParentTypeId) {
		parentType, err := s.typeRepo.GetByID(ctx, req.Msg.ParentTypeId)
		if err != nil {
			return nil, connect.NewError(connect.CodeNotFound, fmt.Errorf("parent type not found: %w", err))
		}

		// Check for circular inheritance
		current := parentType
		for current != nil {
			if current.ID == typeDef.ID {
				return nil, connect.NewError(connect.CodeInvalidArgument, fmt.Errorf("circular inheritance detected"))
			}
			current = current.ParentType
		}

		typeDef.SetParentType(parentType)
		versionChange = true
	} else if req.Msg.ParentTypeId == "" && typeDef.ParentType != nil {
		// Remove parent type
		typeDef.SetParentType(nil)
		versionChange = true
	}

	// If we changed anything that affects the schema, increment the version
	if versionChange {
		typeDef.IncrementVersion()
	}

	// Save the updated type
	err = s.typeRepo.Save(ctx, typeDef)
	if err != nil {
		return nil, connect.NewError(connect.CodeInternal, fmt.Errorf("failed to save type: %w", err))
	}

	return connect.NewResponse(&flexitypev1.TypeResponse{
		Type: domainTypeToProto(typeDef),
	}), nil
}

// AddAttribute adds or updates an attribute on a type definition
func (s *flexiTypeServiceServer) AddAttribute(ctx context.Context, req *connect.Request[flexitypev1.AddAttributeRequest]) (*connect.Response[flexitypev1.TypeResponse], error) {
	// Get the type definition
	typeDef, err := s.typeRepo.GetByID(ctx, req.Msg.TypeId)
	if err != nil {
		return nil, connect.NewError(connect.CodeNotFound, fmt.Errorf("type not found: %w", err))
	}

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

	// Add attribute to type
	typeDef.AddAttribute(attribute)

	// Increment version
	typeDef.IncrementVersion()

	// Save updated type
	err = s.typeRepo.Save(ctx, typeDef)
	if err != nil {
		return nil, connect.NewError(connect.CodeInternal, fmt.Errorf("failed to save type: %w", err))
	}

	return connect.NewResponse(&flexitypev1.TypeResponse{
		Type: domainTypeToProto(typeDef),
	}), nil
}

func (s *flexiTypeServiceServer) UpdateAttribute(ctx context.Context, req *connect.Request[flexitypev1.UpdateAttributeRequest]) (*connect.Response[flexitypev1.TypeResponse], error) {
	// Get the type definition
	typeDef, err := s.typeRepo.GetByID(ctx, req.Msg.TypeId)
	if err != nil {
		return nil, connect.NewError(connect.CodeNotFound, fmt.Errorf("type not found: %w", err))
	}

	// Convert proto attribute to domain attribute
	protoAttr := req.Msg.Attribute

	// Find existing attribute
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

	// Update attribute fields
	existingAttr.Name = protoAttr.Name
	existingAttr.Description = protoAttr.Description
	existingAttr.DataType = core.DataType(protoAttr.DataType)
	existingAttr.Required = protoAttr.Required
	existingAttr.MultiValued = protoAttr.MultiValued
	existingAttr.Disabled = protoAttr.Disabled

	// Clear existing cascades
	existingAttr.Cascades = make([]core.Cascade, 0)

	// Process multiple cascades if provided
	if len(protoAttr.Cascades) > 0 {
		for _, protoCascade := range protoAttr.Cascades {
			cascade := core.Cascade{
				Enabled:  protoCascade.Enabled,
				Behavior: core.CascadeBehavior(protoCascade.Behavior),
				Logic:    protoCascade.Logic,
				Weight:   int(protoCascade.Weight),
			}
			existingAttr.Cascades = append(existingAttr.Cascades, cascade)
		}
	}

	// Set default value if provided
	if protoAttr.DefaultValue != nil {
		existingAttr.DefaultValue = protoValueToDomain(protoAttr.DefaultValue)
	}

	// Update validation rules
	existingAttr.ValidationRules = make([]validation.Rule, 0)

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

		existingAttr.ValidationRules = append(existingAttr.ValidationRules, rule)
	}

	// Increment version
	typeDef.IncrementVersion()

	// Save updated type
	err = s.typeRepo.Save(ctx, typeDef)
	if err != nil {
		return nil, connect.NewError(connect.CodeInternal, fmt.Errorf("failed to save type: %w", err))
	}

	return connect.NewResponse(&flexitypev1.TypeResponse{
		Type: domainTypeToProto(typeDef),
	}), nil
}

func (s *flexiTypeServiceServer) DeleteAttribute(ctx context.Context, req *connect.Request[flexitypev1.DeleteAttributeRequest]) (*connect.Response[flexitypev1.TypeResponse], error) {
	// Get the type definition
	typeDef, err := s.typeRepo.GetByID(ctx, req.Msg.TypeId)
	if err != nil {
		return nil, connect.NewError(connect.CodeNotFound, fmt.Errorf("type not found: %w", err))
	}

	// Find and remove the attribute
	newAttributes := make([]*core.AttributeDefinition, 0, len(typeDef.Attributes))
	found := false

	for _, attr := range typeDef.Attributes {
		if attr.ID != req.Msg.AttributeId {
			newAttributes = append(newAttributes, attr)
		} else {
			found = true
		}
	}

	if !found {
		return nil, connect.NewError(connect.CodeNotFound, fmt.Errorf("attribute with ID '%s' not found", req.Msg.AttributeId))
	}

	typeDef.Attributes = newAttributes

	// Increment version
	typeDef.IncrementVersion()

	// Save updated type
	err = s.typeRepo.Save(ctx, typeDef)
	if err != nil {
		return nil, connect.NewError(connect.CodeInternal, fmt.Errorf("failed to save type: %w", err))
	}

	return connect.NewResponse(&flexitypev1.TypeResponse{
		Type: domainTypeToProto(typeDef),
	}), nil
}

func (s *flexiTypeServiceServer) SetAttributeDisabledState(ctx context.Context, req *connect.Request[flexitypev1.SetAttributeDisabledStateRequest]) (*connect.Response[flexitypev1.TypeResponse], error) {
	// Find the type
	typeDef, err := s.typeRepo.GetByID(ctx, req.Msg.TypeId)
	if err != nil {
		return nil, connect.NewError(connect.CodeNotFound, fmt.Errorf("type not found: %w", err))
	}

	// Find the attribute
	var foundAttr *core.AttributeDefinition
	for _, attr := range typeDef.Attributes {
		if attr.ID == req.Msg.AttributeId {
			foundAttr = attr
			break
		}
	}

	if foundAttr == nil {
		return nil, connect.NewError(connect.CodeNotFound, fmt.Errorf("attribute not found"))
	}

	// Update the disabled state
	foundAttr.SetDisabled(req.Msg.Disabled)

	// Increment the version since the type definition is changing
	typeDef.IncrementVersion()

	// Save the updated type
	err = s.typeRepo.Save(ctx, typeDef)
	if err != nil {
		return nil, connect.NewError(connect.CodeInternal, fmt.Errorf("failed to save type: %w", err))
	}

	return connect.NewResponse(&flexitypev1.TypeResponse{
		Type: domainTypeToProto(typeDef),
	}), nil
}

func (s *flexiTypeServiceServer) CreateInstance(ctx context.Context, req *connect.Request[flexitypev1.CreateInstanceRequest]) (*connect.Response[flexitypev1.InstanceResponse], error) {
	// Get the type definition
	typeDef, err := s.typeRepo.GetByID(ctx, req.Msg.TypeId)
	if err != nil {
		return nil, connect.NewError(connect.CodeNotFound, fmt.Errorf("type not found: %w", err))
	}

	// Create instance
	instance := core.NewInstance(req.Msg.Id, typeDef)

	// Convert attribute values from proto to domain
	attrValues := make(map[string]interface{})
	for name, value := range req.Msg.AttributeValues {
		attrValues[name] = protoValueToDomain(value)
	}

	// Set attribute values
	for name, value := range attrValues {
		err := instance.SetAttribute(name, value)
		if err != nil {
			return nil, connect.NewError(connect.CodeInvalidArgument, fmt.Errorf("failed to set attribute '%s': %w", name, err))
		}
	}

	// Validate instance
	errors := instance.Validate()
	if len(errors) > 0 {
		// Join error messages
		errorMsgs := make([]string, 0, len(errors))
		for _, err := range errors {
			errorMsgs = append(errorMsgs, err.Error())
		}
		return nil, connect.NewError(connect.CodeInvalidArgument, fmt.Errorf("validation failed: %s", strings.Join(errorMsgs, "; ")))
	}

	// Save the instance
	err = s.instanceRepo.Save(ctx, instance)
	if err != nil {
		return nil, connect.NewError(connect.CodeInternal, fmt.Errorf("failed to save instance: %w", err))
	}

	return connect.NewResponse(&flexitypev1.InstanceResponse{
		Instance: domainInstanceToProto(instance),
	}), nil
}

func (s *flexiTypeServiceServer) GetInstance(ctx context.Context, req *connect.Request[flexitypev1.GetInstanceRequest]) (*connect.Response[flexitypev1.InstanceResponse], error) {
	// Get the instance by ID
	instance, err := s.instanceRepo.GetByID(ctx, req.Msg.Id)
	if err != nil {
		return nil, connect.NewError(connect.CodeNotFound, fmt.Errorf("instance not found: %w", err))
	}

	return connect.NewResponse(&flexitypev1.InstanceResponse{
		Instance: domainInstanceToProto(instance),
	}), nil
}

// QueryInstances - UPDATED to use QueryWithOptions with IncludeArchived
func (s *flexiTypeServiceServer) QueryInstances(ctx context.Context, req *connect.Request[flexitypev1.QueryInstancesRequest]) (*connect.Response[flexitypev1.QueryInstancesResponse], error) {
	// Convert attribute filters from proto to domain
	filters := make(map[string]interface{})
	for name, value := range req.Msg.AttributeFilters {
		filters[name] = protoValueToDomain(value)
	}

	// Create query options
	options := &ports.QueryOptions{
		TypeID:           req.Msg.TypeId,
		AttributeFilters: filters,
		Limit:            int(req.Msg.PageSize),
		IncludeArchived:  req.Msg.IncludeArchived,
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

	// Query instances with options
	instances, totalCount, err := s.instanceRepo.QueryWithOptions(ctx, options)
	if err != nil {
		return nil, connect.NewError(connect.CodeInternal, fmt.Errorf("failed to query instances: %w", err))
	}

	// Prepare next page token
	var nextPageToken string
	if options.Offset+len(instances) < totalCount {
		nextPageToken = fmt.Sprintf("%d", options.Offset+len(instances))
	}

	// Convert to proto
	protoInstances := make([]*flexitypev1.Instance, 0, len(instances))
	for _, instance := range instances {
		protoInstances = append(protoInstances, domainInstanceToProto(instance))
	}

	// Build response with pagination
	return connect.NewResponse(&flexitypev1.QueryInstancesResponse{
		Instances:     protoInstances,
		NextPageToken: nextPageToken,
	}), nil
}

func (s *flexiTypeServiceServer) UpdateInstance(ctx context.Context, req *connect.Request[flexitypev1.UpdateInstanceRequest]) (*connect.Response[flexitypev1.InstanceResponse], error) {
	// Get the instance by ID
	instance, err := s.instanceRepo.GetByID(ctx, req.Msg.Id)
	if err != nil {
		return nil, connect.NewError(connect.CodeNotFound, fmt.Errorf("instance not found: %w", err))
	}

	// Check if the instance's type version is current
	if instance.TypeVersion != instance.TypeDefinition.Version {
		// Attempt to migrate the instance to the latest version
		errors := instance.MigrateToLatestVersion()
		if len(errors) > 0 {
			// Join error messages
			errorMsgs := make([]string, 0, len(errors))
			for _, err := range errors {
				errorMsgs = append(errorMsgs, err.Error())
			}
			return nil, connect.NewError(connect.CodeFailedPrecondition,
				fmt.Errorf("instance migration failed: %s", strings.Join(errorMsgs, "; ")))
		}
	}

	// Convert attribute values from proto to domain
	attrValues := make(map[string]interface{})
	for name, value := range req.Msg.AttributeValues {
		attrValues[name] = protoValueToDomain(value)
	}

	// Update attribute values
	for name, value := range attrValues {
		err := instance.SetAttribute(name, value)
		if err != nil {
			return nil, connect.NewError(connect.CodeInvalidArgument, fmt.Errorf("failed to set attribute '%s': %w", name, err))
		}
	}

	// Validate the updated instance
	errors := instance.Validate()
	if len(errors) > 0 {
		// Join error messages
		errorMsgs := make([]string, 0, len(errors))
		for _, err := range errors {
			errorMsgs = append(errorMsgs, err.Error())
		}
		return nil, connect.NewError(connect.CodeInvalidArgument, fmt.Errorf("validation failed: %s", strings.Join(errorMsgs, "; ")))
	}

	// Save the updated instance
	err = s.instanceRepo.Save(ctx, instance)
	if err != nil {
		return nil, connect.NewError(connect.CodeInternal, fmt.Errorf("failed to save instance: %w", err))
	}

	return connect.NewResponse(&flexitypev1.InstanceResponse{
		Instance: domainInstanceToProto(instance),
	}), nil
}

// ArchiveType archives a type definition
func (s *flexiTypeServiceServer) ArchiveType(
	ctx context.Context,
	req *connect.Request[flexitypev1.ArchiveTypeRequest],
) (*connect.Response[flexitypev1.TypeResponse], error) {
	// Get the type definition to ensure it exists
	typeDef, err := s.typeRepo.GetByID(ctx, req.Msg.Id)
	if err != nil {
		return nil, connect.NewError(connect.CodeNotFound, fmt.Errorf("type not found: %w", err))
	}

	// Archive the type
	err = s.typeRepo.Archive(ctx, req.Msg.Id)
	if err != nil {
		return nil, connect.NewError(connect.CodeInternal, fmt.Errorf("failed to archive type: %w", err))
	}

	// Get the updated type definition with the archived_at timestamp
	typeDef, err = s.typeRepo.GetByID(ctx, req.Msg.Id)
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
	// Get the type definition to ensure it exists
	typeDef, err := s.typeRepo.GetByID(ctx, req.Msg.Id)
	if err != nil {
		return nil, connect.NewError(connect.CodeNotFound, fmt.Errorf("type not found: %w", err))
	}

	// Check if the type is actually archived
	if typeDef.ArchivedAt == nil {
		return nil, connect.NewError(connect.CodeFailedPrecondition, fmt.Errorf("type is not archived"))
	}

	// Unarchive the type
	err = s.typeRepo.Unarchive(ctx, req.Msg.Id)
	if err != nil {
		return nil, connect.NewError(connect.CodeInternal, fmt.Errorf("failed to unarchive type: %w", err))
	}

	// Get the updated type definition with the archived_at timestamp cleared
	typeDef, err = s.typeRepo.GetByID(ctx, req.Msg.Id)
	if err != nil {
		return nil, connect.NewError(connect.CodeInternal, fmt.Errorf("failed to retrieve updated type: %w", err))
	}

	return connect.NewResponse(&flexitypev1.TypeResponse{
		Type: domainTypeToProto(typeDef),
	}), nil
}

func (s *flexiTypeServiceServer) ExportTypeSchema(ctx context.Context, req *connect.Request[flexitypev1.ExportTypeSchemaRequest]) (*connect.Response[flexitypev1.SchemaResponse], error) {
	// Get the type definition
	typeDef, err := s.typeRepo.GetByID(ctx, req.Msg.TypeId)
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
	// Get the instance to ensure it exists
	instance, err := s.instanceRepo.GetByID(ctx, req.Msg.Id)
	if err != nil {
		return nil, connect.NewError(connect.CodeNotFound, fmt.Errorf("instance not found: %w", err))
	}

	// Archive the instance
	err = s.instanceRepo.Archive(ctx, req.Msg.Id)
	if err != nil {
		return nil, connect.NewError(connect.CodeInternal, fmt.Errorf("failed to archive instance: %w", err))
	}

	// Get the updated instance with the archived_at timestamp
	instance, err = s.instanceRepo.GetByID(ctx, req.Msg.Id)
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
	// Get the instance to ensure it exists
	instance, err := s.instanceRepo.GetByID(ctx, req.Msg.Id)
	if err != nil {
		return nil, connect.NewError(connect.CodeNotFound, fmt.Errorf("instance not found: %w", err))
	}

	// Check if the instance is actually archived
	if instance.ArchivedAt == nil {
		return nil, connect.NewError(connect.CodeFailedPrecondition, fmt.Errorf("instance is not archived"))
	}

	// Unarchive the instance
	err = s.instanceRepo.Unarchive(ctx, req.Msg.Id)
	if err != nil {
		return nil, connect.NewError(connect.CodeInternal, fmt.Errorf("failed to unarchive instance: %w", err))
	}

	// Get the updated instance with the archived_at timestamp cleared
	instance, err = s.instanceRepo.GetByID(ctx, req.Msg.Id)
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

	// Check if type with same ID already exists
	existing, err := s.typeRepo.GetByID(ctx, typeDef.ID)
	if err == nil && existing != nil {
		return nil, connect.NewError(connect.CodeAlreadyExists, fmt.Errorf("type with ID '%s' already exists", typeDef.ID))
	}

	// Save the type
	err = s.typeRepo.Save(ctx, typeDef)
	if err != nil {
		return nil, connect.NewError(connect.CodeInternal, fmt.Errorf("failed to save type: %w", err))
	}

	return connect.NewResponse(&flexitypev1.TypeResponse{
		Type: domainTypeToProto(typeDef),
	}), nil
}
