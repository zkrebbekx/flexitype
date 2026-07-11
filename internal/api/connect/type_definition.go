package connect

import (
	"context"
	"time"

	"connectrpc.com/connect"
	"github.com/zkrebbekx/flexitype/internal/domain/type_definition"
	"github.com/zkrebbekx/api/connect/pb"
	"google.golang.org/protobuf/types/known/timestamppb"
)

// Type Definition operations
func (s *Service) CreateTypeDefinition(
	ctx context.Context,
	req *connect.Request[pb.TypeDefinition],
) (*connect.Response[pb.TypeDefinition], error) {
	td, err := s.typeDefinitionService.CreateTypeDefinition(ctx, &service.CreateTypeDefinitionDetails{
		Name:        req.Msg.Name,
		Description: req.Msg.Description,
	})
	if err != nil {
		return nil, err
	}

	res := connect.NewResponse(&pb.TypeDefinition{
		Id:          td.ID,
		Name:        td.Name,
		Description: td.Description,
		Version:     td.Version,
		CreatedAt:   timestamppb.New(td.CreatedAt),
		UpdatedAt:   timestamppb.New(td.UpdatedAt),
		ArchivedAt:  timestamppb.New(td.ArchivedAt),
	})
	return res, nil
}

func (s *Service) GetTypeDefinition(
	ctx context.Context,
	req *connect.Request[pb.GetTypeDefinitionRequest],
) (*connect.Response[pb.TypeDefinition], error) {
	td, err := s.typeDefinitionService.GetTypeDefinition(ctx, req.Msg.Id)
	if err != nil {
		return nil, err
	}

	res := connect.NewResponse(&pb.TypeDefinition{
		Id:          td.ID,
		Name:        td.Name,
		Description: td.Description,
		Version:     td.Version,
		CreatedAt:   timestamppb.New(td.CreatedAt),
		UpdatedAt:   timestamppb.New(td.UpdatedAt),
		ArchivedAt:  timestamppb.New(td.ArchivedAt),
	})
	return res, nil
}

func (s *Service) UpdateTypeDefinition(
	ctx context.Context,
	req *connect.Request[pb.TypeDefinition],
) (*connect.Response[pb.TypeDefinition], error) {
	td, err := s.typeDefinitionService.UpdateTypeDefinition(ctx, &service.UpdateTypeDefinitionDetails{
		ID:          req.Msg.Id,
		Name:        req.Msg.Name,
		Description: req.Msg.Description,
	})
	if err != nil {
		return nil, err
	}

	res := connect.NewResponse(&pb.TypeDefinition{
		Id:          td.ID,
		Name:        td.Name,
		Description: td.Description,
		Version:     td.Version,
		CreatedAt:   timestamppb.New(td.CreatedAt),
		UpdatedAt:   timestamppb.New(td.UpdatedAt),
		ArchivedAt:  timestamppb.New(td.ArchivedAt),
	})
	return res, nil
}

func (s *Service) ArchiveTypeDefinition(
	ctx context.Context,
	req *connect.Request[pb.ArchiveTypeDefinitionRequest],
) (*connect.Response[pb.TypeDefinition], error) {
	td, err := s.typeDefinitionService.ArchiveTypeDefinition(ctx, req.Msg.Id)
	if err != nil {
		return nil, err
	}

	res := connect.NewResponse(&pb.TypeDefinition{
		Id:          td.ID,
		Name:        td.Name,
		Description: td.Description,
		Version:     td.Version,
		CreatedAt:   timestamppb.New(td.CreatedAt),
		UpdatedAt:   timestamppb.New(td.UpdatedAt),
		ArchivedAt:  timestamppb.New(td.ArchivedAt),
	})
	return res, nil
}

func (s *Service) ListTypeDefinitions(
	ctx context.Context,
	req *connect.Request[pb.ListTypeDefinitionsRequest],
) (*connect.Response[pb.ListTypeDefinitionsResponse], error) {
	filter := &service.TypeDefinitionFilter{
		Version:       req.Msg.Version,
		LatestVersion: req.Msg.LatestVersion,
		IDs:           req.Msg.Ids,
	}

	tds, err := s.typeDefinitionService.ListTypeDefinitions(ctx, filter)
	if err != nil {
		return nil, err
	}

	response := &pb.ListTypeDefinitionsResponse{
		TypeDefinitions: make([]*pb.TypeDefinition, len(tds)),
	}

	for i, td := range tds {
		response.TypeDefinitions[i] = &pb.TypeDefinition{
			Id:          td.ID,
			Name:        td.Name,
			Description: td.Description,
			Version:     td.Version,
			CreatedAt:   timestamppb.New(td.CreatedAt),
			UpdatedAt:   timestamppb.New(td.UpdatedAt),
			ArchivedAt:  timestamppb.New(td.ArchivedAt),
		}
	}

	res := connect.NewResponse(response)
	return res, nil
} 