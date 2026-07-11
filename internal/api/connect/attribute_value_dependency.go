package service

import (
	"context"

	"github.com/zkrebbekx/flexitype/api/connect/pb"
	"github.com/zkrebbekx/flexitype/internal/domain/service"
	"connectrpc.com/connect"
	"google.golang.org/protobuf/types/known/timestamppb"
)

// AttributeValueDependency operations
func (s *Service) CreateAttributeValueDependency(
	ctx context.Context,
	req *connect.Request[pb.AttributeValueDependency],
) (*connect.Response[pb.AttributeValueDependency], error) {
	dependency, err := s.attributeValueDependencyService.CreateAttributeValueDependency(ctx, &service.CreateAttributeValueDependencyDetails{
		SourceAttributeValueID: req.Msg.SourceAttributeValueId,
		TargetAttributeValueID: req.Msg.TargetAttributeValueId,
		Type:                   req.Msg.Type,
	})
	if err != nil {
		return nil, err
	}

	res := connect.NewResponse(&pb.AttributeValueDependency{
		Id:                     dependency.ID,
		SourceAttributeValueId: dependency.SourceAttributeValueID,
		TargetAttributeValueId: dependency.TargetAttributeValueID,
		Type:                   dependency.Type,
		Version:                dependency.Version,
		CreatedAt:              timestamppb.New(dependency.CreatedAt),
		UpdatedAt:              timestamppb.New(dependency.UpdatedAt),
		ArchivedAt:             timestamppb.New(dependency.ArchivedAt),
	})
	return res, nil
}

func (s *Service) GetAttributeValueDependency(
	ctx context.Context,
	req *connect.Request[pb.GetAttributeValueDependencyRequest],
) (*connect.Response[pb.AttributeValueDependency], error) {
	dependency, err := s.attributeValueDependencyService.GetAttributeValueDependency(ctx, req.Msg.Id)
	if err != nil {
		return nil, err
	}

	res := connect.NewResponse(&pb.AttributeValueDependency{
		Id:                     dependency.ID,
		SourceAttributeValueId: dependency.SourceAttributeValueID,
		TargetAttributeValueId: dependency.TargetAttributeValueID,
		Type:                   dependency.Type,
		Version:                dependency.Version,
		CreatedAt:              timestamppb.New(dependency.CreatedAt),
		UpdatedAt:              timestamppb.New(dependency.UpdatedAt),
		ArchivedAt:             timestamppb.New(dependency.ArchivedAt),
	})
	return res, nil
}

func (s *Service) UpdateAttributeValueDependency(
	ctx context.Context,
	req *connect.Request[pb.AttributeValueDependency],
) (*connect.Response[pb.AttributeValueDependency], error) {
	dependency, err := s.attributeValueDependencyService.UpdateAttributeValueDependency(ctx, &service.UpdateAttributeValueDependencyDetails{
		ID:                     req.Msg.Id,
		SourceAttributeValueID: req.Msg.SourceAttributeValueId,
		TargetAttributeValueID: req.Msg.TargetAttributeValueId,
		Type:                   req.Msg.Type,
	})
	if err != nil {
		return nil, err
	}

	res := connect.NewResponse(&pb.AttributeValueDependency{
		Id:                     dependency.ID,
		SourceAttributeValueId: dependency.SourceAttributeValueID,
		TargetAttributeValueId: dependency.TargetAttributeValueID,
		Type:                   dependency.Type,
		Version:                dependency.Version,
		CreatedAt:              timestamppb.New(dependency.CreatedAt),
		UpdatedAt:              timestamppb.New(dependency.UpdatedAt),
		ArchivedAt:             timestamppb.New(dependency.ArchivedAt),
	})
	return res, nil
}

func (s *Service) ArchiveAttributeValueDependency(
	ctx context.Context,
	req *connect.Request[pb.ArchiveAttributeValueDependencyRequest],
) (*connect.Response[pb.AttributeValueDependency], error) {
	dependency, err := s.attributeValueDependencyService.ArchiveAttributeValueDependency(ctx, req.Msg.Id)
	if err != nil {
		return nil, err
	}

	res := connect.NewResponse(&pb.AttributeValueDependency{
		Id:                     dependency.ID,
		SourceAttributeValueId: dependency.SourceAttributeValueID,
		TargetAttributeValueId: dependency.TargetAttributeValueID,
		Type:                   dependency.Type,
		Version:                dependency.Version,
		CreatedAt:              timestamppb.New(dependency.CreatedAt),
		UpdatedAt:              timestamppb.New(dependency.UpdatedAt),
		ArchivedAt:             timestamppb.New(dependency.ArchivedAt),
	})
	return res, nil
}

func (s *Service) ListAttributeValueDependencies(
	ctx context.Context,
	req *connect.Request[pb.ListAttributeValueDependenciesRequest],
) (*connect.Response[pb.ListAttributeValueDependenciesResponse], error) {
	filter := &service.AttributeValueDependencyFilter{
		Version:                req.Msg.Version,
		LatestVersion:          req.Msg.LatestVersion,
		IDs:                    req.Msg.Ids,
		SourceAttributeValueID: req.Msg.SourceAttributeValueId,
		TargetAttributeValueID: req.Msg.TargetAttributeValueId,
	}

	dependencies, err := s.attributeValueDependencyService.ListAttributeValueDependencies(ctx, filter)
	if err != nil {
		return nil, err
	}

	response := &pb.ListAttributeValueDependenciesResponse{
		AttributeValueDependencies: make([]*pb.AttributeValueDependency, len(dependencies)),
	}

	for i, dependency := range dependencies {
		response.AttributeValueDependencies[i] = &pb.AttributeValueDependency{
			Id:                     dependency.ID,
			SourceAttributeValueId: dependency.SourceAttributeValueID,
			TargetAttributeValueId: dependency.TargetAttributeValueID,
			Type:                   dependency.Type,
			Version:                dependency.Version,
			CreatedAt:              timestamppb.New(dependency.CreatedAt),
			UpdatedAt:              timestamppb.New(dependency.UpdatedAt),
			ArchivedAt:             timestamppb.New(dependency.ArchivedAt),
		}
	}

	res := connect.NewResponse(response)
	return res, nil
}

func (s *Service) GetAttributeValueDependenciesBySourceAttributeValueID(
	ctx context.Context,
	req *connect.Request[pb.GetAttributeValueDependenciesBySourceAttributeValueIDRequest],
) (*connect.Response[pb.ListAttributeValueDependenciesResponse], error) {
	dependencies, err := s.attributeValueDependencyService.GetAttributeValueDependenciesBySourceAttributeValueID(ctx, req.Msg.SourceAttributeValueId)
	if err != nil {
		return nil, err
	}

	response := &pb.ListAttributeValueDependenciesResponse{
		AttributeValueDependencies: make([]*pb.AttributeValueDependency, len(dependencies)),
	}

	for i, dependency := range dependencies {
		response.AttributeValueDependencies[i] = &pb.AttributeValueDependency{
			Id:                     dependency.ID,
			SourceAttributeValueId: dependency.SourceAttributeValueID,
			TargetAttributeValueId: dependency.TargetAttributeValueID,
			Type:                   dependency.Type,
			Version:                dependency.Version,
			CreatedAt:              timestamppb.New(dependency.CreatedAt),
			UpdatedAt:              timestamppb.New(dependency.UpdatedAt),
			ArchivedAt:             timestamppb.New(dependency.ArchivedAt),
		}
	}

	res := connect.NewResponse(response)
	return res, nil
}

func (s *Service) GetAttributeValueDependenciesByTargetAttributeValueID(
	ctx context.Context,
	req *connect.Request[pb.GetAttributeValueDependenciesByTargetAttributeValueIDRequest],
) (*connect.Response[pb.ListAttributeValueDependenciesResponse], error) {
	dependencies, err := s.attributeValueDependencyService.GetAttributeValueDependenciesByTargetAttributeValueID(ctx, req.Msg.TargetAttributeValueId)
	if err != nil {
		return nil, err
	}

	response := &pb.ListAttributeValueDependenciesResponse{
		AttributeValueDependencies: make([]*pb.AttributeValueDependency, len(dependencies)),
	}

	for i, dependency := range dependencies {
		response.AttributeValueDependencies[i] = &pb.AttributeValueDependency{
			Id:                     dependency.ID,
			SourceAttributeValueId: dependency.SourceAttributeValueID,
			TargetAttributeValueId: dependency.TargetAttributeValueID,
			Type:                   dependency.Type,
			Version:                dependency.Version,
			CreatedAt:              timestamppb.New(dependency.CreatedAt),
			UpdatedAt:              timestamppb.New(dependency.UpdatedAt),
			ArchivedAt:             timestamppb.New(dependency.ArchivedAt),
		}
	}

	res := connect.NewResponse(response)
	return res, nil
} 