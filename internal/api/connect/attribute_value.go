package connect

import (
	"context"
	"time"

	"connectrpc.com/connect"
	"github.com/zkrebbekx/flexitype/internal/domain/attribute_value"
	"github.com/zkrebbekx/api/connect/pb"
	"google.golang.org/protobuf/types/known/timestamppb"
)

// AttributeValue operations
func (s *Service) CreateAttributeValue(
	ctx context.Context,
	req *connect.Request[pb.AttributeValue],
) (*connect.Response[pb.AttributeValue], error) {
	attrValue, err := s.attributeValueService.CreateAttributeValue(ctx, &service.CreateAttributeValueDetails{
		AttributeID: req.Msg.AttributeId,
		Value:       req.Msg.Value,
	})
	if err != nil {
		return nil, err
	}

	res := connect.NewResponse(&pb.AttributeValue{
		Id:          attrValue.ID,
		AttributeId: attrValue.AttributeID,
		Value:       attrValue.Value,
		Version:     attrValue.Version,
		CreatedAt:   timestamppb.New(attrValue.CreatedAt),
		UpdatedAt:   timestamppb.New(attrValue.UpdatedAt),
		ArchivedAt:  timestamppb.New(attrValue.ArchivedAt),
	})
	return res, nil
}

func (s *Service) GetAttributeValue(
	ctx context.Context,
	req *connect.Request[pb.GetAttributeValueRequest],
) (*connect.Response[pb.AttributeValue], error) {
	attrValue, err := s.attributeValueService.GetAttributeValue(ctx, req.Msg.Id)
	if err != nil {
		return nil, err
	}

	res := connect.NewResponse(&pb.AttributeValue{
		Id:          attrValue.ID,
		AttributeId: attrValue.AttributeID,
		Value:       attrValue.Value,
		Version:     attrValue.Version,
		CreatedAt:   timestamppb.New(attrValue.CreatedAt),
		UpdatedAt:   timestamppb.New(attrValue.UpdatedAt),
		ArchivedAt:  timestamppb.New(attrValue.ArchivedAt),
	})
	return res, nil
}

func (s *Service) UpdateAttributeValue(
	ctx context.Context,
	req *connect.Request[pb.AttributeValue],
) (*connect.Response[pb.AttributeValue], error) {
	attrValue, err := s.attributeValueService.UpdateAttributeValue(ctx, &service.UpdateAttributeValueDetails{
		ID:          req.Msg.Id,
		AttributeID: req.Msg.AttributeId,
		Value:       req.Msg.Value,
	})
	if err != nil {
		return nil, err
	}

	res := connect.NewResponse(&pb.AttributeValue{
		Id:          attrValue.ID,
		AttributeId: attrValue.AttributeID,
		Value:       attrValue.Value,
		Version:     attrValue.Version,
		CreatedAt:   timestamppb.New(attrValue.CreatedAt),
		UpdatedAt:   timestamppb.New(attrValue.UpdatedAt),
		ArchivedAt:  timestamppb.New(attrValue.ArchivedAt),
	})
	return res, nil
}

func (s *Service) ArchiveAttributeValue(
	ctx context.Context,
	req *connect.Request[pb.ArchiveAttributeValueRequest],
) (*connect.Response[pb.AttributeValue], error) {
	attrValue, err := s.attributeValueService.ArchiveAttributeValue(ctx, req.Msg.Id)
	if err != nil {
		return nil, err
	}

	res := connect.NewResponse(&pb.AttributeValue{
		Id:          attrValue.ID,
		AttributeId: attrValue.AttributeID,
		Value:       attrValue.Value,
		Version:     attrValue.Version,
		CreatedAt:   timestamppb.New(attrValue.CreatedAt),
		UpdatedAt:   timestamppb.New(attrValue.UpdatedAt),
		ArchivedAt:  timestamppb.New(attrValue.ArchivedAt),
	})
	return res, nil
}

func (s *Service) ListAttributeValues(
	ctx context.Context,
	req *connect.Request[pb.ListAttributeValuesRequest],
) (*connect.Response[pb.ListAttributeValuesResponse], error) {
	filter := &service.AttributeValueFilter{
		Version:       req.Msg.Version,
		LatestVersion: req.Msg.LatestVersion,
		IDs:           req.Msg.Ids,
		AttributeID:   req.Msg.AttributeId,
	}

	attrValues, err := s.attributeValueService.ListAttributeValues(ctx, filter)
	if err != nil {
		return nil, err
	}

	response := &pb.ListAttributeValuesResponse{
		AttributeValues: make([]*pb.AttributeValue, len(attrValues)),
	}

	for i, attrValue := range attrValues {
		response.AttributeValues[i] = &pb.AttributeValue{
			Id:          attrValue.ID,
			AttributeId: attrValue.AttributeID,
			Value:       attrValue.Value,
			Version:     attrValue.Version,
			CreatedAt:   timestamppb.New(attrValue.CreatedAt),
			UpdatedAt:   timestamppb.New(attrValue.UpdatedAt),
			ArchivedAt:  timestamppb.New(attrValue.ArchivedAt),
		}
	}

	res := connect.NewResponse(response)
	return res, nil
}

func (s *Service) GetAttributeValuesByAttributeID(
	ctx context.Context,
	req *connect.Request[pb.GetAttributeValuesByAttributeIDRequest],
) (*connect.Response[pb.ListAttributeValuesResponse], error) {
	attrValues, err := s.attributeValueService.GetAttributeValuesByAttributeID(ctx, req.Msg.AttributeId)
	if err != nil {
		return nil, err
	}

	response := &pb.ListAttributeValuesResponse{
		AttributeValues: make([]*pb.AttributeValue, len(attrValues)),
	}

	for i, attrValue := range attrValues {
		response.AttributeValues[i] = &pb.AttributeValue{
			Id:          attrValue.ID,
			AttributeId: attrValue.AttributeID,
			Value:       attrValue.Value,
			Version:     attrValue.Version,
			CreatedAt:   timestamppb.New(attrValue.CreatedAt),
			UpdatedAt:   timestamppb.New(attrValue.UpdatedAt),
			ArchivedAt:  timestamppb.New(attrValue.ArchivedAt),
		}
	}

	res := connect.NewResponse(response)
	return res, nil
} 