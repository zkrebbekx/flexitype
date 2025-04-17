package grpc

import (
	"context"
	"fmt"
	"strings"

	"github.com/bufbuild/connect-go"

	"github.com/zac300/flexitype/api/flexitypev1"
	"github.com/zac300/flexitype/internal/ports"
)

func (s *flexiTypeServiceServer) GetInstance(ctx context.Context, req *connect.Request[flexitypev1.GetInstanceRequest]) (*connect.Response[flexitypev1.InstanceResponse], error) {
	// Get the instance by ID using the instance service
	instance, err := s.instanceService.GetInstance(ctx, req.Msg.Id)
	if err != nil {
		return nil, connect.NewError(connect.CodeNotFound, fmt.Errorf("instance not found: %w", err))
	}

	return connect.NewResponse(&flexitypev1.InstanceResponse{
		Instance: domainInstanceToProto(instance),
	}), nil
}

// GetInstanceVersion retrieves a specific version of an instance by ID and version
func (s *flexiTypeServiceServer) GetInstanceVersion(ctx context.Context, req *connect.Request[flexitypev1.GetInstanceVersionRequest]) (*connect.Response[flexitypev1.InstanceResponse], error) {
	// Get the instance by ID and version using the instance service
	instance, err := s.instanceService.GetInstanceVersion(ctx, req.Msg.Id, int(req.Msg.Version))
	if err != nil {
		return nil, connect.NewError(connect.CodeNotFound, fmt.Errorf("instance version not found: %w", err))
	}

	return connect.NewResponse(&flexitypev1.InstanceResponse{
		Instance: domainInstanceToProto(instance),
	}), nil
}

// GetAllInstanceVersions retrieves all versions of an instance by ID
func (s *flexiTypeServiceServer) GetAllInstanceVersions(ctx context.Context, req *connect.Request[flexitypev1.GetAllInstanceVersionsRequest]) (*connect.Response[flexitypev1.InstanceVersionsResponse], error) {
	// Get all versions of the instance using the instance service
	instances, err := s.instanceService.GetAllInstanceVersions(ctx, req.Msg.Id)
	if err != nil {
		return nil, connect.NewError(connect.CodeNotFound, fmt.Errorf("instance not found: %w", err))
	}

	// Convert to proto instances
	protoInstances := make([]*flexitypev1.Instance, 0, len(instances))
	for _, instance := range instances {
		protoInstances = append(protoInstances, domainInstanceToProto(instance))
	}

	return connect.NewResponse(&flexitypev1.InstanceVersionsResponse{
		Instances: protoInstances,
	}), nil
}

func (s *flexiTypeServiceServer) CreateInstance(ctx context.Context, req *connect.Request[flexitypev1.CreateInstanceRequest]) (*connect.Response[flexitypev1.InstanceResponse], error) {
	// Convert attribute values from proto to domain
	attrValues := make(map[string]interface{})
	for name, value := range req.Msg.AttributeValues {
		attrValues[name] = protoValueToDomain(value)
	}

	// Use the instance service to create the instance
	instance, err := s.instanceService.CreateInstance(ctx, req.Msg.Id, req.Msg.TypeId, attrValues)
	if err != nil {
		// Check error type to determine appropriate error code
		if strings.Contains(err.Error(), "validation failed") {
			return nil, connect.NewError(connect.CodeInvalidArgument, err)
		}
		if strings.Contains(err.Error(), "not found") {
			return nil, connect.NewError(connect.CodeNotFound, err)
		}
		return nil, connect.NewError(connect.CodeInternal, err)
	}

	return connect.NewResponse(&flexitypev1.InstanceResponse{
		Instance: domainInstanceToProto(instance),
	}), nil
}

// UpdateInstance handles updating an instance by creating a new version
func (s *flexiTypeServiceServer) UpdateInstance(ctx context.Context, req *connect.Request[flexitypev1.UpdateInstanceRequest]) (*connect.Response[flexitypev1.InstanceResponse], error) {
	// Convert attribute values from proto to domain
	attrValues := make(map[string]interface{})
	for name, value := range req.Msg.AttributeValues {
		attrValues[name] = protoValueToDomain(value)
	}

	// Use the instance service to update the instance
	newInstance, err := s.instanceService.UpdateInstance(ctx, req.Msg.Id, attrValues)
	if err != nil {
		// Check error type to determine appropriate error code
		if strings.Contains(err.Error(), "validation failed") {
			return nil, connect.NewError(connect.CodeInvalidArgument, err)
		}
		if strings.Contains(err.Error(), "not found") {
			return nil, connect.NewError(connect.CodeNotFound, err)
		}
		return nil, connect.NewError(connect.CodeInternal, err)
	}

	return connect.NewResponse(&flexitypev1.InstanceResponse{
		Instance: domainInstanceToProto(newInstance),
	}), nil
}

// QueryInstances implements the QueryInstances RPC method with versioning support
func (s *flexiTypeServiceServer) QueryInstances(ctx context.Context, req *connect.Request[flexitypev1.QueryInstancesRequest]) (*connect.Response[flexitypev1.QueryInstancesResponse], error) {
	// Convert attribute filters from proto to domain
	filters := make(map[string]interface{})
	for name, value := range req.Msg.AttributeFilters {
		filters[name] = protoValueToDomain(value)
	}

	// Create query options
	options := &ports.QueryOptions{
		TypeID:            req.Msg.TypeId,
		AttributeFilters:  filters,
		Limit:             int(req.Msg.PageSize),
		IncludeArchived:   req.Msg.IncludeArchived,
		LatestVersionOnly: req.Msg.LatestVersionOnly, // Use the versioning filter
	}

	// Apply specific version filter if requested
	if req.Msg.SpecificVersion > 0 {
		options.InstanceVersion = int(req.Msg.SpecificVersion)
		options.LatestVersionOnly = false // If specific version is requested, we don't want latest only
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

	// Query instances with options using the instance service
	instances, totalCount, err := s.instanceService.QueryInstancesWithOptions(ctx, options)
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
