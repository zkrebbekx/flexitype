package connect

import (
	"context"
	"time"

	"connectrpc.com/connect"
	"github.com/zkrebbekx/flexitype/internal/domain/attribute"
	"github.com/zkrebbekx/api/connect/pb"
	"github.com/bufbuild/connect-go"
	"google.golang.org/protobuf/types/known/timestamppb"
)

// Attribute operations
func (s *Service) CreateAttribute(
	ctx context.Context,
	req *connect.Request[pb.Attribute],
) (*connect.Response[pb.Attribute], error) {
	attr, err := s.attributeService.CreateAttribute(ctx, &service.CreateAttributeDetails{
		TypeDefinitionID: req.Msg.TypeDefinitionId,
		Name:            req.Msg.Name,
		Description:     req.Msg.Description,
		DataType:        req.Msg.DataType,
		Required:        req.Msg.Required,
		ValidationRules: req.Msg.ValidationRules,
	})
	if err != nil {
		return nil, err
	}

	res := connect.NewResponse(&pb.Attribute{
		Id:               attr.ID,
		TypeDefinitionId: attr.TypeDefinitionID,
		Name:            attr.Name,
		Description:     attr.Description,
		DataType:        attr.DataType,
		Required:        attr.Required,
		ValidationRules: attr.ValidationRules,
		Version:         attr.Version,
		CreatedAt:       timestamppb.New(attr.CreatedAt),
		UpdatedAt:       timestamppb.New(attr.UpdatedAt),
		ArchivedAt:      timestamppb.New(attr.ArchivedAt),
	})
	return res, nil
}

func (s *Service) GetAttribute(
	ctx context.Context,
	req *connect.Request[pb.GetAttributeRequest],
) (*connect.Response[pb.Attribute], error) {
	attr, err := s.attributeService.GetAttribute(ctx, req.Msg.Id)
	if err != nil {
		return nil, err
	}

	res := connect.NewResponse(&pb.Attribute{
		Id:               attr.ID,
		TypeDefinitionId: attr.TypeDefinitionID,
		Name:            attr.Name,
		Description:     attr.Description,
		DataType:        attr.DataType,
		Required:        attr.Required,
		ValidationRules: attr.ValidationRules,
		Version:         attr.Version,
		CreatedAt:       timestamppb.New(attr.CreatedAt),
		UpdatedAt:       timestamppb.New(attr.UpdatedAt),
		ArchivedAt:      timestamppb.New(attr.ArchivedAt),
	})
	return res, nil
}

func (s *Service) UpdateAttribute(
	ctx context.Context,
	req *connect.Request[pb.Attribute],
) (*connect.Response[pb.Attribute], error) {
	attr, err := s.attributeService.UpdateAttribute(ctx, &service.UpdateAttributeDetails{
		ID:               req.Msg.Id,
		TypeDefinitionID: req.Msg.TypeDefinitionId,
		Name:            req.Msg.Name,
		Description:     req.Msg.Description,
		DataType:        req.Msg.DataType,
		Required:        req.Msg.Required,
		ValidationRules: req.Msg.ValidationRules,
	})
	if err != nil {
		return nil, err
	}

	res := connect.NewResponse(&pb.Attribute{
		Id:               attr.ID,
		TypeDefinitionId: attr.TypeDefinitionID,
		Name:            attr.Name,
		Description:     attr.Description,
		DataType:        attr.DataType,
		Required:        attr.Required,
		ValidationRules: attr.ValidationRules,
		Version:         attr.Version,
		CreatedAt:       timestamppb.New(attr.CreatedAt),
		UpdatedAt:       timestamppb.New(attr.UpdatedAt),
		ArchivedAt:      timestamppb.New(attr.ArchivedAt),
	})
	return res, nil
}

func (s *Service) ArchiveAttribute(
	ctx context.Context,
	req *connect.Request[pb.ArchiveAttributeRequest],
) (*connect.Response[pb.Attribute], error) {
	attr, err := s.attributeService.ArchiveAttribute(ctx, req.Msg.Id)
	if err != nil {
		return nil, err
	}

	res := connect.NewResponse(&pb.Attribute{
		Id:               attr.ID,
		TypeDefinitionId: attr.TypeDefinitionID,
		Name:            attr.Name,
		Description:     attr.Description,
		DataType:        attr.DataType,
		Required:        attr.Required,
		ValidationRules: attr.ValidationRules,
		Version:         attr.Version,
		CreatedAt:       timestamppb.New(attr.CreatedAt),
		UpdatedAt:       timestamppb.New(attr.UpdatedAt),
		ArchivedAt:      timestamppb.New(attr.ArchivedAt),
	})
	return res, nil
}

func (s *Service) ListAttributes(
	ctx context.Context,
	req *connect.Request[pb.ListAttributesRequest],
) (*connect.Response[pb.ListAttributesResponse], error) {
	filter := &service.AttributeFilter{
		Version:           req.Msg.Version,
		LatestVersion:     req.Msg.LatestVersion,
		IDs:               req.Msg.Ids,
		TypeDefinitionID: req.Msg.TypeDefinitionId,
	}

	attrs, err := s.attributeService.ListAttributes(ctx, filter)
	if err != nil {
		return nil, err
	}

	response := &pb.ListAttributesResponse{
		Attributes: make([]*pb.Attribute, len(attrs)),
	}

	for i, attr := range attrs {
		response.Attributes[i] = &pb.Attribute{
			Id:               attr.ID,
			TypeDefinitionId: attr.TypeDefinitionID,
			Name:            attr.Name,
			Description:     attr.Description,
			DataType:        attr.DataType,
			Required:        attr.Required,
			ValidationRules: attr.ValidationRules,
			Version:         attr.Version,
			CreatedAt:       timestamppb.New(attr.CreatedAt),
			UpdatedAt:       timestamppb.New(attr.UpdatedAt),
			ArchivedAt:      timestamppb.New(attr.ArchivedAt),
		}
	}

	res := connect.NewResponse(response)
	return res, nil
}

func (s *Service) GetAttributesByTypeDefinitionID(
	ctx context.Context,
	req *connect.Request[pb.GetAttributesByTypeDefinitionIDRequest],
) (*connect.Response[pb.ListAttributesResponse], error) {
	attrs, err := s.attributeService.GetAttributesByTypeDefinitionID(ctx, req.Msg.TypeDefinitionId)
	if err != nil {
		return nil, err
	}

	response := &pb.ListAttributesResponse{
		Attributes: make([]*pb.Attribute, len(attrs)),
	}

	for i, attr := range attrs {
		response.Attributes[i] = &pb.Attribute{
			Id:               attr.ID,
			TypeDefinitionId: attr.TypeDefinitionID,
			Name:            attr.Name,
			Description:     attr.Description,
			DataType:        attr.DataType,
			Required:        attr.Required,
			ValidationRules: attr.ValidationRules,
			Version:         attr.Version,
			CreatedAt:       timestamppb.New(attr.CreatedAt),
			UpdatedAt:       timestamppb.New(attr.UpdatedAt),
			ArchivedAt:      timestamppb.New(attr.ArchivedAt),
		}
	}

	res := connect.NewResponse(response)
	return res, nil
} 